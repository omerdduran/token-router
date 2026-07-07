package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"tokenrouter/internal/config"
	"tokenrouter/internal/llm"
	"tokenrouter/internal/router"
	"tokenrouter/internal/task"
)

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
	r := router.New(fw, router.NewPacer(deadline, len(tasks)), cfg.RetryBudget)

	// Worker pool over tasks; results land at their original index.
	type job struct {
		idx int
		t   task.Task
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < cfg.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				answer := r.Answer(ctx, j.t)
				resultsMu.Lock()
				results[j.idx].Answer = answer
				resultsMu.Unlock()
			}
		}()
	}
	for i, t := range tasks {
		jobs <- job{idx: i, t: t}
	}
	close(jobs)
	wg.Wait()

	if err := task.WriteAtomic(cfg.OutputPath, results); err != nil {
		log.Printf("fatal: final write: %v", err)
		os.Exit(1)
	}
	log.Printf("%s", fw.Summary())
	log.Printf("done: %d tasks in %s", len(tasks), time.Since(start).Round(time.Millisecond))
}
