package ranking

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/krugis/route42app/internal/analyzer"
	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/config"
)

// miniCatalog is a fixed 20-model (9-model here) fixture used to assert
// the (mode x complexity) selection matrix deterministically. Cloud
// models need a configured key; local models are free and need no key.
func miniCatalog() *catalog.Catalog {
	cloud := []catalog.ModelInfo{
		{ID: "cheapcloud", Provider: "openai", Source: catalog.SourceCloud, QualityScore: 30, OutputTokensPerSecond: 200, TimeToFirstTokenMs: 500, InputPricePerMTok: 0.15, OutputPricePerMTok: 0.60, SupportsTools: true},
		{ID: "midcloud", Provider: "openai", Source: catalog.SourceCloud, QualityScore: 60, OutputTokensPerSecond: 100, TimeToFirstTokenMs: 800, InputPricePerMTok: 1.00, OutputPricePerMTok: 3.00, SupportsTools: true},
		{ID: "topcloud", Provider: "anthropic", Source: catalog.SourceCloud, QualityScore: 96, OutputTokensPerSecond: 60, TimeToFirstTokenMs: 1200, InputPricePerMTok: 5.00, OutputPricePerMTok: 15.00, SupportsTools: true},
		{ID: "fastcloud", Provider: "groq", Source: catalog.SourceCloud, QualityScore: 50, OutputTokensPerSecond: 800, TimeToFirstTokenMs: 100, InputPricePerMTok: 0.10, OutputPricePerMTok: 0.40, SupportsTools: true},
		{ID: "notoolcloud", Provider: "openai", Source: catalog.SourceCloud, QualityScore: 70, OutputTokensPerSecond: 150, TimeToFirstTokenMs: 400, InputPricePerMTok: 1.00, OutputPricePerMTok: 3.00, SupportsTools: false},
		{ID: "slowcloud", Provider: "mistral", Source: catalog.SourceCloud, QualityScore: 80, OutputTokensPerSecond: 20, TimeToFirstTokenMs: 3000, InputPricePerMTok: 0.20, OutputPricePerMTok: 0.60, SupportsTools: true},
	}
	local := []catalog.ModelInfo{
		{ID: "localfast", Provider: "ollama", Source: catalog.SourceLocal, QualityScore: 40, OutputTokensPerSecond: 60, TimeToFirstTokenMs: 50, InputPricePerMTok: 0, OutputPricePerMTok: 0, SupportsTools: true},
		{ID: "localsmall", Provider: "ollama", Source: catalog.SourceLocal, QualityScore: 20, OutputTokensPerSecond: 100, TimeToFirstTokenMs: 30, InputPricePerMTok: 0, OutputPricePerMTok: 0, SupportsTools: false},
		{ID: "localbig", Provider: "ollama", Source: catalog.SourceLocal, QualityScore: 75, OutputTokensPerSecond: 30, TimeToFirstTokenMs: 200, InputPricePerMTok: 0, OutputPricePerMTok: 0, SupportsTools: true},
	}
	return &catalog.Catalog{SchemaVersion: 1, SnapshotDate: "2026-07-04", Models: append(append([]catalog.ModelInfo{}, cloud...), local...)}
}

func allCloudAvailable() []string {
	return []string{"openai", "anthropic", "groq", "mistral"}
}

func allLocals() []catalog.ModelInfo {
	c := miniCatalog()
	var out []catalog.ModelInfo
	for _, m := range c.Models {
		if m.Source == catalog.SourceLocal {
			out = append(out, m)
		}
	}
	return out
}

func baseReq() RankRequest {
	return RankRequest{
		Analysis:     analyzer.AnalysisResult{Complexity: 0.1, Category: analyzer.CategoryChat, Analyzer: analyzer.NameHeuristic},
		Prefs:        config.Prefs{Priority: config.ModeHeuristic, FallbackDepth: 2},
		Available:    allCloudAvailable(),
		LocalModels:  allLocals(),
		PromptTokens: 1000,
	}
}

