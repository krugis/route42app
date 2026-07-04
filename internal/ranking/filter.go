package ranking

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/llm"
)

// Filter reason tags, surfaced in Policy and FilteredCandidate.Reason.
const (
	reasonNoKey        = "provider_without_key"
	reasonDisallowed   = "disallowed"
	reasonOnlyLocal    = "only_local_excluded_cloud"
	reasonToolCapable  = "tool_capable_required"
	reasonOnlyFree     = "only_free"
	reasonMaxCost      = "max_cost_exceeded"
	reasonLatency      = "latency_tolerance_exceeded"
	reasonQualityFloor = "quality_below_floor"
)

// buildCandidates assembles the candidate pool: cloud catalog models for
// providers with a configured key, plus the locally discovered models.
// Catalog local entries are skipped — discovery (LocalModels) is the
// source of truth for what is actually running. Duplicates by canonical
// "provider/id" are removed, keeping the first occurrence.
func (e *Engine) buildCandidates(req RankRequest) []catalog.ModelInfo {
	avail := make(map[string]bool, len(req.Available))
	for _, p := range req.Available {
		avail[llm.CanonicalProvider(p)] = true
	}

	out := make([]catalog.ModelInfo, 0, len(e.catalog.Models)+len(req.LocalModels))
	seen := make(map[string]bool, len(e.catalog.Models)+len(req.LocalModels))

	add := func(m catalog.ModelInfo) {
		key := candidateKey(m)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, m)
	}

	for _, m := range e.catalog.Models {
		if m.Source == catalog.SourceLocal {
			continue // local candidates come from discovery
		}
		if !avail[llm.CanonicalProvider(m.Provider)] {
			continue
		}
		add(m)
	}
	for _, m := range req.LocalModels {
		add(m)
	}
	return out
}

func candidateKey(m catalog.ModelInfo) string {
	return llm.CanonicalProvider(m.Provider) + "/" + strings.ToLower(strings.TrimSpace(m.ID))
}

// filterResult holds the survivors and the removed candidates after a
// filter pass.
type filterResult struct {
	surviving []catalog.ModelInfo
	removed   []FilteredCandidate
}

// applyHardFilter runs a single hard filter (cannot be reset on empty).
func applyHardFilter(in []catalog.ModelInfo, removed []FilteredCandidate, name string, keep func(catalog.ModelInfo) bool, reason func(catalog.ModelInfo) string) ([]catalog.ModelInfo, []FilteredCandidate) {
	out := make([]catalog.ModelInfo, 0, len(in))
	for _, m := range in {
		if keep(m) {
			out = append(out, m)
		} else {
			removed = append(removed, FilteredCandidate{Model: m, Reason: reason(m)})
		}
	}
	return out, removed
}

// applyHardFilters enforces functional, non-resettable constraints:
// disallowed models, only_local, and tool-capable (when tools present).
func applyHardFilters(candidates []catalog.ModelInfo, prefs config.Prefs, tools json.RawMessage) ([]catalog.ModelInfo, []FilteredCandidate, []string) {
	var removed []FilteredCandidate
	var names []string

	// Disallowed models.
	if len(prefs.DisallowedModels) > 0 {
		names = append(names, "disallowed")
		candidates, removed = applyHardFilter(candidates, removed, "disallowed",
			func(m catalog.ModelInfo) bool { return !isDisallowed(m, prefs.DisallowedModels) },
			func(m catalog.ModelInfo) string { return reasonDisallowed })
	}

	// only_local: drop every cloud model.
	if prefs.OnlyLocal {
		names = append(names, "only_local")
		candidates, removed = applyHardFilter(candidates, removed, "only_local",
			func(m catalog.ModelInfo) bool { return m.Source == catalog.SourceLocal },
			func(m catalog.ModelInfo) string { return reasonOnlyLocal })
	}

	// tool-capable: only models that support tools.
	if hasTools(tools) {
		names = append(names, "tool_capable")
		candidates, removed = applyHardFilter(candidates, removed, "tool_capable",
			func(m catalog.ModelInfo) bool { return m.SupportsTools },
			func(m catalog.ModelInfo) string { return reasonToolCapable })
	}

	return candidates, removed, names
}

