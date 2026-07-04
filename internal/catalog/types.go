package catalog

// Source distinguishes where a model runs.
type Source string

const (
	SourceCloud Source = "cloud"
	SourceLocal Source = "local"
)

// Catalog is the schema of data/catalog.json: a versioned snapshot of
// model metadata normalized across providers.
type Catalog struct {
	// SchemaVersion increments on breaking schema changes.
	SchemaVersion int `json:"schema_version"`
	// SnapshotDate is the day the metrics were captured (YYYY-MM-DD).
	SnapshotDate string `json:"snapshot_date"`
	// Attribution credits the upstream data sources.
	Attribution string      `json:"attribution,omitempty"`
	Models      []ModelInfo `json:"models"`
}

// ModelInfo describes one routable model with the metrics the ranking
// engine scores on. Metrics are normalized across providers; zero values
// mean "unknown" and are treated conservatively by the ranker.
type ModelInfo struct {
	ID          string `json:"id"`       // provider-scoped model id, e.g. "gpt-4o-mini"
	Provider    string `json:"provider"` // e.g. "openai", "anthropic", "ollama"
	DisplayName string `json:"display_name,omitempty"`
	Source      Source `json:"source"`

	// Quality is a normalized capability score in [0,100].
	QualityScore float64 `json:"quality_score"`

	// Speed metrics (medians from public benchmarks; local models are
	// measured at discovery time or left unknown).
	OutputTokensPerSecond float64 `json:"output_tokens_per_second,omitempty"`
	TimeToFirstTokenMs    float64 `json:"time_to_first_token_ms,omitempty"`

	// Pricing in USD per million tokens. Local models are 0/0.
	InputPricePerMTok  float64 `json:"input_price_per_mtok"`
	OutputPricePerMTok float64 `json:"output_price_per_mtok"`

	// ContextWindow in tokens.
	ContextWindow int `json:"context_window,omitempty"`

	// Capability flags.
	SupportsTools  bool `json:"supports_tools,omitempty"`
	SupportsVision bool `json:"supports_vision,omitempty"`
}

// IsFree reports whether using the model costs nothing (local models and
// zero-priced cloud tiers).
func (m ModelInfo) IsFree() bool {
	return m.InputPricePerMTok == 0 && m.OutputPricePerMTok == 0
}
