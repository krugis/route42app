package ranking

import (
	"encoding/json"

	"github.com/krugis/route42app/internal/analyzer"
	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/config"
)

// RankRequest is the input to a routing decision. Everything the engine
// needs to filter and score candidates is carried here so that Rank is a
// pure function of its inputs (deterministic, side-effect free).
type RankRequest struct {
	// Analysis is the prompt analyzer output (complexity + category).
	Analysis analyzer.AnalysisResult

	// Prefs are the user's routing preferences (priority mode, constraints).
	Prefs config.Prefs

	// Available is the canonical names of cloud providers with a configured
	// API key. Catalog models whose provider is not in this set (and is not
	// local) are excluded — a model cannot be routed to without credentials.
	Available []string

	// LocalModels are models discovered at runtime (Ollama). They are
	// candidates regardless of the Available set because local inference
	// needs no key. The caller is responsible for any metric enrichment
	// (e.g. matching against the catalog for quality/speed data).
	LocalModels []catalog.ModelInfo

	// Tools is the OpenAI-format tool definition array from the request.
	// A non-empty value enables the tool-capable hard filter: only models
	// with SupportsTools true may serve the request.
	Tools json.RawMessage

	// PromptTokens is the estimated input token count, used for the
	// per-request cost estimate that drives the max-cost filter and the
	// response's est_cost_cents field. Zero is treated as unknown (the
	// cost estimate then reflects only the response budget).
	PromptTokens int

	// MaxResponseTokens overrides Prefs.MaxResponseTokens for this request
	// when positive (e.g. the caller passed max_tokens). Zero falls back to
	// Prefs.MaxResponseTokens, then a built-in default.
	MaxResponseTokens int
}

// Weights is the composite-score weight distribution across the three
// scoring factors. They always sum to 1.0.
type Weights struct {
	Quality float64
	Speed   float64
	Cost    float64
}

// Breakdown is the per-factor contribution to a candidate's Composite
// score, so every routing decision is explainable: Composite == Quality +
// Speed + Cost (within floating-point rounding).
type Breakdown struct {
	Quality float64 // QualityScore * Weights.Quality
	Speed   float64 // SpeedScore   * Weights.Speed
	Cost    float64 // CostScore    * Weights.Cost
}

// RankedCandidate is one surviving candidate with its factor scores and
// the composite ranking score.
type RankedCandidate struct {
	Model        catalog.ModelInfo
	QualityScore float64 // 0..1, derived from catalog quality
	SpeedScore   float64 // 0..1, 1 = fastest
	CostScore    float64 // 0..1, 1 = free / cheapest
	Composite    float64 // 0..1 weighted sum
	Breakdown    Breakdown

	// BlendedPricePerMTok is the 3:1 input:output blended price in USD per
	// million tokens (0 for free models). Used for the "cheap" preference
	// winner selection and surfaced in the explanation.
	BlendedPricePerMTok float64

	// EstCostCents is the estimated request cost in US cents for the given
	// prompt + response token budget. 0 for free models.
	EstCostCents float64
}

// FilteredCandidate records a model removed by a filter, with the reason.
type FilteredCandidate struct {
	Model  catalog.ModelInfo
	Reason string
}

// Policy records what the engine did: which filters and weights were
// applied, so the "why this model" answer is fully inspectable.
type Policy struct {
	Preference      string   // effective preference mode
	Complexity      float64  // analyzer complexity
	Category        string   // analyzer category
	QualityFloor    float64  // 0..1, 0 means no floor applied
	Weights         Weights  // composite weights (post complexity adjustment)
	SelectionWindow int      // top-N window for dominant-factor winner pick
	ToolRequired    bool     // tools were present in the request
	HardFilters     []string // hard filter names applied (cannot reset)
	SoftFilters     []string // soft filter names applied (reset on empty)
	SoftReset       bool     // true if soft filters were reset to avoid an empty result
}

// RankResult is the output of Rank. Candidates is sorted best-first (the
// selected model at index 0); Filtered lists everything removed by a
// filter with the reason; Policy describes the decision.
type RankResult struct {
	Candidates []RankedCandidate   // sorted best-first; winner first
	Selected   *RankedCandidate    // nil only when nothing survived hard filters
	Filtered   []FilteredCandidate // removed by filters, in application order
	Policy     Policy
}
