package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"tokenrouter/internal/classify"
	"tokenrouter/internal/config"
	"tokenrouter/internal/llm"
	"tokenrouter/internal/router"
	"tokenrouter/internal/task"
)

var reWS = regexp.MustCompile(`\s+`)

// normalizePrompt is the dedup key: case- and whitespace-insensitive.
func normalizePrompt(s string) string {
	return reWS.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lmicroseconds)
	start := time.Now()

	cfg := config.FromEnv()
	ctx, cancel := context.WithDeadline(context.Background(), start.Add(cfg.TotalBudget))
	defer cancel()

	if cfg.FireworksBaseURL == "" {
		log.Printf("fatal: FIREWORKS_BASE_URL not set")
		os.Exit(1)
	}

	tasks, err := task.Read(cfg.InputPath)
	if err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
	log.Printf("loaded %d tasks", len(tasks))

	// Write a full skeleton immediately: even a crash later leaves valid JSON
	// with every task_id present.
	results := make([]task.Result, len(tasks))
	var resultsMu sync.Mutex
	for i, t := range tasks {
		results[i] = task.Result{ID: t.ID, Answer: ""}
	}
	if err := task.WriteAtomic(cfg.OutputPath, results); err != nil {
		log.Printf("fatal: initial write: %v", err)
		os.Exit(1)
	}

	// If the harness kills us early, flush whatever is answered so far —
	// partial answers score better than none, and the JSON stays valid.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		resultsMu.Lock()
		snapshot := make([]task.Result, len(results))
		copy(snapshot, results)
		resultsMu.Unlock()
		if err := task.WriteAtomic(cfg.OutputPath, snapshot); err != nil {
			log.Printf("signal %v: flush failed: %v", sig, err)
			os.Exit(1)
		}
		log.Printf("signal %v: flushed partial results", sig)
		os.Exit(0)
	}()

	fw := llm.NewFireworks(
		llm.NewClient(cfg.FireworksBaseURL, cfg.FireworksAPIKey, cfg.RequestTimeout),
		cfg.AllowedModels,
	)
	deadline, _ := ctx.Deadline()
	r := router.New(fw, router.NewPacer(deadline, len(tasks)), router.Options{
		RetryBudget:    cfg.RetryBudget,
		StopSeqs:       cfg.StopSeqs,
		PuzzleSolvers:  cfg.PuzzleSolvers,
		PromptCompress: cfg.PromptCompress,
		MergeSystem:    cfg.MergeSystem,
		MutationRepair: cfg.MutationRepair,
		SolutionLib:    cfg.SolutionLib,
		Grammar:        cfg.Grammar,
	})

	// Dedup pre-pass: normalized-identical prompts are answered once; the
	// duplicates get a copy at the end. Never pay twice for the same question.
	dupOf := map[int]int{} // duplicate index → representative index
	if cfg.Dedup {
		firstByKey := map[string]int{}
		for i, t := range tasks {
			key := normalizePrompt(t.Prompt)
			if j, seen := firstByKey[key]; seen {
				dupOf[i] = j
				r.Pace.TaskDone() // no work will be spent on this task
			} else {
				firstByKey[key] = i
			}
		}
		if len(dupOf) > 0 {
			log.Printf("dedup: %d duplicate task(s) will reuse answers", len(dupOf))
		}
	}

	// Batch pre-pass (opt-in): group short single-answer tasks so the system
	// prompt is paid once per batch instead of once per task. Free-solvable
	// tasks and parse failures fall through to the normal per-task path.
	individual := make([]int, 0, len(tasks))
	if cfg.BatchSize > 0 {
		buckets := map[classify.Category][]int{}
		for i, t := range tasks {
			if _, isDup := dupOf[i]; isDup {
				continue
			}
			cat, _ := classify.ClassifyScored(t.Prompt)
			if !router.Batchable(cat, t.Prompt) {
				individual = append(individual, i)
				continue
			}
			// Invariant: a misclassified puzzle must still hit the free solver.
			if ans, ok := r.TrySolveFree(cat, t.Prompt); ok {
				results[i].Answer = ans
				r.Pace.TaskDone()
				continue
			}
			buckets[cat] = append(buckets[cat], i)
		}
		for cat, idxs := range buckets {
			for start := 0; start < len(idxs); start += cfg.BatchSize {
				end := min(start+cfg.BatchSize, len(idxs))
				chunk := idxs[start:end]
				prompts := make([]string, len(chunk))
				for k, idx := range chunk {
					prompts[k] = tasks[idx].Prompt
				}
				answers, ok := r.AnswerBatch(ctx, cat, prompts)
				if !ok {
					individual = append(individual, chunk...) // fall back per-task
					continue
				}
				resultsMu.Lock()
				for k, idx := range chunk {
					results[idx].Answer = answers[k]
				}
				resultsMu.Unlock()
				for range chunk {
					r.Pace.TaskDone()
				}
			}
		}
	} else {
		for i := range tasks {
			if _, isDup := dupOf[i]; !isDup {
				individual = append(individual, i)
			}
		}
	}

	// Worker pool over the remaining per-task work.
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				answer := r.Answer(ctx, tasks[idx])
				resultsMu.Lock()
				results[idx].Answer = answer
				resultsMu.Unlock()
			}
		}()
	}
	for _, idx := range individual {
		jobs <- idx
	}
	close(jobs)
	wg.Wait()

	// Fill duplicates from their representatives (answered by now).
	resultsMu.Lock()
	for i, j := range dupOf {
		results[i].Answer = results[j].Answer
	}
	resultsMu.Unlock()

	if err := task.WriteAtomic(cfg.OutputPath, results); err != nil {
		log.Printf("fatal: final write: %v", err)
		os.Exit(1)
	}
	log.Printf("%s", fw.Summary())
	log.Printf("done: %d tasks in %s", len(tasks), time.Since(start).Round(time.Millisecond))
}
