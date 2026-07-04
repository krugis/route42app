package analyzer

import (
	"bytes"
	"container/list"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// llmPromptMaxChars caps how much of the user message is sent for
// classification.
const llmPromptMaxChars = 1500

// llmCacheSize bounds the analysis LRU cache (keyed by the last user
// message), so retries and regenerations skip re-analysis.
const llmCacheSize = 512

// LLMAnalyzer classifies prompts with a small local model served by
// Ollama. It is optional, fully local, and free; on any failure —
// timeout, connection error, malformed model output — it falls back to
// the heuristic analyzer so routing is never blocked by analysis.
type LLMAnalyzer struct {
	baseURL  string
	model    string
	timeout  time.Duration
	client   *http.Client
	fallback PromptAnalyzer
	cache    *lruCache
	logger   *slog.Logger
}

// NewLLM returns an analyzer that classifies via the Ollama model at
// baseURL, falling back to fallback (normally NewHeuristic()) on any error.
func NewLLM(baseURL, model string, timeout time.Duration, fallback PromptAnalyzer) *LLMAnalyzer {
	return &LLMAnalyzer{
		baseURL:  strings.TrimRight(baseURL, "/"),
		model:    model,
		timeout:  timeout,
		client:   &http.Client{},
		fallback: fallback,
		cache:    newLRUCache(llmCacheSize),
		logger:   slog.Default(),
	}
}

// Analyze implements PromptAnalyzer.
func (a *LLMAnalyzer) Analyze(ctx context.Context, messages []Message) (AnalysisResult, error) {
	prompt := strings.TrimSpace(lastUserMessage(messages))
	if prompt == "" {
		return a.fallback.Analyze(ctx, messages)
	}

	key := cacheKey(a.model, prompt)
	if res, ok := a.cache.get(key); ok {
		return res, nil
	}

	res, err := a.classify(ctx, prompt)
	if err != nil {
		a.logger.Debug("llm analyzer falling back to heuristic", "error", err)
		// Use the caller's context: the classification context may
		// already be exhausted by the timeout.
		return a.fallback.Analyze(ctx, messages)
	}

	a.cache.put(key, res)
	return res, nil
}

// ollamaGenerateRequest / Response mirror Ollama's /api/generate wire format.
type ollamaGenerateRequest struct {
	Model   string         `json:"model"`
	Prompt  string         `json:"prompt"`
	Stream  bool           `json:"stream"`
	Format  string         `json:"format"`
	Options map[string]any `json:"options,omitempty"`
}

type ollamaGenerateResponse struct {
	Response string `json:"response"`
}

// classification is the JSON contract the model must produce.
type classification struct {
	Category   string   `json:"category"`
	Complexity *float64 `json:"complexity"`
}

func (a *LLMAnalyzer) classify(ctx context.Context, prompt string) (AnalysisResult, error) {
	if len(prompt) > llmPromptMaxChars {
		prompt = prompt[:llmPromptMaxChars]
	}

	instruction := fmt.Sprintf(`Classify this user request. Respond with ONLY JSON:
{"category":"chat|code|math|analysis|general","complexity":0.0-1.0}
complexity: 0=trivial one-liner, 0.5=typical task, 1=multi-constraint expert task.
Request: %s`, prompt)

	body, err := json.Marshal(ollamaGenerateRequest{
		Model:   a.model,
		Prompt:  instruction,
		Stream:  false,
		Format:  "json",
		Options: map[string]any{"temperature": 0},
	})
	if err != nil {
		return AnalysisResult{}, err
	}

	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return AnalysisResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := a.client.Do(req)
	if err != nil {
		return AnalysisResult{}, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return AnalysisResult{}, fmt.Errorf("ollama status %d", resp.StatusCode)
	}

	var gen ollamaGenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&gen); err != nil {
		return AnalysisResult{}, fmt.Errorf("ollama response decode: %w", err)
	}

	var cls classification
	if err := json.Unmarshal([]byte(gen.Response), &cls); err != nil {
		return AnalysisResult{}, fmt.Errorf("model output is not the expected JSON: %w", err)
	}
	if cls.Complexity == nil {
		return AnalysisResult{}, fmt.Errorf("model output missing complexity")
	}
	if !validCategory(cls.Category) {
		return AnalysisResult{}, fmt.Errorf("model output category %q not in whitelist", cls.Category)
	}

	return AnalysisResult{
		Complexity: clamp01(*cls.Complexity),
		Category:   cls.Category,
		Analyzer:   NameLLM,
	}, nil
}

func validCategory(c string) bool {
	switch c {
	case CategoryChat, CategoryCode, CategoryMath, CategoryAnalysis, CategoryGeneral:
		return true
	}
	return false
}

func cacheKey(model, prompt string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(model))
	h.Write([]byte{0})
	h.Write([]byte(prompt))
	return h.Sum64()
}

// lruCache is a minimal fixed-capacity LRU, safe for concurrent use.
type lruCache struct {
	mu       sync.Mutex
	capacity int
	order    *list.List // front = most recently used
	entries  map[uint64]*list.Element
}

type lruEntry struct {
	key uint64
	val AnalysisResult
}

func newLRUCache(capacity int) *lruCache {
	return &lruCache{
		capacity: capacity,
		order:    list.New(),
		entries:  make(map[uint64]*list.Element, capacity),
	}
}

func (c *lruCache) get(key uint64) (AnalysisResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return AnalysisResult{}, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*lruEntry).val, true
}

func (c *lruCache) put(key uint64, val AnalysisResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[key]; ok {
		el.Value.(*lruEntry).val = val
		c.order.MoveToFront(el)
		return
	}
	c.entries[key] = c.order.PushFront(&lruEntry{key: key, val: val})
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*lruEntry).key)
	}
}