// selectedID is a test helper returning the selected model id, fataling if none.
func selectedID(t *testing.T, r *RankResult) string {
	t.Helper()
	if r == nil || r.Selected == nil {
		t.Fatalf("expected a selected model, got none")
	}
	return r.Selected.Model.ID
}

func TestRankCheapSimplePicksFreeLocal(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.Priority = "cheap"
	req.Analysis.Complexity = 0.1
	req.Analysis.Category = analyzer.CategoryChat

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	id := selectedID(t, r)
	if r.Selected.Model.Source != catalog.SourceLocal || !r.Selected.Model.IsFree() {
		t.Fatalf("cheap+simple should pick a free local model, got %s (free=%v source=%s)",
			id, r.Selected.Model.IsFree(), r.Selected.Model.Source)
	}
	if id != "localbig" {
		t.Fatalf("cheap+simple expected localbig, got %s", id)
	}
}

func TestRankAccurateComplexPicksTopQuality(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.Priority = "accurate"
	req.Analysis.Complexity = 0.9
	req.Analysis.Category = analyzer.CategoryAnalysis

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	// floor 0.95 leaves only topcloud (quality 96).
	if id := selectedID(t, r); id != "topcloud" {
		t.Fatalf("accurate+complex expected topcloud, got %s", id)
	}
	if r.Policy.SoftReset {
		t.Fatalf("expected no soft reset (topcloud clears the floor)")
	}
}

func TestRankFastPicksLowestTTFT(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.Priority = "fast"
	req.Analysis.Complexity = 0.3
	req.Analysis.Category = analyzer.CategoryGeneral

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	if id := selectedID(t, r); id != "fastcloud" {
		t.Fatalf("fast expected fastcloud (lowest TTFT), got %s", id)
	}
}

func TestRankOnlyLocal(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.OnlyLocal = true
	req.Analysis.Complexity = 0.1

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	if r.Selected.Model.Source != catalog.SourceLocal {
		t.Fatalf("only_local expected a local model, got source=%s", r.Selected.Model.Source)
	}
	// every survivor must be local.
	for _, c := range r.Candidates {
		if c.Model.Source != catalog.SourceLocal {
			t.Fatalf("only_local leaked a cloud model: %s", c.Model.ID)
		}
	}
	// cloud models must be recorded as filtered (only_local_excluded_cloud).
	found := false
	for _, f := range r.Filtered {
		if f.Reason == reasonOnlyLocal {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected cloud models filtered with %q", reasonOnlyLocal)
	}
}

func TestRankOnlyLocalNoLocalModelsErrors(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.OnlyLocal = true
	req.LocalModels = nil // no local models available

	r, err := e.Rank(req)
	if err != ErrNoCandidates {
		t.Fatalf("expected ErrNoCandidates, got %v", err)
	}
	if r.Selected != nil {
		t.Fatalf("expected nil selected on empty hard filter")
	}
}

func TestRankToolsFilter(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Tools = json.RawMessage(`[{"type":"function","function":{"name":"f"}}]`)
	req.Analysis.Complexity = 0.3

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	if !r.Policy.ToolRequired {
		t.Fatalf("expected ToolRequired=true")
	}
	// notoolcloud and localsmall must be filtered out.
	seen := map[string]bool{}
	for _, f := range r.Filtered {
		if f.Reason == reasonToolCapable {
			seen[f.Model.ID] = true
		}
	}
	if !seen["notoolcloud"] || !seen["localsmall"] {
		t.Fatalf("expected notoolcloud and localsmall filtered for tools, got %v", seen)
	}
	for _, c := range r.Candidates {
		if !c.Model.SupportsTools {
			t.Fatalf("tool-incompatible model survived: %s", c.Model.ID)
		}
	}
	if !r.Selected.Model.SupportsTools {
		t.Fatalf("selected model must support tools")
	}
}

func TestRankDisallowed(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.DisallowedModels = []string{"anthropic/topcloud", "fastcloud"}
	req.Analysis.Complexity = 0.3

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	filtered := map[string]string{}
	for _, f := range r.Filtered {
		if f.Reason == reasonDisallowed {
			filtered[f.Model.ID] = f.Reason
		}
	}
	if _, ok := filtered["topcloud"]; !ok {
		t.Fatalf("expected topcloud disallowed, filtered=%v", filtered)
	}
	if _, ok := filtered["fastcloud"]; !ok {
		t.Fatalf("expected fastcloud disallowed (bare id match), filtered=%v", filtered)
	}
	for _, c := range r.Candidates {
		if c.Model.ID == "topcloud" || c.Model.ID == "fastcloud" {
			t.Fatalf("disallowed model survived: %s", c.Model.ID)
		}
	}
}

func TestRankMaxCostSoftFilter(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Analysis.Complexity = 0.3
	// topcloud est cost (1000 prompt + 1024 resp) ≈ 2.04 cents; cap at 1.0c.
	req.Prefs.MaxCostCents = 1.0
	req.MaxResponseTokens = 1024

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	for _, f := range r.Filtered {
		if f.Model.ID == "topcloud" {
			if !strings.Contains(f.Reason, reasonMaxCost) {
				t.Fatalf("topcloud should be filtered for max_cost, got %q", f.Reason)
			}
			// a cheaper model must have survived (no reset).
			if r.Policy.SoftReset {
				t.Fatalf("did not expect a soft reset (cheaper models survive)")
			}
			return
		}
	}
	t.Fatalf("expected topcloud filtered by max_cost")
}

func TestRankSoftResetOnEmpty(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.OnlyFree = true
	req.LocalModels = nil // no free locals → only_free empties the cloud-only pool
	req.Analysis.Complexity = 0.1

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: unexpected error: %v", err)
	}
	if !r.Policy.SoftReset {
		t.Fatalf("expected SoftReset=true when only_free empties the pool")
	}
	if r.Selected == nil {
		t.Fatalf("expected a selected model after reset")
	}
	if !containsString(r.Policy.SoftFilters, "only_free") {
		t.Fatalf("expected only_free in soft filters: %v", r.Policy.SoftFilters)
	}
}

