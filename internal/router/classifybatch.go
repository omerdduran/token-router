package router

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"tokenrouter/internal/classify"
	"tokenrouter/internal/llm"
	"tokenrouter/internal/task"
)

const batchClassifyChunk = 16
const batchClassifyClip = 300 // chars per task shown to the classifier

// Classification holds the resolved category for a task and how it was decided.
type Classification struct {
	Cat    classify.Category
	Source string // "regex" or "llm-batch"
}

// ClassifyAll resolves every task's category up front. Strong lexical signals
// stay regex-decided; weak-signal tasks are batched into a few local calls
// (one line per task) instead of one call each — same free tokens, a fraction
// of the request round-trips.
func (r *Router) ClassifyAll(ctx context.Context, tasks []task.Task) []Classification {
	out := make([]Classification, len(tasks))
	var weak []int
	for i, t := range tasks {
		cat, score := classify.ClassifyScored(t.Prompt)
		out[i] = Classification{Cat: cat, Source: "regex"}
		if score < 2 || (cat == classify.Math && len(t.Prompt) > 350) {
			weak = append(weak, i)
		}
	}
	if len(weak) == 0 || r.Local == nil {
		return out
	}
	for start := 0; start < len(weak); start += batchClassifyChunk {
		end := min(start+batchClassifyChunk, len(weak))
		chunk := weak[start:end]
		resolved, err := r.classifyChunk(ctx, tasks, chunk)
		if err != nil {
			log.Printf("batch classify: chunk failed, keeping regex: %v", err)
			continue
		}
		for idx, cat := range resolved {
			out[idx] = Classification{Cat: cat, Source: "llm-batch"}
		}
	}
	return out
}

func (r *Router) classifyChunk(ctx context.Context, tasks []task.Task, idxs []int) (map[int]classify.Category, error) {
	var sb strings.Builder
	for n, idx := range idxs {
		p := tasks[idx].Prompt
		if len(p) > batchClassifyClip {
			p = p[:batchClassifyClip]
		}
		fmt.Fprintf(&sb, "%d: %s\n\n", n+1, strings.ReplaceAll(p, "\n", " "))
	}
	resp, err := r.Local.Chat(ctx, llm.ChatRequest{
		Model: "local",
		Messages: []llm.Message{
			{Role: "system", Content: "Classify each numbered task into exactly one of: factual, math, sentiment, summarize, ner, code_debug, logic, code_gen. Reply with one line per task in the form '<number>: <category>'. No other text."},
			{Role: "user", Content: sb.String()},
		},
		MaxTokens:   len(idxs)*8 + 10,
		Temperature: 0,
	})
	if err != nil {
		return nil, err
	}
	resolved := map[int]classify.Category{}
	for _, line := range strings.Split(resp.Content, "\n") {
		num, word, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(num))
		if err != nil || n < 1 || n > len(idxs) {
			continue
		}
		if cat, ok := llmCategories[strings.TrimSpace(strings.ToLower(word))]; ok {
			resolved[idxs[n-1]] = cat
		}
	}
	return resolved, nil
}
