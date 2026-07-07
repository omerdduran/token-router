package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Harness-injected (see participant guide).
	FireworksAPIKey  string
	FireworksBaseURL string
	AllowedModels    []string

	InputPath  string
	OutputPath string

	// Local llama-server. If ModelPath is empty the server is not spawned
	// and LocalBaseURL is assumed to point at an already-running instance
	// (dev mode), or local inference is disabled entirely.
	LocalBaseURL   string
	LocalModelPath string
	LocalServerBin string
	LocalCtxSize   int
	LocalParallel  int
	LocalThreads   int
	LocalExtraArgs []string // space-separated llama-server flags for perf experiments

	Workers             int
	TotalBudget         time.Duration // hard cap is 10min; keep a safety margin
	RequestTimeout      time.Duration // remote per-request cap is 30s; keep a margin
	LocalRequestTimeout time.Duration // local calls only race the total budget

	DisableLocal  bool
	DisableRemote bool
}

func FromEnv() *Config {
	return &Config{
		FireworksAPIKey:  os.Getenv("FIREWORKS_API_KEY"),
		FireworksBaseURL: strings.TrimRight(os.Getenv("FIREWORKS_BASE_URL"), "/"),
		AllowedModels:    splitModels(os.Getenv("ALLOWED_MODELS")),

		InputPath:  envStr("INPUT_PATH", "/input/tasks.json"),
		OutputPath: envStr("OUTPUT_PATH", "/output/results.json"),

		LocalBaseURL:   envStr("LOCAL_BASE_URL", "http://127.0.0.1:8080"),
		LocalModelPath: os.Getenv("LOCAL_MODEL_PATH"),
		LocalServerBin: envStr("LOCAL_SERVER_BIN", "llama-server"),
		LocalCtxSize:   envInt("LOCAL_CTX_SIZE", 8192),
		LocalParallel:  envInt("LOCAL_PARALLEL", 4),
		LocalThreads:   envInt("LOCAL_THREADS", 0), // 0 = llama-server default (all cores)
		LocalExtraArgs: strings.Fields(os.Getenv("LOCAL_EXTRA_ARGS")),

		Workers:             envInt("WORKERS", 4),
		TotalBudget:         envDur("TOTAL_BUDGET", 9*time.Minute+15*time.Second),
		RequestTimeout:      envDur("REQUEST_TIMEOUT", 25*time.Second),
		LocalRequestTimeout: envDur("LOCAL_REQUEST_TIMEOUT", 90*time.Second),

		DisableLocal:  envBool("DISABLE_LOCAL", false),
		DisableRemote: envBool("DISABLE_REMOTE", false),
	}
}

func splitModels(s string) []string {
	var out []string
	for _, m := range strings.Split(s, ",") {
		if m = strings.TrimSpace(m); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func envStr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
