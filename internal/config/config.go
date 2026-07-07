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

	Workers        int
	TotalBudget    time.Duration // hard cap is 10min; keep a safety margin
	RequestTimeout time.Duration // per-request cap is 30s; keep a margin

	// RetryBudget caps paid second-attempt calls across the run (-1 =
	// unlimited). The submission-ladder knob for leaderboard probing.
	RetryBudget int
}

func FromEnv() *Config {
	return &Config{
		FireworksAPIKey:  os.Getenv("FIREWORKS_API_KEY"),
		FireworksBaseURL: strings.TrimRight(os.Getenv("FIREWORKS_BASE_URL"), "/"),
		AllowedModels:    splitModels(os.Getenv("ALLOWED_MODELS")),

		InputPath:  envStr("INPUT_PATH", "/input/tasks.json"),
		OutputPath: envStr("OUTPUT_PATH", "/output/results.json"),

		Workers:        envInt("WORKERS", 4),
		TotalBudget:    envDur("TOTAL_BUDGET", 9*time.Minute+15*time.Second),
		RequestTimeout: envDur("REQUEST_TIMEOUT", 25*time.Second),

		RetryBudget: envInt("RETRY_BUDGET", -1),
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

func envDur(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
