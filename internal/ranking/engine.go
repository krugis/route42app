package ranking

import (
	"errors"
	"sort"

	"github.com/krugis/route42app/internal/catalog"
)

// ErrNoCandidates is returned by Rank when hard filters (or an empty
// candidate pool) leave no routable model. Soft filters never cause this
// error — they are reset on empty instead.
var ErrNoCandidates = errors.New("ranking: no candidates survived hard filters")

// Engine filters and ranks candidate models for a routing decision. It is
// safe for concurrent use: Rank reads only the (immutable) catalog and
// builds fresh local slices per call.
type Engine struct {
	catalog *catalog.Catalog
}

// New creates an Engine over a catalog. The catalog is read but not
// modified.
func New(c *catalog.Catalog) *Engine {
	return &Engine{catalog: c}
}

// Rank runs the full pipeline: build candidates → hard filter → soft
// filter (with reset) → score → select. The result is deterministic given
// the inputs: same request always yields byte-identical candidates,
// scores, and selection.
//
// An error is returned only when no candidate survives the hard filters
// (e.g. only_local with no local models, or no providers configured). The
// returned *RankResult is non-nil even on error and carries the Filtered
// list for explainability.
func (e *Engine) Rank(req RankRequest) (*RankResult, error) {
	pref := preferenceOf(req.Prefs)
	complexity := req.Analysis.Complexity
	if complexity < 0 {
		complexity = 0
	} else if complexity > 1 {
		complexity = 1
	}

	candidates := e.buildCandidates(req)

	// Hard filters (functional, non-resettable).
	hard, hardRemoved, hardNames := applyHardFilters(candidates, req.Prefs, req.Tools)
	if len(hard) == 0 {
		return &RankResult{
			Filtered: hardRemoved,
			Policy: Policy{
				Preference:      pref,
				Complexity:      complexity,
				Category:        req.Analysis.Category,
				QualityFloor:    minQualityForComplexity(complexity),
				Weights:         weightsFor(pref, complexity),
				SelectionWindow: selectionWindowFor(pref),
				ToolRequired:    hasTools(req.Tools),
				HardFilters:     hardNames,
			},
		}, ErrNoCandidates
	}

	// Soft filters (preference, resettable).
	floor := minQualityForComplexity(complexity)
	eligible, softRemoved, softNames, softReset := applySoftFilters(hard, req.Prefs, req, floor)

	// Score the eligible pool. Cost scoring is pool-relative, so it must
	// run after filtering on the final survivor set.
	scored := scoreAll(eligible, req, pref, complexity)

	// Sort by composite (desc) with a deterministic tiebreak.
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Composite != scored[j].Composite {
			return scored[i].Composite > scored[j].Composite
		}
		ki, kj := candidateKey(scored[i].Model), candidateKey(scored[j].Model)
		if ki != kj {
			return ki < kj
		}
		return false
	})

	// Pick the winner. balanced takes the top composite; others pick the
	// dominant factor within a top-N window.
	window := selectionWindowFor(pref)
	idx := chooseWinner(scored, pref, window)
	if idx > 0 {
		w := scored[idx]
		copy(scored[1:idx+1], scored[0:idx])
		scored[0] = w
	}

	filtered := append([]FilteredCandidate{}, hardRemoved...)
	filtered = append(filtered, softRemoved...)

	var selected *RankedCandidate
	if len(scored) > 0 {
		selected = &scored[0]
	}

	return &RankResult{
		Candidates: scored,
		Selected:   selected,
		Filtered:   filtered,
		Policy: Policy{
			Preference:      pref,
			Complexity:      complexity,
			Category:        req.Analysis.Category,
			QualityFloor:    floor,
			Weights:         weightsFor(pref, complexity),
			SelectionWindow: window,
			ToolRequired:    hasTools(req.Tools),
			HardFilters:     hardNames,
			SoftFilters:     softNames,
			SoftReset:       softReset,
		},
	}, nil
}

// scoreAll computes quality/speed/cost/composite for every candidate.
func scoreAll(models []catalog.ModelInfo, req RankRequest, pref string, complexity float64) []RankedCandidate {
	costs := costScores(models)
	weights := weightsFor(pref, complexity)
	budget := responseBudget(req)

	out := make([]RankedCandidate, len(models))
	for i, m := range models {
		q := qualityScore(m)
		s := speedScore(m)
		c := costs[i]
		out[i] = RankedCandidate{
			Model:               m,
			QualityScore:        q,
			SpeedScore:          s,
			CostScore:           c,
			Composite:           q*weights.Quality + s*weights.Speed + c*weights.Cost,
			BlendedPricePerMTok: blendedPrice(m),
			EstCostCents:        estCostCents(m, req.PromptTokens, budget),
		}
		out[i].Breakdown = Breakdown{
			Quality: q * weights.Quality,
			Speed:   s * weights.Speed,
			Cost:    c * weights.Cost,
		}
	}
	return out
}

// chooseWinner picks the index of the winning candidate. balanced returns
// 0 (the top composite). cheap/fast/accurate scan the top-N window for the
// best candidate by the dominant factor. Ties keep the earlier index
// (which is the lower candidate key after the stable sort).
func chooseWinner(ranked []RankedCandidate, pref string, window int) int {
	if len(ranked) == 0 {
		return -1
	}
	limit := len(ranked)
	if window > 0 && window < limit {
		limit = window
	}
	idx := 0
	switch pref {
	case prefCheap:
		best := ranked[0].BlendedPricePerMTok
		for i := 1; i < limit; i++ {
			if ranked[i].BlendedPricePerMTok < best {
				best = ranked[i].BlendedPricePerMTok
				idx = i
			}
		}
	case prefFast:
		best := ranked[0].SpeedScore
		for i := 1; i < limit; i++ {
			if ranked[i].SpeedScore > best {
				best = ranked[i].SpeedScore
				idx = i
			}
		}
	case prefAccurate:
		best := ranked[0].QualityScore
		for i := 1; i < limit; i++ {
			if ranked[i].QualityScore > best {
				best = ranked[i].QualityScore
				idx = i
			}
		}
	default: // balanced / local_only: top composite wins.
	}
	return idx
}
