package api

import (
	"encoding/json"
	"time"

	"github.com/krugis/route42app/internal/analyzer"
)

// This file holds the OpenAI-compatible wire types used by the gateway.
// Field names use the OpenAI snake_case JSON tags so any OpenAI SDK works
// against the gateway unchanged. Route42 extensions live in a separate
// XRoute42 object that OpenAI clients simply ignore.

// ChatRequest is the OpenAI /chat/completions request. Fields Route42 does
// not use are still accepted (and ignored) for client compatibility.
type ChatRequest struct {
	Model       string          `json:"model,omitempty"`
	Messages    []ChatMessage   `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	MaxTokens   *int            `json:"max_tokens,omitempty"`
	Tools       json.RawMessage `json:"tools,omitempty"`
	// TopP, FrequencyPenalty, etc. are accepted but not surfaced to the
	// provider layer in CE (kept here only to avoid strict-decode errors in
	// lenient clients that send them — the decoder is non-strict).
}

// ChatMessage is one message in the OpenAI wire format.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCalls and ToolCallID are accepted for compatibility but CE does
	// not synthesize tool-call round trips; they pass through to providers.
	ToolCalls  json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
}

// ChatResponse is the non-streaming OpenAI /chat/completions response.
type ChatResponse struct {
	ID                string       `json:"id"`
	Object            string       `json:"object"` // "chat.completion"
	Created           int64        `json:"created"`
	Model             string       `json:"model"`
	SystemFingerprint string       `json:"system_fingerprint,omitempty"`
	Choices           []ChatChoice `json:"choices"`
	Usage             *ChatUsage   `json:"usage,omitempty"`
	XRoute42          *XRoute42    `json:"x_route42,omitempty"`
}

// ChatChoice is one completion choice in the non-streaming response.
type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// ChatUsage reports token consumption in the OpenAI format.
type ChatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// StreamChunk is one OpenAI SSE chunk for streaming completions.
type StreamChunk struct {
	ID       string         `json:"id"`
	Object   string         `json:"object"` // "chat.completion.chunk"
	Created  int64          `json:"created"`
	Model    string         `json:"model"`
	Choices  []StreamChoice `json:"choices"`
	XRoute42 *XRoute42      `json:"x_route42,omitempty"` // only on the final metadata chunk
}

// StreamChoice is one choice in a streaming chunk.
type StreamChoice struct {
	Index        int       `json:"index"`
	Delta        ChatDelta `json:"delta"`
	FinishReason *string   `json:"finish_reason"` // null until the final content chunk
}

// ChatDelta is the incremental content in a streaming chunk.
type ChatDelta struct {
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls json.RawMessage `json:"tool_calls,omitempty"`
}

// XRoute42 is the non-breaking Route42 extension surfaced on every
// response and the final SSE metadata chunk, explaining the routing
// decision.
type XRoute42 struct {
	SelectedModel        string             `json:"selected_model"`
	Provider             string             `json:"provider"`
	Analyzer             string             `json:"analyzer"`
	Complexity           float64            `json:"complexity"`
	Category             string             `json:"category"`
	Signals              map[string]float64 `json:"signals,omitempty"`
	CandidatesConsidered int                `json:"candidates_considered"`
	EstCostCents         float64            `json:"est_cost_cents"`
	FallbackAttempts     int                `json:"fallback_attempts,omitempty"`
	Reason               string             `json:"reason,omitempty"` // "routed" | "pinned"
}

// ModelsResponse is the OpenAI /v1/models list response, augmented with
// local discovery and availability in XRoute42 per model.
type ModelsResponse struct {
	Object string      `json:"object"` // "list"
	Data   []ModelInfo `json:"data"`
}

// ModelInfo is one entry in the models list. The OpenAI fields (id,
// object, created, owned_by) are populated; Route42 adds pricing, source,
// quality, and capability metadata under x_route42.
type ModelInfo struct {
	ID       string    `json:"id"`
	Object   string    `json:"object"` // "model"
	Created  int64     `json:"created"`
	OwnedBy  string    `json:"owned_by"`
	XRoute42 ModelMeta `json:"x_route42"`
}

// ModelMeta is the Route42 extension on a models-list entry.
type ModelMeta struct {
	Provider           string  `json:"provider"`
	Source             string  `json:"source"` // "cloud" | "local"
	QualityScore       float64 `json:"quality_score,omitempty"`
	OutputTokensPerSec float64 `json:"output_tokens_per_second,omitempty"`
	TimeToFirstTokenMs float64 `json:"time_to_first_token_ms,omitempty"`
	InputPricePerMTok  float64 `json:"input_price_per_mtok,omitempty"`
	OutputPricePerMTok float64 `json:"output_price_per_mtok,omitempty"`
	SupportsTools      bool    `json:"supports_tools,omitempty"`
	Available          bool    `json:"available"` // provider has a key (cloud) or is discovered (local)
}

// RecommendRequest is the body of POST /api/recommend: a prompt to route
// without executing.
type RecommendRequest struct {
	Model    string          `json:"model,omitempty"`
	Messages []ChatMessage   `json:"messages"`
	Tools    json.RawMessage `json:"tools,omitempty"`
}

// RecommendResponse is the ranked-candidate list with an explanation.
type RecommendResponse struct {
	XRoute42    XRoute42             `json:"x_route42"`
	Candidates  []RecommendCandidate `json:"candidates"`
	Explanation string               `json:"explanation"`
}

// RecommendCandidate is one ranked candidate in a recommend response.
type RecommendCandidate struct {
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Source       string  `json:"source"`
	Composite    float64 `json:"composite"`
	Quality      float64 `json:"quality"`
	Speed        float64 `json:"speed"`
	Cost         float64 `json:"cost"`
	EstCostCents float64 `json:"est_cost_cents"`
}

// ErrorJSON is the OpenAI-style error envelope.
type ErrorJSON struct {
	Error ErrorBody `json:"error"`
}

// ErrorBody is the body of the OpenAI error envelope.
type ErrorBody struct {
	Message string `json:"message"`
	Type    string `json:"type"` // "invalid_request_error" | "server_error" | ...
	Code    string `json:"code,omitempty"`
}

// toAnalyzerMessages converts wire ChatMessages to analyzer.Message,
// dropping tool-call-only fields (the analyzer works on text).
func toAnalyzerMessages(msgs []ChatMessage) []analyzer.Message {
	out := make([]analyzer.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, analyzer.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// estimatePromptTokens approximates the input token count as chars/4.
// Used for the cost estimate and the interaction log when the provider
// does not report prompt usage (streaming).
func estimatePromptTokens(msgs []ChatMessage) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
	}
	return n / 4
}

// nowUnix returns the current Unix timestamp for the Created field.
func nowUnix() int64 { return time.Now().Unix() }
