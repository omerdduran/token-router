package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestChatBodySerialization(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(raw, &parsed)
		got = parsed // fresh map per request — stale keys must not linger
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "", 5*time.Second)
	_, err := c.Chat(context.Background(), ChatRequest{
		Model:          "m",
		Messages:       []Message{{Role: "user", Content: "hi"}},
		MaxTokens:      7,
		Stop:           []string{"\n\n"},
		ResponseFormat: map[string]any{"type": "grammar", "grammar": "root ::= \"x\""},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got["max_tokens"] != float64(7) {
		t.Errorf("max_tokens = %v", got["max_tokens"])
	}
	if _, ok := got["stop"]; !ok {
		t.Error("stop missing from body")
	}
	rf, ok := got["response_format"].(map[string]any)
	if !ok || rf["type"] != "grammar" {
		t.Errorf("response_format missing or wrong: %v", got["response_format"])
	}

	// And absent when unset — a stray null field could 400 on strict servers.
	_, err = c.Chat(context.Background(), ChatRequest{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, present := got["response_format"]; present {
		t.Error("response_format must be omitted when unset")
	}
	if _, present := got["stop"]; present {
		t.Error("stop must be omitted when unset")
	}
}