func TestRankNoProvidersNoLocalsErrors(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Available = nil
	req.LocalModels = nil

	r, err := e.Rank(req)
	if err != ErrNoCandidates {
		t.Fatalf("expected ErrNoCandidates, got %v", err)
	}
	if r.Selected != nil {
		t.Fatalf("expected nil selected")
	}
}

func TestRankDeterminismByteIdenticalExplain(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Prefs.Priority = "balanced"
	req.Analysis.Complexity = 0.55
	req.Analysis.Category = analyzer.CategoryCode

	r1, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank #1: %v", err)
	}
	r2, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank #2: %v", err)
	}
	a, b := r1.Explain(), r2.Explain()
	if a != b {
		t.Fatalf("non-deterministic explain output:\n--- run 1 ---\n%s\n--- run 2 ---\n%s", a, b)
	}
	// full structural equality of the ranked list (ids + composites).
	if len(r1.Candidates) != len(r2.Candidates) {
		t.Fatalf("candidate count differs across runs")
	}
	for i := range r1.Candidates {
		if r1.Candidates[i].Model.ID != r2.Candidates[i].Model.ID {
			t.Fatalf("rank order differs at %d: %s vs %s", i, r1.Candidates[i].Model.ID, r2.Candidates[i].Model.ID)
		}
		if r1.Candidates[i].Composite != r2.Candidates[i].Composite {
			t.Fatalf("composite differs for %s", r1.Candidates[i].Model.ID)
		}
	}
}

func TestRankExplainContainsBreakdown(t *testing.T) {
	e := New(miniCatalog())
	req := baseReq()
	req.Analysis.Complexity = 0.5

	r, err := e.Rank(req)
	if err != nil {
		t.Fatalf("Rank: %v", err)
	}
	ex := r.Explain()
	for _, want := range []string{"preference=", "weights:", "selected:", "composite=", "ranking:"} {
		if !strings.Contains(ex, want) {
			t.Fatalf("explain missing %q:\n%s", want, ex)
		}
	}
}

