package router

import (
	"context"
	"fmt"
	"strings"
	"time"

	"tokenrouter/internal/classify"
	"tokenrouter/internal/llm"
	"tokenrouter/internal/solve"
)

// Self-generated test verification (CodeT, 2022): the local model writes a
// few asserts from the task description, and we actually execute the
// candidate code against them. A pass upgrades the answer from "plausible"
// to "proven", which is what lets us keep code tasks — the most expensive
// escalations — local.

const assertGenSystem = "Write up to 3 Python assert statements that test the function described by the user. Use only the function name and behavior from the description. Output ONLY the assert lines, no imports, no prose. If you cannot infer concrete test cases, output NONE."

const codeRunTimeout = 8 * time.Second

// verifyCodeByTests returns (passed, tested): tested=false means we could not
// build meaningful tests and learned nothing.
func (r *Router) verifyCodeByTests(ctx context.Context, prompt, code string) (bool, bool) {
	if !looksLikePython(prompt, code) {
		return false, false // execution harness only speaks Python for now
	}
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model:       "local",
		Messages:    []llm.Message{{Role: "system", Content: assertGenSystem}, {Role: "user", Content: prompt}},
		MaxTokens:   150,
		Temperature: 0,
	})
	if err != nil {
		return false, false
	}
	asserts := extractAsserts(resp.Content)
	if len(asserts) == 0 {
		return false, false
	}
	res, err := solve.RunPython(ctx, code+"\n\n"+strings.Join(asserts, "\n"), codeRunTimeout)
	if err != nil || res.TimedOut {
		return false, false
	}
	return res.ExitCode == 0, true
}

// repairCode feeds the failure back to the local model once — a free fix
// attempt before any paid escalation.
func (r *Router) repairCode(ctx context.Context, prompt, code, failure string) (string, error) {
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model: "local",
		Messages: []llm.Message{
			{Role: "system", Content: "Fix the code so it satisfies the task. Output only the corrected code in one fenced block."},
			{Role: "user", Content: fmt.Sprintf("Task:\n%s\n\nCurrent code:\n```python\n%s\n```\n\nFailure:\n%s", prompt, code, truncateStr(failure, 400))},
		},
		MaxTokens:   localMaxTokens[classify.CodeGen],
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}
	return solve.ExtractCode(resp.Content), nil
}

func extractAsserts(s string) []string {
	s = solve.ExtractCode(s)
	if strings.Contains(strings.ToUpper(s), "NONE") && !strings.Contains(s, "assert") {
		return nil
	}
	var out []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "assert ") {
			out = append(out, line)
		}
	}
	if len(out) > 3 {
		out = out[:3]
	}
	return out
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
