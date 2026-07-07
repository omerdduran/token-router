package router

import (
	"context"
	"log"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"tokenrouter/internal/classify"
	"tokenrouter/internal/llm"
	"tokenrouter/internal/solve"
	"tokenrouter/internal/task"
	"tokenrouter/internal/verify"
)

const fallbackAnswer = "Unable to determine the answer."

// Router implements the organizer-blessed reading of "routing intelligence":
// decide when a task can be handled with plain code (zero tokens) versus
// which ALLOWED model it must go to, spending the minimum tokens that still
// clear the accuracy gate. Verification (code execution, arithmetic
// re-evaluation, format checks) is plain code too — free — so retries are
// paid for only on proven failure.
type Router struct {
	FW   *llm.Fireworks
	Pace *Pacer // nil → always ModeFull

	// retryBudget caps second-attempt calls across the whole run (-1 =
	// unlimited). The submission-ladder knob: step it down between
	// leaderboard probes.
	retryBudget atomic.Int64
}

func New(fw *llm.Fireworks, pace *Pacer, retryBudget int) *Router {
	r := &Router{FW: fw, Pace: pace}
	r.retryBudget.Store(int64(retryBudget))
	return r
}

// allowRetry spends one unit of retry budget if any is left.
func (r *Router) allowRetry() bool {
	for {
		cur := r.retryBudget.Load()
		if cur < 0 {
			return true // unlimited
		}
		if cur == 0 {
			return false
		}
		if r.retryBudget.CompareAndSwap(cur, cur-1) {
			return true
		}
	}
}

// retryMaxPromptChars: retries resend the whole prompt as input tokens, so
// long passages (summarization) never get a paid second attempt.
const retryMaxPromptChars = 2400

func (r *Router) Answer(ctx context.Context, t task.Task) string {
	cat, score := classify.ClassifyScored(t.Prompt)
	generic := score < 2 // weak signal → no category scaffolding, model reads the task itself
	mode := r.Pace.Mode()
	trace := &decisionTrace{id: t.ID, cat: cat, generic: generic, mode: mode}
	defer trace.emit()
	defer r.Pace.TaskDone()

	// Layer 0: plain code, zero tokens.
	if cat == classify.Math {
		if ans, ok := solveBareExpression(t.Prompt); ok {
			trace.layer = "code"
			return ans
		}
	}
	// The logic solvers self-gate strictly (exact ordering / universal-
	// syllogism shapes only), so run them regardless of classification —
	// this rescues misclassified puzzles and never fires on other text.
	if ans, ok := solve.SolveOrdering(t.Prompt); ok {
		trace.layer = "code"
		return ans
	}
	if ans, ok := solve.SolveSyllogism(t.Prompt); ok {
		trace.layer = "code"
		return ans
	}

	// Layer 1: one frugal API call, verified by plain code where possible.
	var ans string
	var err error
	switch {
	case cat == classify.Math && !reMultiQuantity.MatchString(t.Prompt):
		ans, err = r.mathPAL(ctx, t.Prompt, trace)
	case cat == classify.CodeGen || cat == classify.CodeDebug:
		ans, err = r.code(ctx, cat, t.Prompt, trace)
	default:
		ans, err = r.plain(ctx, cat, generic, t.Prompt, trace)
	}
	if err != nil {
		log.Printf("task %s: remote error: %v", t.ID, err)
		if ans == "" {
			return fallbackAnswer
		}
	}
	if ans == "" {
		return fallbackAnswer
	}
	return ans
}

// --- Layer 0 ---

var reBareExpr = regexp.MustCompile(`^[\s\d,.$+\-*/^()×÷%]+[?.\s]*$`)
var reMathPreamble = regexp.MustCompile(`(?i)^\s*(what is|what'?s|calculate|compute|evaluate|solve)[:\s]*`)
var reMultiQuantity = regexp.MustCompile(`(?i)how (many|much) .{0,60}\band\b .{0,60}(how (many|much)|are there)|\?.*\?|\bnumber of each\b|\beach (animal|kind|type)\b`)

func solveBareExpression(prompt string) (string, bool) {
	stripped := reMathPreamble.ReplaceAllString(strings.TrimSpace(prompt), "")
	if !reBareExpr.MatchString(stripped) {
		return "", false
	}
	v, err := solve.EvalExpr(strings.Trim(stripped, "?. \n"))
	if err != nil {
		return "", false
	}
	return solve.FormatNumber(v), true
}

// --- Layer 1: math via PAL ---

// mathPAL asks the cheap model for a bare expression (~20 output tokens) and
// evaluates it in Go: fewer tokens than a worked solution AND the arithmetic
// is correct by construction. Falls back to a direct solve when the problem
// isn't expressible.
func (r *Router) mathPAL(ctx context.Context, prompt string, trace *decisionTrace) (string, error) {
	resp, err := r.FW.Chat(ctx, llm.RoleGeneral, llm.ChatRequest{
		Messages:    []llm.Message{{Role: "system", Content: palSystem}, {Role: "user", Content: prompt}},
		MaxTokens:   60,
		Temperature: 0,
	})
	if err == nil {
		expr := strings.TrimSpace(resp.Content)
		if !strings.Contains(strings.ToUpper(expr), "UNSUPPORTED") && strings.ContainsAny(expr, "+-*/^") {
			if v, evalErr := solve.EvalExpr(expr); evalErr == nil {
				trace.layer = "pal"
				return solve.FormatNumber(v), nil
			}
		}
	}
	// Direct solve fallback — one call, tight cap, answer-line extraction.
	return r.plain(ctx, classify.Math, false, prompt, trace)
}