// applySoftFilter runs a single soft filter (resettable on empty).
func applySoftFilter(in []catalog.ModelInfo, removed []FilteredCandidate, name string, keep func(catalog.ModelInfo) bool, reason func(catalog.ModelInfo) string) ([]catalog.ModelInfo, []FilteredCandidate) {
	out := make([]catalog.ModelInfo, 0, len(in))
	for _, m := range in {
		if keep(m) {
			out = append(out, m)
		} else {
			removed = append(removed, FilteredCandidate{Model: m, Reason: reason(m)})
		}
	}
	return out, removed
}

// applySoftFilters enforces preference constraints that are relaxed when
// they would empty the pool (FILTER_RESET semantics). It returns the
// survivors, the removed candidates, the applied filter names, and whether
// a reset occurred.
func applySoftFilters(candidates []catalog.ModelInfo, prefs config.Prefs, req RankRequest, floor float64) ([]catalog.ModelInfo, []FilteredCandidate, []string, bool) {
	var removed []FilteredCandidate
	var names []string
	budget := responseBudget(req)

	// only_free.
	if prefs.OnlyFree {
		names = append(names, "only_free")
		candidates, removed = applySoftFilter(candidates, removed, "only_free",
			func(m catalog.ModelInfo) bool { return m.IsFree() },
			func(catalog.ModelInfo) string { return reasonOnlyFree })
	}

	// max cost estimate.
	if prefs.MaxCostCents > 0 {
		names = append(names, "max_cost")
		candidates, removed = applySoftFilter(candidates, removed, "max_cost",
			func(m catalog.ModelInfo) bool { return estCostCents(m, req.PromptTokens, budget) <= prefs.MaxCostCents },
			func(m catalog.ModelInfo) string {
				return fmt.Sprintf("%s (%.4f cents > %.4f cap)", reasonMaxCost, estCostCents(m, req.PromptTokens, budget), prefs.MaxCostCents)
			})
	}

	// latency tolerance (TTFT). Unknown TTFT passes — we cannot filter on
	// missing data.
	if prefs.LatencyToleranceMs > 0 {
		names = append(names, "latency")
		candidates, removed = applySoftFilter(candidates, removed, "latency",
			func(m catalog.ModelInfo) bool {
				return m.TimeToFirstTokenMs == 0 || m.TimeToFirstTokenMs <= float64(prefs.LatencyToleranceMs)
			},
			func(m catalog.ModelInfo) string {
				return fmt.Sprintf("%s (%.0fms > %dms)", reasonLatency, m.TimeToFirstTokenMs, prefs.LatencyToleranceMs)
			})
	}

	// quality floor. Models with unknown quality (0) are exempt.
	if floor > 0 {
		names = append(names, "quality_floor")
		candidates, removed = applySoftFilter(candidates, removed, "quality_floor",
			func(m catalog.ModelInfo) bool { return m.QualityScore == 0 || m.QualityScore/100.0 >= floor },
			func(m catalog.ModelInfo) string {
				return fmt.Sprintf("%s (%.0f/100 < %.0f)", reasonQualityFloor, m.QualityScore, floor*100)
			})
	}

	// FILTER_RESET: if the soft filters emptied the pool, undo them and keep
	// the pre-soft-filter set. The caller is informed via SoftReset.
	if len(candidates) == 0 && len(removed) > 0 {
		reset := make([]catalog.ModelInfo, 0, len(removed))
		for _, r := range removed {
			reset = append(reset, r.Model)
		}
		return reset, nil, names, true
	}
	return candidates, removed, names, false
}

// hasTools reports whether the request carries a non-empty tools array.
func hasTools(tools json.RawMessage) bool {
	if len(tools) == 0 {
		return false
	}
	s := strings.TrimSpace(string(tools))
	return s != "" && s != "null" && s != "[]"
}

// isDisallowed reports whether a model matches a disallowed-models entry.
// Entries may be "provider/id" or bare "id" (matched against any provider).
func isDisallowed(m catalog.ModelInfo, disallowed []string) bool {
	for _, d := range disallowed {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		if i := strings.IndexByte(d, '/'); i >= 0 {
			prov := strings.TrimSpace(d[:i])
			id := strings.TrimSpace(d[i+1:])
			if id == "" {
				continue
			}
			if llm.CanonicalProvider(prov) == llm.CanonicalProvider(m.Provider) && strings.EqualFold(id, m.ID) {
				return true
			}
			continue
		}
		if strings.EqualFold(d, m.ID) {
			return true
		}
	}
	return false
}
