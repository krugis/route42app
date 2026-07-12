package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krugis/route42app/internal/config"
)

// fakeOllama serves /api/generate returning the given inner JSON payload,
// counting requests.
func fakeOllama(t *testing.T, innerJSON string, delay time.Duration, calls *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var req ollamaGenerateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("bad request body: %v", err)
		}
		if req.Stream || req.Format != "json" {
			t.Errorf("want stream=false format=json, got %+v", req)
		}
		if calls != nil {
			calls.Add(1)
		}
		time.Sleep(delay)
		json.NewEncoder(w).Encode(ollamaGenerateResponse{Response: innerJSON})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newLLMForTest(url string, timeout time.Duration) *LLMAnalyzer {
	return NewLLM(url, "test-model", timeout, NewHeuristic())
}

func TestLLMHappyPath(t *testing.T) {
	srv := fakeOllama(t, `{"category":"code","complexity":0.7}`, 0, nil)
	res, err := newLLMForTest(srv.URL, time.Second).Analyze(context.Background(), []Message{user("refactor this")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != NameLLM || res.Category != CategoryCode || res.Complexity != 0.7 {
		t.Errorf("got %+v", res)
	}
}

func TestLLMClampsOutOfRangeComplexity(t *testing.T) {
	srv := fakeOllama(t, `{"category":"chat","complexity":3.2}`, 0, nil)
	res, err := newLLMForTest(srv.URL, time.Second).Analyze(context.Background(), []Message{user("hello there")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != NameLLM || res.Complexity != 1.0 {
		t.Errorf("want clamped llm result, got %+v", res)
	}
}

func TestLLMFallsBackOnMalformedJSON(t *testing.T) {
	srv := fakeOllama(t, `certainly! here is my classification`, 0, nil)
	res, err := newLLMForTest(srv.URL, time.Second).Analyze(context.Background(), []Message{user("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != NameHeuristic || res.Category != CategoryChat {
		t.Errorf("want heuristic fallback, got %+v", res)
	}
}

func TestLLMFallsBackOnBadCategory(t *testing.T) {
	srv := fakeOllama(t, `{"category":"poetry","complexity":0.4}`, 0, nil)
	res, err := newLLMForTest(srv.URL, time.Second).Analyze(context.Background(), []Message{user("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != NameHeuristic {
		t.Errorf("want heuristic fallback, got %+v", res)
	}
}

func TestLLMFallsBackOnMissingComplexity(t *testing.T) {
	srv := fakeOllama(t, `{"category":"chat"}`, 0, nil)
	res, err := newLLMForTest(srv.URL, time.Second).Analyze(context.Background(), []Message{user("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != NameHeuristic {
		t.Errorf("want heuristic fallback, got %+v", res)
	}
}

func TestLLMFallsBackOnTimeout(t *testing.T) {
	srv := fakeOllama(t, `{"category":"chat","complexity":0.1}`, 300*time.Millisecond, nil)
	start := time.Now()
	res, err := newLLMForTest(srv.URL, 30*time.Millisecond).Analyze(context.Background(), []Message{user("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != NameHeuristic {
		t.Errorf("want heuristic fallback on timeout, got %+v", res)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Error("fallback must not wait for the slow server")
	}
}

func TestLLMFallsBackOnConnectionRefused(t *testing.T) {
	res, err := newLLMForTest("http://127.0.0.1:1", 200*time.Millisecond).Analyze(context.Background(), []Message{user("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Analyzer != NameHeuristic {
		t.Errorf("want heuristic fallback, got %+v", res)
	}
}

func TestLLMCachesRepeatPrompts(t *testing.T) {
	var calls atomic.Int64
	srv := fakeOllama(t, `{"category":"math","complexity":0.5}`, 0, &calls)
	a := newLLMForTest(srv.URL, time.Second)
	msgs := []Message{user("integrate x^2 from 0 to 1")}
	for i := 0; i < 3; i++ {
		if _, err := a.Analyze(context.Background(), msgs); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("server called %d times, want 1 (cache)", calls.Load())
	}
}

func TestLLMTruncatesLongPrompts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ollamaGenerateRequest
		json.NewDecoder(r.Body).Decode(&req)
		// Few-shot preamble is much longer (~1200+ bytes), so allow more tolerance.
		// Max prompt total: llmPromptMaxChars (1500) + preamble (~1500+) = ~3000+
		// We check the user message is actually truncated, not the total.
		if len(req.Prompt) > llmPromptMaxChars+2000 {
			t.Errorf("prompt not truncated: %d chars", len(req.Prompt))
		}
		json.NewEncoder(w).Encode(ollamaGenerateResponse{Response: `{"category":"general","complexity":0.9}`})
	}))
	defer srv.Close()
	long := make([]byte, 10_000)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := newLLMForTest(srv.URL, time.Second).Analyze(context.Background(), []Message{user(string(long))}); err != nil {
		t.Fatal(err)
	}
}

func TestLRUEviction(t *testing.T) {
	c := newLRUCache(2)
	c.put(1, AnalysisResult{Category: "a"})
	c.put(2, AnalysisResult{Category: "b"})
	c.get(1) // 1 is now most recent
	c.put(3, AnalysisResult{Category: "c"})
	if _, ok := c.get(2); ok {
		t.Error("key 2 should have been evicted")
	}
	if _, ok := c.get(1); !ok {
		t.Error("key 1 should have survived (recently used)")
	}
	if _, ok := c.get(3); !ok {
		t.Error("key 3 should be present")
	}
}

func TestFactory(t *testing.T) {
	cfg := config.Default()
	a, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.(*HeuristicAnalyzer); !ok {
		t.Errorf("default mode must build HeuristicAnalyzer, got %T", a)
	}

	cfg.Analyzer.Mode = config.ModeLLM
	a, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := a.(*LLMAnalyzer); !ok {
		t.Errorf("llm mode must build LLMAnalyzer, got %T", a)
	}

	cfg.Analyzer.Mode = "ml"
	if _, err := New(cfg); err == nil {
		t.Error("unknown mode must error at startup")
	} else if want := fmt.Sprintf("analyzer mode %q", "ml"); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q should mention %q", err, want)
	}
}
