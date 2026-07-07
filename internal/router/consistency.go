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

// selfConsistent returns the majority answer and whether a majority (>= 2/3)
// exists. The greedy answer participates as one vote.
func (r *Router) selfConsistent(ctx context.Context, cat classify.Category, prompt, greedy string) (string, bool) {
	votes := []string{greedy}
	for i := 0; i < consistencyExtraSamples; i++ {
		resp, err := r.Local.Chat(ctx, llm.ChatRequest{
			Model:       "local",
			Messages:    []llm.Message{{Role: "system", Content: localSystem[cat]}, {Role: "user", Content: prompt}},
			MaxTokens:   localMaxTokens[cat],
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

	counts := map[string][]string{}
	for _, v := range votes {
		key := normalizeAnswer(cat, v)
		if key == "" {
			continue
		}
		counts[key] = append(counts[key], v)
	}
	need := len(votes)/2 + 1
	if cat == classify.Logic {
		// Logic is the local model's weakest category (5/8 on eval) and a
		// confidently-wrong majority is common; demand unanimity to stay local.
		need = len(votes)
	}
	for key, group := range counts {
		if len(group) >= need {
			if cat == classify.Math {
				return key, true // the normalized number IS the answer
			}
			return group[0], true // raw form of a majority member
		}
	}
	return greedy, false
}

var reLastNumber = regexp.MustCompile(`-?\d[\d,]*\.?\d*`)
var rePunct = regexp.MustCompile(`[.,;:!?'"()\[\]{}]`)

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
