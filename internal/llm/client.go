package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature float64
	Stop        []string
	// ResponseFormat is sent as the response_format body field when set —
	// e.g. a Fireworks GBNF grammar constraining the completion.
	ResponseFormat any
	// Extra is merged into the JSON body verbatim — used for provider
	// specific knobs like disabling thinking mode.
	Extra map[string]any
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	Content      string
	FinishReason string
	Usage        Usage
}

// Client talks to any OpenAI-compatible chat completions endpoint
// (llama-server locally, Fireworks remotely).
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
	// Headers are attached to every request — used to pin Fireworks calls to
	// one replica (x-session-affinity) so the automatic prefix cache hits and
	// the shared system-prompt prefix is billed at the discount.
	Headers map[string]string

	calls            atomic.Int64
	completionTokens atomic.Int64
}

// Stats reports call and completion-token counts — the hardware-independent
// cost metrics used to evaluate performance changes.
func (c *Client) Stats() (calls, completionTokens int64) {
	return c.calls.Load(), c.completionTokens.Load()
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		BaseURL: baseURL,
		APIKey:  apiKey,
		HTTP:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := map[string]any{
		"model":       req.Model,
		"messages":    req.Messages,
		"temperature": req.Temperature,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if req.ResponseFormat != nil {
		body["response_format"] = req.ResponseFormat
	}
	for k, v := range req.Extra {
		body[k] = v
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	for k, v := range c.Headers {
		httpReq.Header.Set(k, v)
	}

	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chat %s: status %d: %s", req.Model, resp.StatusCode, truncate(string(data), 300))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("chat %s: bad response: %w", req.Model, err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("chat %s: no choices", req.Model)
	}
	c.calls.Add(1)
	c.completionTokens.Add(int64(parsed.Usage.CompletionTokens))
	return &ChatResponse{
		Content:      parsed.Choices[0].Message.Content,
		FinishReason: parsed.Choices[0].FinishReason,
		Usage:        parsed.Usage,
	}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
