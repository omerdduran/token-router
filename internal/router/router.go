package router

import (
	"context"
	"log"
	"regexp"
	"strings"
	"time"

	"tokenrouter/internal/classify"
	"tokenrouter/internal/llm"
	"tokenrouter/internal/solve"
	"tokenrouter/internal/task"
	"tokenrouter/internal/verify"
)

const fallbackAnswer = "Unable to determine the answer."

// Router implements the 3-layer strategy: deterministic solvers first (zero
// tokens), then the bundled local model (zero scored tokens), and only
// verified failures escalate to Fireworks (scored tokens).
type Router struct {
	Local *llm.Client    // nil when local inference is unavailable
	FW    *llm.Fireworks // nil when remote is disabled (dev/offline)
}

func (r *Router) Answer(ctx context.Context, t task.Task) string {
	cat := classify.Classify(t.Prompt)

	// Layer 0: word problem → expression → exact evaluation in Go.
	if cat == classify.Math {
		if ans, ok := r.tryDeterministicMath(ctx, t.Prompt); ok {
			return ans
		}
	}

	// Layer 1: local model with a format-scaffolded prompt.
	var localAns string
	if r.Local != nil {
		if ans, err := r.localChat(ctx, cat, t.Prompt, ""); err == nil {
			localAns = postprocess(cat, ans)
			if r.verified(ctx, cat, t.Prompt, localAns) {
				return localAns
			}
			// One corrective retry: free, and fixes most format slips.
			if ans2, err2 := r.localChat(ctx, cat, t.Prompt,
				"Your previous answer failed a format check. Follow the required output format exactly."); err2 == nil {
				a2 := postprocess(cat, ans2)
				if r.verified(ctx, cat, t.Prompt, a2) {
					return a2
				}
				if localAns == "" {
					localAns = a2
				}
			}
		} else {
			log.Printf("task %s: local error: %v", t.ID, err)
		}
	}

	// Layer 2: escalate to Fireworks.
	if r.FW != nil {
		if ans, err := r.remoteChat(ctx, cat, t.Prompt); err == nil {
			a := postprocess(cat, ans)
			if a != "" {
				log.Printf("task %s: escalated (%s)", t.ID, cat)
				return a
			}
		} else {
			log.Printf("task %s: remote error: %v", t.ID, err)
		}
	}

	if localAns != "" {
		return localAns // unverified local beats nothing
	}
	return fallbackAnswer
}

// --- Layer 0: deterministic math ---

var reBareExpr = regexp.MustCompile(`^[\s\d,.$+\-*/^()×÷%]+[?.\s]*$`)
var reExprInPrompt = regexp.MustCompile(`\d[\d,.\s]*(?:[+\-*/^×÷]\s*[\d(][\d,.\s()+\-*/^×÷]*)+`)

func (r *Router) tryDeterministicMath(ctx context.Context, prompt string) (string, bool) {
	// Case 1: the prompt essentially IS an expression ("What is 15*(3+2)?").
	stripped := stripMathPreamble(prompt)
	if reBareExpr.MatchString(stripped) {
		if v, err := solve.EvalExpr(strings.Trim(stripped, "?. \n")); err == nil {
			return solve.FormatNumber(v), true
		}
	}
	// Case 2: ask the local model for a translation to pure arithmetic.
	if r.Local == nil {
		return "", false
	}
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model:       "local",
		Messages:    []llm.Message{{Role: "system", Content: mathToExprSystem}, {Role: "user", Content: prompt}},
		MaxTokens:   80,
		Temperature: 0,
	})
	if err != nil {
		return "", false
	}
	expr := strings.TrimSpace(resp.Content)
	if strings.Contains(strings.ToUpper(expr), "UNSUPPORTED") || expr == "" {
		return "", false
	}
	v, err := solve.EvalExpr(expr)
	if err != nil {
		return "", false
	}
	return solve.FormatNumber(v), true
}

var reMathPreamble = regexp.MustCompile(`(?i)^\s*(what is|what'?s|calculate|compute|evaluate|solve)[:\s]*`)

func stripMathPreamble(p string) string {
	return reMathPreamble.ReplaceAllString(strings.TrimSpace(p), "")
}

// --- Layer 1: local ---

func (r *Router) localChat(ctx context.Context, cat classify.Category, prompt, corrective string) (string, error) {
	sys := localSystem[cat]
	if corrective != "" {
		sys += " " + corrective
	}
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model:       "local",
		Messages:    []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: prompt}},
		MaxTokens:   localMaxTokens[cat],
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// verified runs the cheap static checks plus, for code, a real syntax gate.
func (r *Router) verified(ctx context.Context, cat classify.Category, prompt, answer string) bool {
	if !verify.Check(cat, prompt, answer) {
		return false
	}
	if cat == classify.CodeGen || cat == classify.CodeDebug {
		code := solve.ExtractCode(answer)
		if looksLikePython(prompt, code) {
			cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if err := solve.CheckPythonSyntax(cctx, code); err != nil {
				return false
			}
		}
	}
	return true
}

func looksLikePython(prompt, code string) bool {
	lp := strings.ToLower(prompt)
	if strings.Contains(lp, "python") {
		return true
	}
	if strings.Contains(lp, "javascript") || strings.Contains(lp, "java ") ||
		strings.Contains(lp, "c++") || strings.Contains(lp, " go ") || strings.Contains(lp, "rust") {
		return false
	}
	return strings.Contains(code, "def ") || strings.Contains(code, "import ")
}

// --- Layer 2: remote ---

func roleFor(cat classify.Category) llm.Role {
	switch cat {
	case classify.Math, classify.Logic:
		return llm.RoleStrong
	case classify.CodeGen, classify.CodeDebug:
		// gemma-4-31b first: kimi-k2p7-code is a reasoning model whose
		// thinking tokens are scored. RoleCode stays wired for a future
		// second-stage escalation if eval shows we need it.
		return llm.RoleStrong
	default:
		return llm.RoleGeneral
	}
}

func (r *Router) remoteChat(ctx context.Context, cat classify.Category, prompt string) (string, error) {
	resp, err := r.FW.Chat(ctx, roleFor(cat), llm.ChatRequest{
		Messages:    []llm.Message{{Role: "system", Content: remoteSystem[cat]}, {Role: "user", Content: prompt}},
		MaxTokens:   remoteMaxTokens[cat],
		Temperature: 0,
		// TODO(task 5): verify the exact Fireworks knob to disable Gemma 4 /
		// MiniMax thinking mode and set it here — thinking tokens are scored.
	})
	if err != nil {
		return "", err
	}
	return resp.Content, nil
}

// --- postprocessing ---

var reAnswerLine = regexp.MustCompile(`(?im)^answer:\s*(.+)$`)

func postprocess(cat classify.Category, s string) string {
	s = strings.TrimSpace(s)
	switch cat {
	case classify.Math, classify.Logic:
		// Keep reasoning out of the graded answer; surface the final line.
		if m := reAnswerLine.FindStringSubmatch(s); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return s
}
