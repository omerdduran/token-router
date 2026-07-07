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
// tokens), then the bundled local model with proof-based verification (zero
// scored tokens), and only unproven answers escalate to Fireworks (scored).
type Router struct {
	Local *llm.Client    // nil when local inference is unavailable
	FW    *llm.Fireworks // nil when remote is disabled (dev/offline)
}

// escalateMaxPromptChars gates escalation by input size: remote input tokens
// are scored, so shipping a long passage (summarization!) to Fireworks costs
// more than the accuracy it could buy. ~4 chars/token → 2400 chars ≈ 600 tok.
const escalateMaxPromptChars = 2400

func (r *Router) Answer(ctx context.Context, t task.Task) string {
	cat := classify.Classify(t.Prompt)
	trace := &decisionTrace{id: t.ID, cat: cat}
	defer trace.emit()

	// Layer 0: word problem → expression → exact evaluation in Go.
	if cat == classify.Math {
		if ans, ok := r.tryDeterministicMath(ctx, t.Prompt); ok {
			trace.layer = "deterministic"
			return ans
		}
	}

	// Layer 1: local model.
	var best string
	if r.Local != nil {
		ans, proven := r.answerLocal(ctx, cat, t.Prompt, trace)
		if proven {
			trace.layer = "local"
			return ans
		}
		best = ans
	}

	// Layer 2: escalate — unless the input is so long that remote input
	// tokens outweigh the plausible accuracy gain.
	if r.FW != nil {
		if len(t.Prompt) > escalateMaxPromptChars && best != "" {
			trace.layer = "local-forced"
			trace.note = "escalation skipped: long input"
			return best
		}
		if ans, err := r.remoteChat(ctx, cat, t.Prompt); err == nil {
			if a := postprocess(cat, ans); a != "" {
				trace.layer = "remote"
				return a
			}
		} else {
			log.Printf("task %s: remote error: %v", t.ID, err)
		}
	}

	trace.layer = "local-unproven"
	if best != "" {
		return best
	}
	return fallbackAnswer
}

// answerLocal produces the best local answer and reports whether it is
// "proven": verified by execution, consistency vote, or format checks strict
// enough for its category. Unproven answers are escalation candidates.
func (r *Router) answerLocal(ctx context.Context, cat classify.Category, prompt string, trace *decisionTrace) (string, bool) {
	ans, err := r.localChat(ctx, cat, prompt, "")
	if err != nil {
		log.Printf("local error: %v", err)
		return "", false
	}
	ans = postprocess(cat, ans)

	switch cat {
	case classify.CodeGen, classify.CodeDebug:
		return r.proveCode(ctx, prompt, ans, trace)

	case classify.Math, classify.Logic:
		if !verify.Check(cat, prompt, ans) {
			return ans, false
		}
		// Free extra samples; require a 2/3 majority on the final answer.
		voted, ok := r.selfConsistent(ctx, cat, prompt, ans)
		trace.consistency = ok
		return voted, ok

	default: // factual, sentiment, summarize, ner
		if verify.Check(cat, prompt, ans) {
			return ans, true
		}
		// One corrective retry fixes most format slips, free of charge.
		if ans2, err2 := r.localChat(ctx, cat, prompt,
			"Your previous answer failed a format check. Follow the required output format exactly."); err2 == nil {
			a2 := postprocess(cat, ans2)
			if verify.Check(cat, prompt, a2) {
				trace.retried = true
				return a2, true
			}
		}
		return ans, false
	}
}

// proveCode runs the syntax gate, then self-generated tests, then one free
// repair round with the failure fed back (CodeT-style acceptance).
func (r *Router) proveCode(ctx context.Context, prompt, ans string, trace *decisionTrace) (string, bool) {
	code := solve.ExtractCode(ans)
	if code == "" {
		return ans, false
	}
	if looksLikePython(prompt, code) {
		sctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err := solve.CheckPythonSyntax(sctx, code)
		cancel()
		if err != nil {
			return ans, false
		}
	}
	passed, tested := r.verifyCodeByTests(ctx, prompt, code)
	trace.codeTested = tested
	if !tested {
		// No tests derivable: syntax-clean code is our best local evidence.
		// Treat as proven for debug tasks (original code as spec), unproven
		// for generation, where silent logic errors are likelier.
		return ans, trace.cat == classify.CodeDebug
	}
	if passed {
		return ans, true
	}
	if fixed, err := r.repairCode(ctx, prompt, code, "self-generated assert failed"); err == nil && fixed != "" {
		if passed2, tested2 := r.verifyCodeByTests(ctx, prompt, fixed); tested2 && passed2 {
			trace.retried = true
			return wrapCode(fixed), true
		}
	}
	return ans, false
}

func wrapCode(code string) string {
	return "```python\n" + code + "\n```"
}

// --- Layer 0: deterministic math ---

var reBareExpr = regexp.MustCompile(`^[\s\d,.$+\-*/^()×÷%]+[?.\s]*$`)

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

// --- Layer 1: local chat ---

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

// --- postprocessing & tracing ---

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

// decisionTrace logs one line per task: which layer answered and why. This
// is the audit trail for eval debugging and the demo.
type decisionTrace struct {
	id          string
	cat         classify.Category
	layer       string
	consistency bool
	retried     bool
	codeTested  bool
	note        string
}

func (d *decisionTrace) emit() {
	parts := []string{"task " + d.id, "cat=" + string(d.cat), "layer=" + d.layer}
	if d.consistency {
		parts = append(parts, "consistent")
	}
	if d.retried {
		parts = append(parts, "retried")
	}
	if d.codeTested {
		parts = append(parts, "code-tested")
	}
	if d.note != "" {
		parts = append(parts, d.note)
	}
	log.Printf("%s", strings.Join(parts, " "))
}
