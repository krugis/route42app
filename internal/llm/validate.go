package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ValidationResult reports whether a provider API key works.
type ValidationResult struct {
	IsValid      bool   `json:"is_valid"`
	StatusCode   int    `json:"status_code"`
	ErrorMessage string `json:"error_message,omitempty"`
}

const validateTimeout = 10 * time.Second

// validationPaths maps canonical provider names to a cheap authenticated
// GET path (relative to the provider's API base) used to verify a key.
var validationPaths = map[string]string{
	"openai":     "/models",
	"groq":       "/models",
	"mistral":    "/models",
	"deepseek":   "/models",
	"nvidia":     "/models",
	"moonshot":   "/models",
	"alibaba":    "/models",
	"zai":        "/models",
	"openrouter": "/auth/key",
	"anthropic":  "/models",
	"gemini":     "/models",
}

// validationBase returns the effective API base for a canonical provider,
// honoring configured overrides (which also enable offline tests).
func (r *Registry) validationBase(canonical string) string {
	if override := r.baseURLs[canonical]; override != "" {
		return override
	}
	if base, ok := openAICompatEndpoints[canonical]; ok {
		return base
	}
	switch canonical {
	case "anthropic":
		return anthropicDefaultBaseURL
	case "gemini":
		return geminiDefaultBaseURL
	}
	return ""
}

// ValidateKey checks a provider API key with a lightweight authenticated
// request. Aliases are accepted. Unknown providers return an error.
func (r *Registry) ValidateKey(ctx context.Context, provider, apiKey string) (ValidationResult, error) {
	canonical := CanonicalProvider(provider)
	path, ok := validationPaths[canonical]
	if !ok {
		return ValidationResult{IsValid: false, ErrorMessage: "unknown provider"},
			fmt.Errorf("provider %q is not supported", provider)
	}

	ctx, cancel := context.WithTimeout(ctx, validateTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.validationBase(canonical)+path, nil)
	if err != nil {
		return ValidationResult{IsValid: false, ErrorMessage: err.Error()}, err
	}
	switch canonical {
	case "anthropic":
		req.Header.Set("x-api-key", apiKey)
		req.Header.Set("anthropic-version", anthropicVersion)
	case "gemini":
		req.Header.Set("X-goog-api-key", apiKey)
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := r.client.Do(req)
	if err != nil {
		// Network failure: the key is unverified, not invalid; report
		// the reason without failing the call.
		return ValidationResult{IsValid: false, ErrorMessage: err.Error()}, nil
	}
	defer resp.Body.Close()

	result := ValidationResult{
		IsValid:    resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated,
		StatusCode: resp.StatusCode,
	}
	if !result.IsValid {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		result.ErrorMessage = string(body)
	}
	return result, nil
}
