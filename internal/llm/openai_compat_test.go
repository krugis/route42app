package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testRegistry(t *testing.T, provider, baseURL string) *Registry {
	t.Helper()
	return NewRegistry(
		func(p string) string {
			if p == "ollama" {
				return ""
			}
			return "test-key"
		},
		map[string]string{provider: baseURL},
		"http://localhost:11434",
	)
}

func mustProvider(t *testing.T, r *Registry, name string) Provider {
	t.Helper()
	p, err := r.Provider(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// collect drains a stream into concatenated text and the final chunk.
func collect(t *testing.T, ch <-chan Chunk) (string, Chunk) {
	t.Helper()
	var text strings.Builder
	var last Chunk
	for c := range ch {
		if c.Err != nil {
			return text.String(), c
		}
		text.WriteString(c.Delta)
		last = c
	}
	return text.String(), last
}

func TestOpenAICompatComplete(t *testing.T) {
	var got openAIChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer test-key" {
			t.Errorf("auth = %q", auth)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2}}`))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "groq", srv.URL), "groq")
	resp, err := p.Complete(context.Background(), ChatRequest{
		Model:     "llama-3.3-70b",
		Messages:  []Message{{Role: "user", Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello" || resp.FinishReason != "stop" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 2 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if got.MaxTokens != 100 || got.MaxCompletionTokens != 0 {
		t.Errorf("request tokens = %+v", got)
	}
	if got.Stream {
		t.Error("stream must be false")
	}
}

func TestOpenAIMaxCompletionTokensForNewFamilies(t *testing.T) {
	for _, model := range []string{"gpt-5.2", "o3-mini", "gpt-5-codex"} {
		var got openAIChatRequest
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
		}))
		p := mustProvider(t, testRegistry(t, "openai", srv.URL), "openai")
		if _, err := p.Complete(context.Background(), ChatRequest{Model: model, MaxTokens: 50}); err != nil {
			t.Fatal(err)
		}
		if got.MaxCompletionTokens != 50 || got.MaxTokens != 0 {
			t.Errorf("model %s: want max_completion_tokens, got %+v", model, got)
		}
		srv.Close()
	}
}

func TestOpenRouterQuirks(t *testing.T) {
	var got openAIChatRequest
	var referer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		referer = r.Header.Get("HTTP-Referer")
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "openrouter", srv.URL), "openrouter")
	if _, err := p.Complete(context.Background(), ChatRequest{Model: "meta-llama/llama-3-8b:free"}); err != nil {
		t.Fatal(err)
	}
	if got.Model != "meta-llama/llama-3-8b" {
		t.Errorf(":free suffix not stripped: %q", got.Model)
	}
	if referer == "" {
		t.Error("HTTP-Referer header missing")
	}
}

func TestOpenAICompatMistralBlockContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"choices":[{"message":{"content":[{"type":"thinking","thinking":[{"type":"text","text":"pondering"}]},{"type":"text","text":"answer"}]}}]}`))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "mistral", srv.URL), "mistral")
	resp, err := p.Complete(context.Background(), ChatRequest{Model: "magistral-medium"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "answer" || resp.Reasoning != "pondering" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestOpenAICompatAPIError(t *testing.T) {
	cases := []struct {
		status    int
		retryable bool
	}{
		{401, false},
		{404, false},
		{429, true},
		{500, true},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "provider says no", tc.status)
		}))
		p := mustProvider(t, testRegistry(t, "deepseek", srv.URL), "deepseek")
		_, err := p.Complete(context.Background(), ChatRequest{Model: "deepseek-chat"})
		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("status %d: want APIError, got %v", tc.status, err)
		}
		if apiErr.StatusCode != tc.status || apiErr.Retryable() != tc.retryable {
			t.Errorf("status %d: got code=%d retryable=%v", tc.status, apiErr.StatusCode, apiErr.Retryable())
		}
		srv.Close()
	}
}

func TestOpenAICompatMissingKey(t *testing.T) {
	r := NewRegistry(func(string) string { return "" }, nil, "")
	p := mustProvider(t, r, "openai")
	if _, err := p.Complete(context.Background(), ChatRequest{Model: "gpt-4o-mini"}); err == nil {
		t.Fatal("missing key must error before any network call")
	}
}

func TestOpenAICompatStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req openAIChatRequest
		json.NewDecoder(r.Body).Decode(&req)
		if !req.Stream {
			t.Error("stream must be true")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hel\"}}]}\n\n"))
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n"))
		w.Write([]byte(": keepalive comment\n\n"))
		w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "openai", srv.URL), "openai")
	ch, err := p.Stream(context.Background(), ChatRequest{Model: "gpt-4o-mini"})
	if err != nil {
		t.Fatal(err)
	}
	text, last := collect(t, ch)
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	if !last.Done || last.Usage.PromptTokens != 5 || last.Usage.CompletionTokens != 2 {
		t.Errorf("final chunk = %+v", last)
	}
}

func TestOpenAICompatStreamHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "quota exceeded", 429)
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "openai", srv.URL), "openai")
	if _, err := p.Stream(context.Background(), ChatRequest{Model: "gpt-4o-mini"}); err == nil {
		t.Fatal("HTTP error must fail Stream() synchronously")
	}
}

func TestRegistryAliases(t *testing.T) {
	r := testRegistry(t, "gemini", "http://example.invalid")
	cases := map[string]string{
		"google":     "gemini",
		"moonshotai": "moonshot",
		"dashscope":  "alibaba",
		"z.ai":       "zai",
		"local":      "ollama",
		"OpenAI":     "openai",
	}
	for input, want := range cases {
		p, err := r.Provider(input)
		if err != nil {
			t.Fatalf("%s: %v", input, err)
		}
		if p.Name() != want {
			t.Errorf("Provider(%q).Name() = %q, want %q", input, p.Name(), want)
		}
	}
	if _, err := r.Provider("does-not-exist"); err == nil {
		t.Error("unknown provider must error")
	}
}
