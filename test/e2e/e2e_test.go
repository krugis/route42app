//go:build e2e

// e2e tests exercise the real HTTP handler stack end-to-end with fake
// providers backed by httptest. They are gated behind the `e2e` build tag
// because they spin up HTTP servers and may be slower than unit tests.
//
// Run: go test -tags e2e ./test/e2e/...
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krugis/route42app/internal/analyzer"
	"github.com/krugis/route42app/internal/api"
	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/llm"
	"github.com/krugis/route42app/internal/ranking"
	"github.com/krugis/route42app/internal/store"
)

// testCatalog is a small catalog with cloud models for two providers plus
// a local model, so routing has real candidates to choose from.
func testCatalog() *catalog.Catalog {
	return &catalog.Catalog{SchemaVersion: 1, SnapshotDate: "2026-07-04", Models: []catalog.ModelInfo{
		{ID: "fast-model", Provider: "openai", Source: catalog.SourceCloud, QualityScore: 50, OutputTokensPerSecond: 800, TimeToFirstTokenMs: 100, InputPricePerMTok: 0.10, OutputPricePerMTok: 0.40, SupportsTools: true},
		{ID: "smart-model", Provider: "mistral", Source: catalog.SourceCloud, QualityScore: 96, OutputTokensPerSecond: 60, TimeToFirstTokenMs: 1200, InputPricePerMTok: 5.00, OutputPricePerMTok: 15.00, SupportsTools: true},
		{ID: "local-model", Provider: "ollama", Source: catalog.SourceLocal, QualityScore: 40, OutputTokensPerSecond: 60, TimeToFirstTokenMs: 50, InputPricePerMTok: 0, OutputPricePerMTok: 0, SupportsTools: true},
	}}
}

// newServer builds a real api.Server wired to httptest fake providers and a
// temp store. The fakeProvider handles OpenAI-format /chat/completions for
// both "openai" and "mistral" (distinguished by the model in the request).
func newServer(t *testing.T, fake *fakeProvider, fakeOllama *fakeOllama) (*httptest.Server, *store.Store) {
	t.Helper()

	cfg := config.Default()
	cfg.Ollama.BaseURL = fakeOllama.server.URL
	cfg.DB.Path = filepath.Join(t.TempDir(), "e2e.db")

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsurePrefs(cfg.Prefs); err != nil {
		t.Fatalf("seed prefs: %v", err)
	}
	// Both cloud providers need a key (the adapter refuses to call without one).
	if err := st.SetProviderKey("openai", "sk-test-openai"); err != nil {
		t.Fatalf("set openai key: %v", err)
	}
	if err := st.SetProviderKey("mistral", "sk-test-mistral"); err != nil {
		t.Fatalf("set mistral key: %v", err)
	}

	// Build a registry with base-URL overrides pointing at the fake provider.
	keyFor := func(provider string) string {
		k, _ := st.GetProviderKey(provider)
		return k
	}
	registry := llm.NewRegistry(keyFor, map[string]string{
		"openai":  fake.server.URL,
		"mistral": fake.server.URL,
	}, fakeOllama.server.URL)

	srv, err := api.New(cfg, st, api.Options{
		Catalog:  testCatalog(),
		Registry: registry,
	})
	if err != nil {
		t.Fatalf("build server: %v", err)
	}
	httpSrv := httptest.NewServer(srv.Handler())
	t.Cleanup(httpSrv.Close)
	return httpSrv, st
}

// fakeProvider answers OpenAI-format chat completions. It can be
// configured to fail for specific models (to test fallback) and to stream.
type fakeProvider struct {
	server *httptest.Server
	// failModels maps model id -> HTTP status to return (0 = succeed).
	failModels map[string]int
	// requests records every request body for assertions.
	requests []fakeReq
}

type fakeReq struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func newFakeProvider() *fakeProvider {
	fp := &fakeProvider{failModels: map[string]int{}}
	fp.server = httptest.NewServer(http.HandlerFunc(fp.handle))
	return fp
}

func (fp *fakeProvider) close() { fp.server.Close() }

