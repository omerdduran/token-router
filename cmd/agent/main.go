package main

import (
	"context"
	"log"
	"net/http"
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

	// Local model: spawn llama-server when a model path is set, otherwise
	// probe LOCAL_BASE_URL for an externally managed server (dev mode).
	var local *llm.Client
	if !cfg.DisableLocal {
		startupCtx, startupCancel := context.WithTimeout(ctx, 45*time.Second)
		if cfg.LocalModelPath != "" {
			srv, err := llm.StartLocal(startupCtx, llm.LocalOptions{
				Bin:       cfg.LocalServerBin,
				ModelPath: cfg.LocalModelPath,
				BaseURL:   cfg.LocalBaseURL,
				CtxSize:   cfg.LocalCtxSize,
				Parallel:  cfg.LocalParallel,
				Threads:   cfg.LocalThreads,
				ExtraArgs: cfg.LocalExtraArgs,
			})
			if err != nil {
				log.Printf("warn: local server failed, continuing remote-only: %v", err)
			} else {
				defer srv.Stop()
				local = llm.NewClient(cfg.LocalBaseURL, "", cfg.LocalRequestTimeout)
			}
		} else if probeHealth(startupCtx, cfg.LocalBaseURL) {
			local = llm.NewClient(cfg.LocalBaseURL, "", cfg.LocalRequestTimeout)
			log.Printf("using external local server at %s", cfg.LocalBaseURL)
		} else {
			log.Printf("warn: no local model configured and %s not healthy", cfg.LocalBaseURL)
		}
		startupCancel()
	}

	var fw *llm.Fireworks
	if !cfg.DisableRemote && cfg.FireworksBaseURL != "" {
		fw = llm.NewFireworks(
			llm.NewClient(cfg.FireworksBaseURL, cfg.FireworksAPIKey, cfg.RequestTimeout),
			cfg.AllowedModels,
		)
	}
	if local == nil && fw == nil {
		log.Printf("fatal: neither local nor remote inference available")
		os.Exit(1)
	}

	deadline, _ := ctx.Deadline()
	r := &router.Router{Local: local, FW: fw, Pace: router.NewPacer(deadline, len(tasks))}

	// Resolve every category up front: strong regex signals directly, the
	// rest batched through the local model in a few calls.
	cls := r.ClassifyAll(ctx, tasks)

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
				answer := r.Answer(ctx, j.t, cls[j.idx])
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
	if fw != nil {
		log.Printf("%s", fw.Summary())
	}
	if local != nil {
		calls, ctoks := local.Stats()
		log.Printf("local: %d calls, %d completion tokens", calls, ctoks)
	}
	log.Printf("done: %d tasks in %s", len(tasks), time.Since(start).Round(time.Millisecond))
}

func probeHealth(ctx context.Context, baseURL string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}
