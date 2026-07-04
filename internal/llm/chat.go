package llm

import (
	"encoding/json"
	"fmt"
)

// Message is a single chat message in OpenAI wire format.
type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`
}

// ChatRequest is a provider-neutral chat completion request.
type ChatRequest struct {
	// Model is the provider-scoped model id (no "provider/" prefix).
	Model    string
	Messages []Message
	// MaxTokens caps the response length; 0 means provider default.
	MaxTokens int
	// Temperature; 0 means provider default.
	Temperature float64
	// Tools is the OpenAI-format tool definition array, passed through
	// verbatim to providers that support it.
	Tools json.RawMessage
}

// Usage reports token consumption when the provider supplies it.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// ChatResponse is a provider-neutral non-streaming completion result.
type ChatResponse struct {
	Text string
	// Reasoning carries thinking/reasoning content for models that emit it.
	Reasoning string
	// ToolCalls is the OpenAI-format tool_calls array, passed through
	// verbatim when the model chose to call tools.
	ToolCalls    json.RawMessage
	FinishReason string
	Usage        Usage
}

// Chunk is one streaming increment. The final chunk has Done set (and
// Usage when the provider reports it); a failed stream delivers a single
// chunk with Err set. The channel is closed after either.
type Chunk struct {
	Delta          string
	ReasoningDelta string
	Done           bool
	Usage          Usage
	Err            error
}

// APIError is a non-2xx provider response. Status code is preserved so
// callers can distinguish retryable failures (429, 5xx) from permanent
// ones (401, 404) during fallback.
type APIError struct {
	Provider   string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	body := e.Body
	if len(body) > 300 {
		body = body[:300] + "..."
	}
	return fmt.Sprintf("%s error %d: %s", e.Provider, e.StatusCode, body)
}

// Retryable reports whether a fallback to another model/provider is
// worth attempting.
func (e *APIError) Retryable() bool {
	return e.StatusCode == 429 || e.StatusCode >= 500
}