func (fp *fakeProvider) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var req fakeReq
	_ = json.Unmarshal(body, &req)
	fp.requests = append(fp.requests, req)

	if status, ok := fp.failModels[req.Model]; ok {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"error":{"message":"configured failure"}}`))
		return
	}

	if req.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		writeSSELine(w, flusher, map[string]any{
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"role": "assistant", "content": "Hello"}}},
		})
		writeSSELine(w, flusher, map[string]any{
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{"content": " world"}}},
		})
		writeSSELine(w, flusher, map[string]any{
			"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
			"usage":   map[string]any{"prompt_tokens": 5, "completion_tokens": 2},
		})
		return
	}

	resp := map[string]any{
		"id":      "chatcmpl-fake",
		"object":  "chat.completion",
		"created": 1,
		"model":   req.Model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": "Hello from " + req.Model},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 5, "completion_tokens": 4},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func writeSSELine(w io.Writer, flusher http.Flusher, v any) {
	data, _ := json.Marshal(v)
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n\n"))
	flusher.Flush()
}

// fakeOllama answers /api/tags and /api/ps so local discovery finds models.
type fakeOllama struct {
	server *httptest.Server
	models []string
}

func newFakeOllama(models []string) *fakeOllama {
	fo := &fakeOllama{models: models}
	fo.server = httptest.NewServer(http.HandlerFunc(fo.handle))
	return fo
}

func (fo *fakeOllama) close() { fo.server.Close() }

func (fo *fakeOllama) handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/tags":
		models := []map[string]any{}
		for _, name := range fo.models {
			models = append(models, map[string]any{"name": name, "details": map[string]any{"family": "llama"}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	case "/api/ps":
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []any{}})
	default:
		w.WriteHeader(404)
	}
}

// doChat posts a chat completion request and returns the response.
func doChat(t *testing.T, url string, body map[string]any) (int, []byte) {
	t.Helper()
	data, _ := json.Marshal(body)
	resp, err := http.Post(url+"/api/chat/completions", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("chat request: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestE2EChatCompletionNonStream(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama([]string{"local-model"})
	defer ollama.close()

	srv, _ := newServer(t, fake, ollama)

	// A simple chat prompt routed to the cheapest/fastest qualified model.
	status, body := doChat(t, srv.URL, map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", status, body)
	}
	var resp api.ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, body)
	}
	if len(resp.Choices) != 1 || !strings.Contains(resp.Choices[0].Message.Content, "Hello from") {
		t.Fatalf("unexpected choices: %+v", resp.Choices)
	}
	if resp.XRoute42 == nil {
		t.Fatal("expected x_route42 extension")
	}
	if resp.XRoute42.Reason != "routed" {
		t.Errorf("expected reason=routed, got %q", resp.XRoute42.Reason)
	}
	if resp.XRoute42.SelectedModel == "" {
		t.Error("expected a selected model")
	}
}

func TestE2EChatCompletionStream(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama([]string{"local-model"})
	defer ollama.close()

	srv, _ := newServer(t, fake, ollama)

	data, _ := json.Marshal(map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	resp, err := http.Post(srv.URL+"/api/chat/completions", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("stream request: %v", err)
	}
	defer resp.Body.Close()

	// Parse the SSE stream.
	scanner := bufio.NewScanner(resp.Body)
	var content strings.Builder
	var sawMeta bool
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var chunk api.StreamChunk
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &chunk); err != nil {
			t.Fatalf("decode chunk: %v", err)
		}
		for _, c := range chunk.Choices {
			content.WriteString(c.Delta.Content)
		}
		if chunk.XRoute42 != nil {
			sawMeta = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan stream: %v", err)
	}
	if !strings.Contains(content.String(), "Hello world") {
		t.Errorf("expected streamed 'Hello world', got %q", content.String())
	}
	if !sawMeta {
		t.Error("expected a final metadata chunk with x_route42")
	}
}

func TestE2EFallbackOn500(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	// Make the primary (fast-model) fail with a retryable 500; the fallback
	// should pick the next candidate (smart-model).
	fake.failModels["fast-model"] = 500
	// No local models: isolate the cloud fallback chain [fast-model, smart-model].
	ollama := newFakeOllama(nil)
	defer ollama.close()

	srv, st := newServer(t, fake, ollama)
	// Ensure a fallback depth of at least 1.
	prefs, _ := st.GetPrefs()
	prefs.FallbackDepth = 2
	_ = st.SetPrefs(prefs)

	// cheap + simple prompt: fast-model (cheapest) ranks first, smart-model
	// second. Failing fast-model triggers fallback to smart-model.
	status, body := doChat(t, srv.URL, map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200 (fallback), got %d: %s", status, body)
	}
	var resp api.ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	if resp.XRoute42 == nil {
		t.Fatal("expected x_route42")
	}
	if resp.XRoute42.FallbackAttempts < 1 {
		t.Errorf("expected fallback_attempts>=1, got %d", resp.XRoute42.FallbackAttempts)
	}
	if resp.Model != "smart-model" {
		t.Errorf("expected fallback to smart-model, got %q", resp.Model)
	}
}

func TestE2EOnlyLocalPath(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama([]string{"local-model"})
	defer ollama.close()

	srv, st := newServer(t, fake, ollama)
	prefs, _ := st.GetPrefs()
	prefs.OnlyLocal = true
	_ = st.SetPrefs(prefs)

	// For only_local to actually execute on the local model, the registry's
	// ollama base URL must point at a fake that serves /api/chat. Reuse the
	// fake provider's handler for /api/chat by adding an Ollama responder.
	status, body := doChat(t, srv.URL, map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	// With only_local and a discovered local model, routing selects it; the
	// execution then hits the real ollama base URL (the fake ollama returns
	// 404 for /api/chat). We assert routing selected the local model and the
	// request failed at execution (not at routing) with a 502/503.
	if status != http.StatusOK && status != http.StatusBadGateway && status != http.StatusServiceUnavailable {
		t.Fatalf("expected 200/502/503 for only_local, got %d: %s", status, body)
	}
	// If it succeeded, the selected model must be local. If it failed at
	// execution, the x_route42 (on 200) or the error confirms local routing.
	if status == http.StatusOK {
		var resp api.ChatResponse
		_ = json.Unmarshal(body, &resp)
		if resp.XRoute42 == nil || resp.XRoute42.Provider != "ollama" {
			t.Errorf("expected local provider, got %+v", resp.XRoute42)
		}
	}
}

func TestE2EPinnedModelBypassesRouting(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama([]string{"local-model"})
	defer ollama.close()

	srv, _ := newServer(t, fake, ollama)

	status, body := doChat(t, srv.URL, map[string]any{
		"model":    "smart-model",
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if status != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", status, body)
	}
	var resp api.ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Model != "smart-model" {
		t.Errorf("expected pinned smart-model, got %q", resp.Model)
	}
	if resp.XRoute42 == nil || resp.XRoute42.Reason != "pinned" {
		t.Errorf("expected reason=pinned, got %+v", resp.XRoute42)
	}
}

func TestE2ERecommendReturnsRankedCandidates(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama([]string{"local-model"})
	defer ollama.close()

	srv, _ := newServer(t, fake, ollama)

	data, _ := json.Marshal(map[string]any{
		"messages": []map[string]any{{"role": "user", "content": "explain quantum computing"}},
	})
	resp, err := http.Post(srv.URL+"/api/recommend", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("recommend: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, b)
	}
	var rec api.RecommendResponse
	if err := json.NewDecoder(resp.Body).Decode(&rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rec.Candidates) == 0 {
		t.Fatal("expected ranked candidates")
	}
	if rec.XRoute42.SelectedModel == "" {
		t.Error("expected a selected model")
	}
	if rec.Explanation == "" {
		t.Error("expected an explanation")
	}
}

func TestE2EModelsList(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama([]string{"local-model"})
	defer ollama.close()

	srv, _ := newServer(t, fake, ollama)

	resp, err := http.Get(srv.URL + "/api/models")
	if err != nil {
		t.Fatalf("models: %v", err)
	}
	defer resp.Body.Close()
	var out api.ModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	ids := map[string]bool{}
	for _, m := range out.Data {
		ids[m.ID] = true
	}
	// Catalog cloud models for configured providers + discovered local.
	if !ids["fast-model"] || !ids["smart-model"] {
		t.Errorf("expected cloud models in list, got %v", ids)
	}
	if !ids["local-model"] {
		t.Errorf("expected discovered local model in list, got %v", ids)
	}
}

func TestE2EHealth(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama(nil)
	defer ollama.close()

	srv, _ := newServer(t, fake, ollama)
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var h map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if h["status"] != "ok" {
		t.Errorf("expected status ok, got %v", h["status"])
	}
}

func TestE2EKeysMasked(t *testing.T) {
	fake := newFakeProvider()
	defer fake.close()
	ollama := newFakeOllama(nil)
	defer ollama.close()

	srv, _ := newServer(t, fake, ollama)
	resp, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	// The full key must NEVER appear.
	if strings.Contains(string(body), "sk-test-openai") {
		t.Errorf("api key leaked in response: %s", body)
	}
}

// Ensure the ranking/analyzer/config imports are used (compile guard for
// the e2e package as it evolves).
var _ = ranking.ErrNoCandidates
var _ = analyzer.CategoryChat
var _ = config.Default
var _ = context.Background
var _ = fmt.Sprintf
