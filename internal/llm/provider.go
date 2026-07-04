package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Provider executes chat completions against one upstream API.
// Implementations are safe for concurrent use.
type Provider interface {
	// Name is the canonical provider name (e.g. "openai", "ollama").
	Name() string
	Complete(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// Stream starts a streaming completion. The returned channel is
	// closed after the final (Done or Err) chunk; the caller must drain it.
	Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
}

// KeyFunc resolves the API key for a canonical provider name, returning
// "" when the provider is not configured. Keys are looked up per call so
// runtime key changes take effect immediately.
type KeyFunc func(provider string) string

// openAICompatEndpoints maps canonical names of OpenAI-compatible
// providers to their default chat-completions base URL (the adapter
// appends /chat/completions).
var openAICompatEndpoints = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"groq":       "https://api.groq.com/openai/v1",
	"mistral":    "https://api.mistral.ai/v1",
	"deepseek":   "https://api.deepseek.com/v1",
	"moonshot":   "https://api.moonshot.ai/v1",
	"alibaba":    "https://dashscope-intl.aliyuncs.com/compatible-mode/v1",
	"nvidia":     "https://integrate.api.nvidia.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"zai":        "https://api.z.ai/api/paas/v4",
}

// aliases maps accepted provider spellings to canonical names.
var aliases = map[string]string{
	"google":        "gemini",
	"google-gemini": "gemini",
	"moonshotai":    "moonshot",
	"dashscope":     "alibaba",
	"z-ai":          "zai",
	"z.ai":          "zai",
	"zhipu":         "zai",
	"local":         "ollama",
}

// CanonicalProvider normalizes a provider name, resolving aliases.
func CanonicalProvider(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if c, ok := aliases[n]; ok {
		return c
	}
	return n
}

// Registry builds and caches provider adapters. Base URLs can be
// overridden per provider (config `providers.<name>.base_url`), which
// also enables offline testing against local fakes.
type Registry struct {
	keyFor    KeyFunc
	baseURLs  map[string]string // canonical name -> override
	ollamaURL string
	client    *http.Client
}

// NewRegistry creates a Registry. keyFor must not be nil; baseOverrides
// may be nil.
func NewRegistry(keyFor KeyFunc, baseOverrides map[string]string, ollamaBaseURL string) *Registry {
	normalized := make(map[string]string, len(baseOverrides))
	for name, u := range baseOverrides {
		if u != "" {
			normalized[CanonicalProvider(name)] = strings.TrimRight(u, "/")
		}
	}
	return &Registry{
		keyFor:    keyFor,
		baseURLs:  normalized,
		ollamaURL: strings.TrimRight(ollamaBaseURL, "/"),
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

// Provider returns the adapter for a provider name (aliases accepted).
func (r *Registry) Provider(name string) (Provider, error) {
	canonical := CanonicalProvider(name)
	key := func() string { return r.keyFor(canonical) }

	if base, ok := openAICompatEndpoints[canonical]; ok {
		if override := r.baseURLs[canonical]; override != "" {
			base = override
		}
		return newOpenAICompat(canonical, base, key, r.client), nil
	}

	switch canonical {
	case "anthropic":
		return newAnthropic(r.baseURLs["anthropic"], key, r.client), nil
	case "gemini":
		return newGemini(r.baseURLs["gemini"], key, r.client), nil
	case "ollama":
		base := r.ollamaURL
		if override := r.baseURLs["ollama"]; override != "" {
			base = override
		}
		return newOllama(base, r.client), nil
	}
	return nil, fmt.Errorf("provider %q is not supported", name)
}
