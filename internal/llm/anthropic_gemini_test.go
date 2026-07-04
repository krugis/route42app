package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAnthropicComplete(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/messages" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("headers = %v", r.Header)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"result"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":3}}`))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "anthropic", srv.URL), "anthropic")
	resp, err := p.Complete(context.Background(), ChatRequest{
		Model: "claude-sonnet-5",
		Messages: []Message{
			{Role: "system", Content: "be brief"},
			{Role: "user", Content: "hi"},
		},
		MaxTokens: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "result" || resp.Reasoning != "hmm" || resp.FinishReason != "end_turn" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	// System messages move to the top-level field.
	if got["system"] != "be brief" {
		t.Errorf("system = %v", got["system"])
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("messages = %v", msgs)
	}
	if got["max_tokens"].(float64) != 200 {
		t.Errorf("max_tokens = %v", got["max_tokens"])
	}
}

func TestAnthropicDefaultMaxTokens(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "anthropic", srv.URL), "anthropic")
	if _, err := p.Complete(context.Background(), ChatRequest{Model: "claude-haiku-4-5"}); err != nil {
		t.Fatal(err)
	}
	if got["max_tokens"].(float64) != anthropicDefaultMaxTokens {
		t.Errorf("max_tokens = %v, want default %d (Anthropic requires the field)", got["max_tokens"], anthropicDefaultMaxTokens)
	}
}

func TestAnthropicStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":9}}}\n\n"))
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"let me think\"}}\n\n"))
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi \"}}\n\n"))
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"there\"}}\n\n"))
		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n\n"))
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "anthropic", srv.URL), "anthropic")
	ch, err := p.Stream(context.Background(), ChatRequest{Model: "claude-sonnet-5"})
	if err != nil {
		t.Fatal(err)
	}
	var text, reasoning string
	var last Chunk
	for c := range ch {
		if c.Err != nil {
			t.Fatal(c.Err)
		}
		text += c.Delta
		reasoning += c.ReasoningDelta
		last = c
	}
	if text != "Hi there" || reasoning != "let me think" {
		t.Errorf("text=%q reasoning=%q", text, reasoning)
	}
	if !last.Done || last.Usage.PromptTokens != 9 || last.Usage.CompletionTokens != 4 {
		t.Errorf("final = %+v", last)
	}
}

func TestGeminiComplete(t *testing.T) {
	var got map[string]any
	var path, query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, query = r.URL.Path, r.URL.RawQuery
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"pondering...","thought":true},{"text":"four"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":6,"candidatesTokenCount":1}}`))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "gemini", srv.URL), "gemini")
	resp, err := p.Complete(context.Background(), ChatRequest{
		Model: "gemini-3-flash",
		Messages: []Message{
			{Role: "system", Content: "be terse"},
			{Role: "user", Content: "2+2?"},
			{Role: "assistant", Content: "4"},
			{Role: "user", Content: "in words?"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "four" || resp.Reasoning != "pondering..." || resp.FinishReason != "stop" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.PromptTokens != 6 || resp.Usage.CompletionTokens != 1 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if path != "/models/gemini-3-flash:generateContent" {
		t.Errorf("path = %s", path)
	}
	if !strings.Contains(query, "key=test-key") {
		t.Errorf("api key missing from query: %s", query)
	}
	if got["systemInstruction"] == nil {
		t.Error("system message must map to systemInstruction")
	}
	contents := got["contents"].([]any)
	if len(contents) != 3 {
		t.Errorf("contents length = %d, want 3 (system excluded)", len(contents))
	}
	roles := []string{}
	for _, c := range contents {
		roles = append(roles, c.(map[string]any)["role"].(string))
	}
	if roles[1] != "model" {
		t.Errorf("assistant must map to model role, got %v", roles)
	}
}

func TestGeminiStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("alt") != "sse" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Once\"}]}}]}\n\n"))
		w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\" upon\"}]}}],\"usageMetadata\":{\"promptTokenCount\":3,\"candidatesTokenCount\":2}}\n\n"))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "gemini", srv.URL), "gemini")
	ch, err := p.Stream(context.Background(), ChatRequest{Model: "gemini-3-flash"})
	if err != nil {
		t.Fatal(err)
	}
	text, last := collect(t, ch)
	if text != "Once upon" {
		t.Errorf("text = %q", text)
	}
	if !last.Done || last.Usage.CompletionTokens != 2 {
		t.Errorf("final = %+v", last)
	}
}
