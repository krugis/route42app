package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOllamaComplete(t *testing.T) {
	var got ollamaChatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"message":{"content":"local answer"},"done":true,"done_reason":"stop","prompt_eval_count":12,"eval_count":5}`))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "ollama", srv.URL), "ollama")
	resp, err := p.Complete(context.Background(), ChatRequest{
		Model:     "llama3.2:3b",
		Messages:  []Message{{Role: "user", Content: "hi"}},
		MaxTokens: 64,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "local answer" || resp.FinishReason != "stop" {
		t.Errorf("resp = %+v", resp)
	}
	if resp.Usage.PromptTokens != 12 || resp.Usage.CompletionTokens != 5 {
		t.Errorf("usage = %+v", resp.Usage)
	}
	if got.Stream {
		t.Error("stream must be false")
	}
	if got.Options["num_predict"].(float64) != 64 {
		t.Errorf("options = %v", got.Options)
	}
}

func TestOllamaStreamNDJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.Write([]byte(`{"message":{"content":"Hel"},"done":false}` + "\n"))
		w.Write([]byte(`{"message":{"content":"lo"},"done":false}` + "\n"))
		w.Write([]byte(`{"message":{"content":""},"done":true,"prompt_eval_count":8,"eval_count":2}` + "\n"))
	}))
	defer srv.Close()

	p := mustProvider(t, testRegistry(t, "ollama", srv.URL), "ollama")
	ch, err := p.Stream(context.Background(), ChatRequest{Model: "llama3.2:3b"})
	if err != nil {
		t.Fatal(err)
	}
	text, last := collect(t, ch)
	if text != "Hello" {
		t.Errorf("text = %q", text)
	}
	if !last.Done || last.Usage.PromptTokens != 8 || last.Usage.CompletionTokens != 2 {
		t.Errorf("final = %+v", last)
	}
}

func TestOllamaNoKeyNeeded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("ollama must not send Authorization")
		}
		w.Write([]byte(`{"message":{"content":"ok"},"done":true}`))
	}))
	defer srv.Close()

	// Registry with no keys at all.
	r := NewRegistry(func(string) string { return "" }, map[string]string{"ollama": srv.URL}, "")
	p := mustProvider(t, r, "ollama")
	if _, err := p.Complete(context.Background(), ChatRequest{Model: "phi3"}); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			w.Write([]byte(`{"models":[
				{"name":"llama3.2:3b","size":2000000000,"details":{"family":"llama","parameter_size":"3.2B","quantization_level":"Q4_K_M"}},
				{"name":"qwen2.5:0.5b","size":400000000,"details":{"family":"qwen2"}}
			]}`))
		case "/api/ps":
			w.Write([]byte(`{"models":[{"name":"qwen2.5:0.5b"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	models, err := DiscoverOllama(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
	if models[0].Name != "llama3.2:3b" || models[0].Family != "llama" || models[0].Parameters != "3.2B" || models[0].Running {
		t.Errorf("model[0] = %+v", models[0])
	}
	if !models[1].Running {
		t.Errorf("qwen must be marked running: %+v", models[1])
	}
}

func TestDiscoverOllamaPsFailureNonFatal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.Write([]byte(`{"models":[{"name":"phi3:latest","size":1}]}`))
			return
		}
		http.Error(w, "ps broken", 500)
	}))
	defer srv.Close()

	models, err := DiscoverOllama(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].Family != "phi3" {
		t.Errorf("models = %+v", models)
	}
}

func TestDiscoverOllamaUnreachable(t *testing.T) {
	if _, err := DiscoverOllama(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Fatal("unreachable ollama must return an error (caller treats as no local models)")
	}
}

func TestValidateKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("Authorization") {
		case "Bearer good-key":
			w.Write([]byte(`{"data":[]}`))
		default:
			http.Error(w, `{"error":"invalid key"}`, 401)
		}
	}))
	defer srv.Close()

	r := testRegistry(t, "groq", srv.URL)

	res, err := r.ValidateKey(context.Background(), "groq", "good-key")
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsValid || res.StatusCode != 200 {
		t.Errorf("res = %+v", res)
	}

	res, err = r.ValidateKey(context.Background(), "groq", "bad-key")
	if err != nil {
		t.Fatal(err)
	}
	if res.IsValid || res.StatusCode != 401 || res.ErrorMessage == "" {
		t.Errorf("res = %+v", res)
	}
}

func TestValidateKeyProviderSpecificAuth(t *testing.T) {
	var anthropicHeader, geminiHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		anthropicHeader = r.Header.Get("x-api-key")
		geminiHeader = r.Header.Get("X-goog-api-key")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	r := NewRegistry(func(string) string { return "" },
		map[string]string{"anthropic": srv.URL, "gemini": srv.URL}, "")

	if _, err := r.ValidateKey(context.Background(), "anthropic", "ak"); err != nil {
		t.Fatal(err)
	}
	if anthropicHeader != "ak" {
		t.Errorf("anthropic header = %q", anthropicHeader)
	}
	if _, err := r.ValidateKey(context.Background(), "google", "gk"); err != nil {
		t.Fatal(err)
	}
	if geminiHeader != "gk" {
		t.Errorf("gemini header = %q", geminiHeader)
	}
}

func TestValidateKeyUnknownProvider(t *testing.T) {
	r := NewRegistry(func(string) string { return "" }, nil, "")
	if _, err := r.ValidateKey(context.Background(), "made-up", "k"); err == nil {
		t.Fatal("unknown provider must error")
	}
}

func TestValidateKeyNetworkFailureIsNotAnError(t *testing.T) {
	r := NewRegistry(func(string) string { return "" },
		map[string]string{"openai": "http://127.0.0.1:1"}, "")
	res, err := r.ValidateKey(context.Background(), "openai", "k")
	if err != nil {
		t.Fatalf("network failure must not return error, got %v", err)
	}
	if res.IsValid || res.ErrorMessage == "" {
		t.Errorf("res = %+v", res)
	}
}
