package router

import (
	"context"
	"log"
	"math"
	"regexp"
	"strconv"
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
	cat, score := classify.ClassifyScored(t.Prompt)
	trace := &decisionTrace{id: t.ID, cat: cat, clsSource: "regex"}
	defer trace.emit()

	// Weak lexical signal (unseen phrasing): let the local model classify —
	// free, and far more robust to wording than keyword patterns. Long
	// passages that merely CONTAIN numbers routinely fake a confident math
	// score, so long "math" prompts get a second opinion too.
	weakSignal := score < 2 || (cat == classify.Math && len(t.Prompt) > 350)
	if weakSignal && r.Local != nil {
		if c, ok := r.llmClassify(ctx, t.Prompt); ok {
			cat, trace.cat, trace.clsSource = c, c, "llm"
		}
	}

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
		if ans, err := r.remoteChat(ctx, roleFor(cat), cat, t.Prompt); err == nil {
			if a := postprocess(cat, ans); a != "" {
				trace.layer = "remote"
				// Verification is local and free — apply it to paid answers
				// too. A failed code answer gets one shot at the specialist
				// model before we settle.
				if !r.remoteAcceptable(ctx, cat, t.Prompt, a) &&
					(cat == classify.CodeGen || cat == classify.CodeDebug) {
					if ans2, err2 := r.remoteChat(ctx, llm.RoleCode, cat, t.Prompt); err2 == nil {
						if a2 := postprocess(cat, ans2); a2 != "" && r.remoteAcceptable(ctx, cat, t.Prompt, a2) {
							trace.layer = "remote-code"
							return a2
						}
					}
				}
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
		if !verify.Check(cat, prompt, ans) {
			// One corrective retry fixes most format slips, free of charge.
			ans2, err2 := r.localChat(ctx, cat, prompt,
				"Your previous answer failed a format check. Follow the required output format exactly.")
			if err2 != nil {
				return ans, false
			}
			a2 := postprocess(cat, ans2)
			if !verify.Check(cat, prompt, a2) {
				return ans, false
			}
			trace.retried = true
			ans = a2
		}
		// Factual is the catch-all bucket: misclassified tasks land here and
		// its format check proves nothing. Demand agreement with a second
		// independent sample before trusting the answer.
		if cat == classify.Factual {
			ok := r.factualAgreement(ctx, prompt, ans)
			trace.consistency = ok
			return ans, ok
		}
		// Sentiment labels pass the format check even when the nuance call
		// (sarcasm, reportive-neutral, mixed-but-positive) is wrong — the
		// hard-set showed those slipping through as "proven". Require the
		// label to survive one resample.
		if cat == classify.Sentiment {
			ok := r.sentimentAgreement(ctx, prompt, ans)
			trace.consistency = ok
			return ans, ok
		}
		return ans, true
	}
}

var reSentLabel = regexp.MustCompile(`(?i)\b(positive|negative|neutral|mixed)\b`)

// sentimentAgreement resamples once and accepts only when both samples pick
// the same label. Ambiguous tone flips across samples; confident calls don't.
func (r *Router) sentimentAgreement(ctx context.Context, prompt, greedy string) bool {
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model:       "local",
		Messages:    []llm.Message{{Role: "system", Content: localSystem[classify.Sentiment]}, {Role: "user", Content: prompt}},
		MaxTokens:   localMaxTokens[classify.Sentiment],
		Temperature: 0.7,
	})
	if err != nil {
		return false
	}
	a := reSentLabel.FindString(strings.ToLower(greedy))
	b := reSentLabel.FindString(strings.ToLower(resp.Content))
	return a != "" && a == b
}

// factualAgreement draws one extra sample and accepts the answer only when
// both tellings agree on content — a cheap semantic-entropy stand-in that
// catches the model improvising.
func (r *Router) factualAgreement(ctx context.Context, prompt, greedy string) bool {
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model:       "local",
		Messages:    []llm.Message{{Role: "system", Content: localSystem[classify.Factual]}, {Role: "user", Content: prompt}},
		MaxTokens:   localMaxTokens[classify.Factual],
		Temperature: 0.7,
	})
	if err != nil {
		return false
	}
	return looselyAgrees(greedy, postprocess(classify.Factual, resp.Content))
}

var llmCategories = map[string]classify.Category{
	"factual": classify.Factual, "math": classify.Math,
	"sentiment": classify.Sentiment, "summarize": classify.Summarize,
	"ner": classify.NER, "code_debug": classify.CodeDebug,
	"logic": classify.Logic, "code_gen": classify.CodeGen,
}

