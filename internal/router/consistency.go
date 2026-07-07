package router

import (
	"context"
	"regexp"
	"strings"

	"tokenrouter/internal/classify"
	"tokenrouter/internal/llm"
)

// Self-consistency voting (Wang et al. 2022): local samples are free, so for
// the categories where a single greedy answer is least reliable (math, logic)
// we draw extra samples at higher temperature and require a majority on the
// normalized final answer. Agreement is a far better confidence signal for
// small models than raw logprobs or "is this correct?" self-evaluation
// (Kadavath et al. 2022, AutoMix 2024).

const consistencyExtraSamples = 2 // first greedy answer + 2 sampled = 3 votes

// Verification samples don't need the full derivation, but zero reasoning
// makes the 4B sampler incompetent (measured: unproven 7→14 with answer-only
// sampling — false disagreements everywhere). Brief-reasoning sampling keeps
// the sampler capable at ~25% of the full-CoT volume.
var terseSampleSystem = map[classify.Category]string{
	classify.Math:  "Solve with the briefest possible working, then end with 'Answer: <number>'.",
	classify.Logic: "Reason briefly (a few lines at most), then end with 'Answer: <solution>'.",
}

var terseSampleMaxTokens = map[classify.Category]int{
	classify.Math:  150,
	classify.Logic: 250,
}

// selfConsistent returns the majority answer and whether a majority (>= 2/3)
// exists. The greedy answer participates as one vote. Under ModeFull the
// samples reason at full depth (measured: fewest false disagreements, hence
// fewest wasted escalations); ModeBrief trades some verification precision
// for ~4x less sample volume.
func (r *Router) selfConsistent(ctx context.Context, mode VerifyMode, cat classify.Category, prompt, greedy string) (string, bool) {
	sys, maxTok := localSystem[cat], localMaxTokens[cat]
	if mode == ModeBrief {
		sys, maxTok = terseSampleSystem[cat], terseSampleMaxTokens[cat]
	}
	votes := []string{greedy}
	for i := 0; i < consistencyExtraSamples; i++ {
		resp, err := r.Local.Chat(ctx, llm.ChatRequest{
			Model:       "local",
			Messages:    []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: prompt}},
			MaxTokens:   maxTok,
			Temperature: 0.7,
		})
		if err != nil {
			continue
		}
		votes = append(votes, postprocess(cat, resp.Content))
	}
	if len(votes) < 2 {
		return greedy, false // sampling failed; no signal either way
	}

	if cat == classify.Math {
		counts := map[string][]string{}
		for _, v := range votes {
			key := normalizeAnswer(cat, v)
			if key == "" {
				continue
			}
			counts[key] = append(counts[key], v)
		}
		need := len(votes)/2 + 1
		for key, group := range counts {
			if len(group) >= need {
				return key, true // the normalized number IS the answer
			}
		}
		return greedy, false
	}
	// Logic: exact text match almost never happens across free-form samples
	// ("Carol sits left" vs "Carol is in the leftmost seat"), so compare
	// content overlap instead — but demand it from EVERY sample (logic is
	// the weakest local category) and at a stricter threshold than the
	// factual gate.
	for _, v := range votes[1:] {
		if !looselyAgreesAt(greedy, v, 0.5) {
			return greedy, false
		}
	}
	return greedy, len(votes) >= 2
}

var reLastNumber = regexp.MustCompile(`-?\d[\d,]*(?:\.\d+)?`)
var rePunct = regexp.MustCompile(`[.,;:!?'"()\[\]{}]`)

// looselyAgrees decides whether two independently sampled free-form answers
// tell the same story: their final numbers must match (when both have any),
// and their stemmed content words must overlap enough. It is the cheap
// stand-in for semantic-entropy clustering used to gate catch-all answers.
func looselyAgrees(a, b string) bool {
	return looselyAgreesAt(a, b, 0.3)
}

func looselyAgreesAt(a, b string, threshold float64) bool {
	numsA := reLastNumber.FindAllString(a, -1)
	numsB := reLastNumber.FindAllString(b, -1)
	numbersMatch := false
	if len(numsA) > 0 && len(numsB) > 0 {
		if strings.ReplaceAll(numsA[len(numsA)-1], ",", "") != strings.ReplaceAll(numsB[len(numsB)-1], ",", "") {
			return false
		}
		numbersMatch = true
	}
	wa, wb := contentWords(a), contentWords(b)
	if len(wa) == 0 || len(wb) == 0 {
		return len(wa) == len(wb)
	}
	// Terse numeric answers ("It equals 42.") carry their meaning in the
	// number; an agreeing number is agreement.
	if numbersMatch && (len(wa) <= 3 || len(wb) <= 3) {
		return true
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	union := len(wa) + len(wb) - inter
	return float64(inter)/float64(union) >= threshold
}

var stopwords = map[string]bool{
	"the": true, "a": true, "an": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "of": true, "to": true, "in": true,
	"on": true, "and": true, "or": true, "it": true, "its": true, "this": true,
	"that": true, "by": true, "for": true, "with": true, "as": true, "at": true,
	"from": true, "has": true, "have": true, "had": true, "not": true,
	"but": true, "they": true, "their": true, "he": true, "she": true,
	"his": true, "her": true, "we": true, "our": true, "you": true,
	"your": true, "i": true, "do": true, "does": true, "did": true,
	"will": true, "would": true, "can": true, "could": true, "should": true,
	"may": true, "might": true, "than": true, "then": true, "so": true,
	"such": true, "if": true, "because": true, "which": true, "who": true,
	"what": true, "when": true, "where": true, "how": true, "also": true,
	"more": true, "most": true, "other": true, "into": true, "over": true,
	"between": true, "each": true, "both": true, "all": true, "any": true,
	"some": true, "only": true, "same": true, "there": true, "these": true,
	"those": true, "while": true, "during": true, "through": true,
}

func contentWords(s string) map[string]bool {
	s = strings.ToLower(rePunct.ReplaceAllString(s, " "))
	out := map[string]bool{}
	for _, w := range strings.Fields(s) {
		if len(w) > 1 && !stopwords[w] {
			out[stem(w)] = true
		}
	}
	return out
}

// stem trims one common English suffix so morphological variants (tilt /
// tilted / tilting, axis / axial) land on the same key. Crude but enough
// for overlap counting.
func stem(w string) string {
	for _, suf := range []string{"ing", "ed", "es", "al", "ly", "s"} {
		if strings.HasSuffix(w, suf) && len(w)-len(suf) >= 3 {
			return w[:len(w)-len(suf)]
		}
	}
	return w
}

// normalizeAnswer maps an answer to a comparable key: for math the final
// number, otherwise lowercase text without punctuation.
func normalizeAnswer(cat classify.Category, s string) string {
	s = strings.TrimSpace(s)
	if cat == classify.Math {
		nums := reLastNumber.FindAllString(s, -1)
		if len(nums) == 0 {
			return ""
		}
		return strings.ReplaceAll(nums[len(nums)-1], ",", "")
	}
	s = strings.ToLower(s)
	s = rePunct.ReplaceAllString(s, "")
	return strings.Join(strings.Fields(s), " ")
}