// --- pure scoring function tests ---

func TestWeightsForComplexityAdjustment(t *testing.T) {
	cases := []struct {
		pref string
		c    float64
		want Weights
	}{
		{"balanced", 0.1, Weights{0.15, 0.15, 0.70}}, // very simple → cost
		{"balanced", 0.3, Weights{0.25, 0.25, 0.50}}, // somewhat simple
		{"balanced", 0.8, Weights{0.70, 0.15, 0.15}}, // complex → quality
		{"cheap", 0.1, Weights{0.05, 0.05, 0.90}},    // trivial → extreme cost
		{"cheap", 0.6, Weights{0.30, 0.20, 0.50}},    // complex cheap stays base
		{"fast", 0.8, Weights{0.60, 0.30, 0.10}},     // complex fast → quality bump
		{"accurate", 0.8, Weights{0.80, 0.10, 0.10}}, // complex accurate
	}
	for _, tc := range cases {
		got := weightsFor(tc.pref, tc.c)
		if got != tc.want {
			t.Errorf("weightsFor(%s, %.2f) = %+v, want %+v", tc.pref, tc.c, got, tc.want)
		}
		// weights must sum to 1.
		sum := got.Quality + got.Speed + got.Cost
		if math.Abs(sum-1.0) > 1e-9 {
			t.Errorf("weightsFor(%s, %.2f) sum=%.6f != 1", tc.pref, tc.c, sum)
		}
	}
}

func TestMinQualityForComplexity(t *testing.T) {
	cases := []struct {
		c    float64
		want float64
	}{
		{0.0, 0.30}, {0.24, 0.30}, {0.25, 0.50}, {0.49, 0.50},
		{0.50, 0.70}, {0.74, 0.70}, {0.75, 0.85}, {0.89, 0.85},
		{0.90, 0.95}, {1.0, 0.95},
	}
	for _, tc := range cases {
		if got := minQualityForComplexity(tc.c); got != tc.want {
			t.Errorf("minQualityForComplexity(%.2f) = %.2f, want %.2f", tc.c, got, tc.want)
		}
	}
}

func TestCombinedSpeedScore(t *testing.T) {
	cases := []struct {
		tps, ttft float64
		want      float64
	}{
		{0, 0, 0},          // no data
		{0, 0.1, 1000},     // ttft-only, capped
		{500, 0.5, 400},    // 1000/(0.5+2)
		{600, 0.2, 535.71}, // 1000/(0.2+1.6667)
		{100, 5, 66.66},    // slow
	}
	for _, tc := range cases {
		got := combinedSpeedScore(tc.tps, tc.ttft)
		if math.Abs(got-tc.want) > 0.1 {
			t.Errorf("combinedSpeedScore(%g, %g) = %g, want ~%g", tc.tps, tc.ttft, got, tc.want)
		}
		if got < 0 || got > 1000 {
			t.Errorf("combinedSpeedScore out of [0,1000]: %g", got)
		}
	}
}

func TestSpeedScoreLocalNoDataIsFast(t *testing.T) {
	m := catalog.ModelInfo{ID: "x", Source: catalog.SourceLocal} // no metrics
	if got := speedScore(m); got != 1.0 {
		t.Fatalf("local model with no data should be fast (1.0), got %g", got)
	}
	m2 := catalog.ModelInfo{ID: "y", Source: catalog.SourceCloud} // no metrics
	if got := speedScore(m2); got != 0 {
		t.Fatalf("cloud model with no data should score 0, got %g", got)
	}
}

