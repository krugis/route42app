package analyzer

import "context"

// Classifier is the interface for classifying prompts. LLMAnalyzer implements
// this via its Classify method.
type Classifier interface {
	Classify(ctx context.Context, prompt string) (AnalysisResult, error)
}

// HybridAnalyzer blends the deterministic HeuristicAnalyzer with an LLM
// analyzer's judgment: heuristic runs unconditionally (cheap, never fails),
// and LLM output — when available — refines complexity via a weighted
// blend and overrides category only when the heuristic had low confidence
// (fell back to CategoryGeneral). On any LLM failure, the heuristic result
// is returned unchanged, so hybrid mode is a strict superset of heuristic
// mode's reliability guarantees.
type HybridAnalyzer struct {
	heuristic *HeuristicAnalyzer
	llm       Classifier
	weight    float64 // 0..1, weight given to the LLM's complexity score
}

// NewHybrid returns a hybrid analyzer that blends heuristic signals with
// LLM judgment. weight should be in [0,1]; weights <= 0 default to 0.5.
func NewHybrid(h *HeuristicAnalyzer, c Classifier, weight float64) *HybridAnalyzer {
	if weight <= 0 {
		weight = 0.5
	}
	return &HybridAnalyzer{heuristic: h, llm: c, weight: weight}
}

// Analyze implements PromptAnalyzer. It always runs the heuristic analyzer
// (cheap, never fails) and attempts to refine it with the LLM analyzer's
// judgment. On any LLM error, the heuristic result is returned unchanged.
func (a *HybridAnalyzer) Analyze(ctx context.Context, messages []Message) (AnalysisResult, error) {
	base, _ := a.heuristic.Analyze(ctx, messages) // heuristic never errors

	prompt := lastUserMessage(messages)
	llmRes, err := a.llm.Classify(ctx, prompt)
	if err != nil {
		// LLM failed; return heuristic result but tag as hybrid for observability.
		base.Analyzer = NameHybrid
		return base, nil
	}

	// Blend complexity: weight given to LLM, (1-weight) to heuristic.
	base.Complexity = clamp01(a.weight*llmRes.Complexity + (1-a.weight)*base.Complexity)

	// Override category only if heuristic had low confidence (fell back to general).
	if base.Category == CategoryGeneral && llmRes.Category != "" {
		base.Category = llmRes.Category
	}

	// Record the raw LLM complexity for auditability.
	base.Signals["llm.complexity"] = llmRes.Complexity
	base.Analyzer = NameHybrid
	return base, nil
}
