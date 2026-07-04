package analyzer

import "context"

// Message is a single chat message, matching the OpenAI wire format.
type Message struct {
	Role    string `json:"role"` // system | user | assistant | tool
	Content string `json:"content"`
}

// Prompt categories. General is the fallback when no category signal
// clears the detection threshold.
const (
	CategoryChat     = "chat"
	CategoryCode     = "code"
	CategoryMath     = "math"
	CategoryAnalysis = "analysis"
	CategoryGeneral  = "general"
)

// Analyzer names reported in AnalysisResult.Analyzer.
const (
	NameHeuristic = "heuristic"
	NameLLM       = "llm"
)

// AnalysisResult is the outcome of prompt analysis that drives routing.
type AnalysisResult struct {
	// Complexity estimates task difficulty in [0,1]: 0 is a trivial
	// one-liner, 1 a multi-constraint expert task.
	Complexity float64 `json:"complexity"`
	// Category is one of the Category* constants.
	Category string `json:"category"`
	// Signals holds each signal's post-weight contribution, keyed by
	// signal name, so every routing decision is explainable.
	Signals map[string]float64 `json:"signals,omitempty"`
	// Analyzer identifies which implementation produced this result
	// (a configured analyzer may fall back to another at runtime).
	Analyzer string `json:"analyzer"`
}

// PromptAnalyzer scores a conversation before model selection. The last
// user message carries the most weight; prior context may refine the result.
//
// Implementations must be safe for concurrent use and should degrade rather
// than fail: routing must never be blocked by analysis errors.
type PromptAnalyzer interface {
	Analyze(ctx context.Context, messages []Message) (AnalysisResult, error)
}