func TestCostScoresLogScaleP95(t *testing.T) {
	// Free model + a spread of paid prices.
	models := []catalog.ModelInfo{
		{ID: "free", Provider: "ollama", Source: catalog.SourceLocal},                      // IsFree
		{ID: "cheap", Provider: "openai", InputPricePerMTok: 0.1, OutputPricePerMTok: 0.4}, // blended 0.175
		{ID: "mid", Provider: "openai", InputPricePerMTok: 1, OutputPricePerMTok: 3},       // blended 1.5
		{ID: "pricey", Provider: "openai", InputPricePerMTok: 10, OutputPricePerMTok: 30},  // blended 15
	}
	scores := costScores(models)
	if scores[0] != 1.0 {
		t.Errorf("free model cost score = %g, want 1.0", scores[0])
	}
	// cheaper should score higher than mid and pricey.
	if !(scores[1] > scores[2] && scores[2] > scores[3]) {
		t.Errorf("expected cheap>mid>pricey, got %v", scores)
	}
	if scores[1] < 0.9 {
		t.Errorf("cheapest paid model should score near 1, got %g", scores[1])
	}
}

func TestHasTools(t *testing.T) {
	cases := []struct {
		raw  string
		want bool
	}{
		{``, false},
		{`null`, false},
		{`[]`, false},
		{`   `, false},
		{`[{"type":"function"}]`, true},
	}
	for _, tc := range cases {
		if got := hasTools(json.RawMessage(tc.raw)); got != tc.want {
			t.Errorf("hasTools(%q) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestIsDisallowed(t *testing.T) {
	m := catalog.ModelInfo{ID: "gpt-4o", Provider: "OpenAI"}
	if !isDisallowed(m, []string{"openai/gpt-4o"}) {
		t.Error("provider/id match should disallow")
	}
	if !isDisallowed(m, []string{"gpt-4o"}) {
		t.Error("bare id match should disallow")
	}
	if !isDisallowed(m, []string{"OPENAI/GPT-4O"}) {
		t.Error("disallowed match should be case-insensitive")
	}
	if isDisallowed(m, []string{"anthropic/gpt-4o"}) {
		t.Error("different provider should not disallow")
	}
	if isDisallowed(m, []string{"other-model"}) {
		t.Error("unrelated id should not disallow")
	}
}

func TestBuildCandidatesSkipsLocalInCatalog(t *testing.T) {
	// A catalog local entry must be skipped; discovery (LocalModels) is
	// the source of truth for what is running.
	c := &catalog.Catalog{SchemaVersion: 1, Models: []catalog.ModelInfo{
		{ID: "catalog-local", Provider: "ollama", Source: catalog.SourceLocal},
		{ID: "cloud", Provider: "openai", Source: catalog.SourceCloud},
	}}
	e := New(c)
	got := e.buildCandidates(RankRequest{Available: []string{"openai"}, LocalModels: []catalog.ModelInfo{
		{ID: "discovered", Provider: "ollama", Source: catalog.SourceLocal},
	}})
	ids := map[string]bool{}
	for _, m := range got {
		ids[m.ID] = true
	}
	if ids["catalog-local"] {
		t.Errorf("catalog local entry should be skipped")
	}
	if !ids["cloud"] {
		t.Errorf("cloud model for available provider should be included")
	}
	if !ids["discovered"] {
		t.Errorf("discovered local model should be included")
	}
}

func TestNormalizePreferenceAliases(t *testing.T) {
	cases := map[string]string{
		"balanced":   prefBalanced,
		"speed":      prefFast,
		"quality":    prefAccurate,
		"FAST":       prefFast,
		"":           prefBalanced,
		"local_only": prefLocalOnly,
		"weird":      prefBalanced,
	}
	_ = prefLocalOnly // keep the const referenced even if the alias case changes
	for in, want := range cases {
		if got := normalizePreference(in); got != want {
			t.Errorf("normalizePreference(%q) = %q, want %q", in, got, want)
		}
	}
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// guard against accidental drift in the RankRequest/Policy shape breaking
// the golden assertions above (compile-time + a smoke check).
func TestPolicyShape(t *testing.T) {
	p := Policy{Preference: "balanced"}
	if reflect.DeepEqual(p, Policy{}) {
		t.Fatal("Policy zero value should not equal a populated one")
	}
}