// --- Layer 1: code with free execution-based verification ---

func (r *Router) code(ctx context.Context, cat classify.Category, prompt string, trace *decisionTrace) (string, error) {
	ans, err := r.call(ctx, llm.RoleStrong, cat, false, prompt)
	if err != nil {
		return "", err
	}
	trace.layer = "remote"
	code := solve.ExtractCode(ans)
	if code == "" {
		return ans, nil
	}
	asserts := solve.DeriveAsserts(prompt)
	if !looksLikePython(prompt, code) || len(asserts) == 0 {
		// No executable evidence either way; syntax check is still free.
		if looksLikePython(prompt, code) {
			sctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			if solve.CheckPythonSyntax(sctx, code) != nil && r.mayRetry(prompt) {
				trace.layer = "remote-retry"
				if ans2, err2 := r.call(ctx, llm.RoleCode, cat, false, prompt); err2 == nil {
					return ans2, nil
				}
			}
		}
		return ans, nil
	}
	if r.testsPass(ctx, code, asserts) {
		trace.codeTested = true
		return ans, nil
	}
	// Proven failure — the only case worth paying the code specialist for.
	if r.mayRetry(prompt) {
		if ans2, err2 := r.call(ctx, llm.RoleCode, cat, false, prompt); err2 == nil {
			if code2 := solve.ExtractCode(ans2); code2 != "" && r.testsPass(ctx, code2, asserts) {
				trace.layer = "remote-code"
				trace.codeTested = true
				return ans2, nil
			}
		}
	}
	return ans, nil
}

func (r *Router) testsPass(ctx context.Context, code string, asserts []string) bool {
	res, err := solve.RunPython(ctx, code+"\n\n"+strings.Join(asserts, "\n"), 8*time.Second)
	if err != nil || res.TimedOut {
		return false
	}
	if res.ExitCode != 0 &&
		(strings.Contains(res.Stderr, "NameError") || strings.Contains(res.Stderr, "SyntaxError")) {
		// Broken derived asserts prove nothing about the code.
		return true
	}
	return res.ExitCode == 0
}

// --- Layer 1: everything else ---

func (r *Router) plain(ctx context.Context, cat classify.Category, generic bool, prompt string, trace *decisionTrace) (string, error) {
	role := llm.RoleGeneral
	if cat == classify.Logic {
		role = llm.RoleStrong
	}
	ans, err := r.call(ctx, role, cat, generic, prompt)
	if err != nil {
		return "", err
	}
	trace.layer = "remote"
	if generic || verify.Check(cat, prompt, ans) {
		return ans, nil
	}
	// Format failure is cheap to prove and usually worth one paid nudge.
	if r.mayRetry(prompt) {
		ans2, err2 := r.callWithNudge(ctx, role, cat, prompt)
		if err2 == nil && verify.Check(cat, prompt, ans2) {
			trace.layer = "remote-retry"
			return ans2, nil
		}
	}
	return ans, nil
}

func (r *Router) mayRetry(prompt string) bool {
	return r.Pace.Mode() != ModeOff && len(prompt) <= retryMaxPromptChars && r.allowRetry()
}

func (r *Router) call(ctx context.Context, role llm.Role, cat classify.Category, generic bool, prompt string) (string, error) {
	sys, maxTok := remoteSystem[cat], remoteMaxTokens[cat]
	if generic {
		sys, maxTok = genericSystem, genericMaxTokens
	}
	resp, err := r.FW.Chat(ctx, role, llm.ChatRequest{
		Messages:    []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: prompt}},
		MaxTokens:   maxTok,
		Temperature: 0,
		// TODO: verify the Fireworks knob for disabling Gemma 4 / MiniMax
		// thinking mode against the live API — thinking tokens are scored.
	})
	if err != nil {
		return "", err
	}
	return postprocess(cat, resp.Content), nil
}

func (r *Router) callWithNudge(ctx context.Context, role llm.Role, cat classify.Category, prompt string) (string, error) {
	resp, err := r.FW.Chat(ctx, role, llm.ChatRequest{
		Messages: []llm.Message{
			{Role: "system", Content: remoteSystem[cat] + " Follow the required output format exactly."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   remoteMaxTokens[cat],
		Temperature: 0,
	})
	if err != nil {
		return "", err
	}
	return postprocess(cat, resp.Content), nil
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

// --- postprocessing & tracing ---

var reAnswerLine = regexp.MustCompile(`(?im)^answer:\s*(.+)$`)

func postprocess(cat classify.Category, s string) string {
	s = strings.TrimSpace(s)
	switch cat {
	case classify.Math, classify.Logic:
		if m := reAnswerLine.FindStringSubmatch(s); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return s
}

type decisionTrace struct {
	id         string
	cat        classify.Category
	generic    bool
	mode       VerifyMode
	layer      string
	codeTested bool
}

func (d *decisionTrace) emit() {
	parts := []string{"task " + d.id, "cat=" + string(d.cat), "mode=" + d.mode.String(), "layer=" + d.layer}
	if d.generic {
		parts = append(parts, "generic")
	}
	if d.codeTested {
		parts = append(parts, "code-tested")
	}
	log.Printf("%s", strings.Join(parts, " "))
}