func (r *Router) llmClassify(ctx context.Context, prompt string) (classify.Category, bool) {
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model: "local",
		Messages: []llm.Message{
			{Role: "system", Content: "Classify the task into exactly one of: factual, math, sentiment, summarize, ner, code_debug, logic, code_gen. Reply with only the category word."},
			{Role: "user", Content: prompt},
		},
		MaxTokens:   8,
		Temperature: 0,
	})
	if err != nil {
		return "", false
	}
	word := strings.ToLower(strings.Trim(strings.TrimSpace(resp.Content), ".\"'` \n"))
	if i := strings.IndexAny(word, " \n\t"); i > 0 {
		word = word[:i]
	}
	c, ok := llmCategories[word]
	return c, ok
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

// reMultiQuantity flags questions asking for more than one value ("how many
// chickens and how many rabbits") — a single arithmetic expression cannot
// answer those, and a confident wrong number would bypass escalation.
var reMultiQuantity = regexp.MustCompile(`(?i)how (many|much) .{0,60}\band\b .{0,60}(how (many|much)|are there)|\?.*\?|\bnumber of each\b|\beach (animal|kind|type)\b`)

func (r *Router) tryDeterministicMath(ctx context.Context, prompt string) (string, bool) {
	if reMultiQuantity.MatchString(prompt) {
		return "", false
	}
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
	// A bare number is an echo, not a computation — nothing to trust.
	if !strings.ContainsAny(expr, "+-*/^") {
		return "", false
	}
	v, err := solve.EvalExpr(expr)
	if err != nil {
		return "", false
	}
	det := solve.FormatNumber(v)
	// Cross-check: a misrouted task (summarize/logic classified as math)
	// still yields a syntactically valid expression and a confidently wrong
	// number. Only trust the deterministic path when an independent terse
	// answer from the model lands on the same value; otherwise fall through
	// to the normal verify-then-escalate flow.
	check, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model:       "local",
		Messages:    []llm.Message{{Role: "system", Content: "Solve the problem. Reply with ONLY the final number."}, {Role: "user", Content: prompt}},
		MaxTokens:   40,
		Temperature: 0,
	})
	if err != nil || !sameNumber(det, normalizeAnswer(classify.Math, strings.TrimSpace(check.Content))) {
		return "", false
	}
	return det, true
}

// sameNumber compares two numeric strings with float tolerance so "72" and
// "72.0" (or rounding tails) agree.
func sameNumber(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	fa, errA := strconv.ParseFloat(strings.ReplaceAll(a, ",", ""), 64)
	fb, errB := strconv.ParseFloat(strings.ReplaceAll(b, ",", ""), 64)
	if errA != nil || errB != nil {
		return a == b
	}
	tol := 1e-6 * math.Max(1, math.Abs(fa))
	return math.Abs(fa-fb) <= tol
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
	req := llm.ChatRequest{
		Model:       "local",
		Messages:    []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: prompt}},
		MaxTokens:   localMaxTokens[cat],
		Temperature: 0,
	}
	resp, err := r.Local.Chat(ctx, req)
	if err != nil && ctx.Err() == nil {
		// Transient failure (slot contention timeout): one retry is free.
		resp, err = r.Local.Chat(ctx, req)
	}
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

// remoteAcceptable reruns the free local checks on a paid answer: format
// gates always, plus real execution when the answer is runnable code.
func (r *Router) remoteAcceptable(ctx context.Context, cat classify.Category, prompt, answer string) bool {
	if !verify.Check(cat, prompt, answer) {
		return false
	}
	if (cat == classify.CodeGen || cat == classify.CodeDebug) && r.Local != nil {
		code := solve.ExtractCode(answer)
		if code == "" {
			return false
		}
		if passed, tested := r.verifyCodeByTests(ctx, prompt, code); tested {
			return passed
		}
	}
	return true
}

func (r *Router) remoteChat(ctx context.Context, role llm.Role, cat classify.Category, prompt string) (string, error) {
	resp, err := r.FW.Chat(ctx, role, llm.ChatRequest{
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
	clsSource   string
	layer       string
	consistency bool
	retried     bool
	codeTested  bool
	note        string
}

func (d *decisionTrace) emit() {
	parts := []string{"task " + d.id, "cat=" + string(d.cat), "cls=" + d.clsSource, "layer=" + d.layer}
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
