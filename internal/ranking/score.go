package ranking

import (
	"math"
	"sort"
	"strings"

	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/config"
)

// This file ports the scoring core from the private router, minus pro
// enrichment (ML behavior bonus, similarity bonus, evidence-confidence
// scaling, personal speed-stats overrides). Scoring is deterministic and
// uses three factors on a 0..1 scale:
//
//   - quality: catalog QualityScore / 100 (single composite; CE has no
//     per-category breakdown).
//   - speed:   an absolute combined-speed score based on throughput and
//     time-to-first-token. Local models with unknown speed are treated as
//     a fast latency tier (no network hop).
//   - cost:    log-scale, P95-clipped normalization of the blended price
//     across the eligible pool. Free models score 1.
//
// The composite is quality*Wq + speed*Ws + cost*Wc, with weights chosen
// by preference mode and adjusted by complexity.

// preference constants (mirror config's valid priorities).
const (
	prefBalanced  = "balanced"
	prefFast      = "fast"
	prefCheap     = "cheap"
	prefAccurate  = "accurate"
	prefLocalOnly = "local_only"
)

// selectionWindow is the top-N window within which the dominant factor
// decides the winner for non-balanced preferences.
const (
	windowBalanced = 5
	windowAccurate = 5
	windowCheap    = 10
	windowFast     = 10
)

// weightsFor returns the composite weights for a preference mode after
// complexity adjustment. The adjustment makes simple prompts favor cost
// (any qualified model handles them) and complex prompts favor quality
// (a weak model must not be chosen for a hard task).
func weightsFor(preference string, complexity float64) Weights {
	pref := normalizePreference(preference)
	c := clamp01(complexity)

	switch pref {
	case prefFast:
		if c >= 0.75 {
			return Weights{Quality: 0.60, Speed: 0.30, Cost: 0.10}
		}
		return Weights{Quality: 0.30, Speed: 0.50, Cost: 0.20}

	case prefCheap:
		if c < 0.25 {
			return Weights{Quality: 0.05, Speed: 0.05, Cost: 0.90}
		}
		if c < 0.50 {
			return Weights{Quality: 0.10, Speed: 0.10, Cost: 0.80}
		}
		return Weights{Quality: 0.30, Speed: 0.20, Cost: 0.50}

	case prefAccurate:
		if c >= 0.75 {
			return Weights{Quality: 0.80, Speed: 0.10, Cost: 0.10}
		}
		return Weights{Quality: 0.70, Speed: 0.15, Cost: 0.15}

	case prefLocalOnly:
		// local_only is a hard filter, not a scoring mode; score the
		// surviving locals with balanced weights adjusted by complexity.
		fallthrough

	default: // balanced
		if c >= 0.75 {
			return Weights{Quality: 0.70, Speed: 0.15, Cost: 0.15}
		}
		if c >= 0.50 {
			// boost quality by 0.10 and rescale the remainder.
			return normalizeWeights(Weights{Quality: 0.60, Speed: 0.24, Cost: 0.16})
		}
		if c < 0.25 {
			return Weights{Quality: 0.15, Speed: 0.15, Cost: 0.70}
		}
		return Weights{Quality: 0.25, Speed: 0.25, Cost: 0.50}
	}
}

// selectionWindowFor returns the winner-pick window for a preference.
func selectionWindowFor(preference string) int {
	switch normalizePreference(preference) {
	case prefCheap, prefFast:
		return windowCheap
	default:
		return windowBalanced
	}
}

// minQualityForComplexity returns the quality floor (0..1) a model must
// meet to be considered for a given complexity. Models with unknown
// quality (QualityScore 0) are exempt — lack of data is not evidence of
// inadequacy (see filterQualityFloor).
func minQualityForComplexity(complexity float64) float64 {
	switch {
	case complexity < 0.25:
		return 0.30
	case complexity < 0.50:
		return 0.50
	case complexity < 0.75:
		return 0.70
	case complexity < 0.90:
		return 0.85
	default:
		return 0.95
	}
}

// qualityScore normalizes the catalog quality (0..100) to 0..1.
func qualityScore(m catalog.ModelInfo) float64 {
	return clamp01(m.QualityScore / 100.0)
}

// combinedSpeedScore returns an absolute 0..1000 speed score for a model
// based on throughput (tokens/sec) and time-to-first-token (seconds).
// Higher is faster. It estimates the wall-clock time to emit 1000 tokens:
//
//	totalTime = ttft + 1000 / tps
//	score     = 1000 / totalTime   (capped at 1000)
//
// Models with no data return 0. The throughput-only path assumes a
// nominal 0.5s TTFT; the TTFT-only path falls back to 100/ttft.
func combinedSpeedScore(tps, ttftSeconds float64) float64 {
	if tps <= 0 && ttftSeconds <= 0 {
		return 0
	}
	if tps <= 0 {
		return math.Min(1000, 100.0/ttftSeconds)
	}
	if ttftSeconds <= 0 {
		ttftSeconds = 0.5
	}
	total := ttftSeconds + 1000.0/tps
	score := 1000.0 / total
	if score > 1000 {
		score = 1000
	}
	return score
}

// speedScore returns the 0..1 speed score for a single model. Local models
// with no measured speed are treated as a fast latency tier (score 1):
// they have no network hop, so TTFT is negligible even when throughput is
// unknown. Cloud models with no speed data score 0 (penalized on speed,
// but still selectable on quality/cost).
func speedScore(m catalog.ModelInfo) float64 {
	tps := m.OutputTokensPerSecond
	ttft := m.TimeToFirstTokenMs / 1000.0
	if tps <= 0 && ttft <= 0 {
		if m.Source == catalog.SourceLocal {
			return 1.0
		}
		return 0
	}
	return clamp01(combinedSpeedScore(tps, ttft) / 1000.0)
}

// costScores returns a 0..1 cost score per model, aligned with the input
// slice. Free models (IsFree) score 1. Paid models are normalized with a
// log-scale, P95-clipped mapping so ultra-expensive outliers do not
// compress cheap models into a narrow band: log(price) is mapped so the
// cheapest paid model scores ~1 and the P95-priced model scores ~0, with
// anything above P95 clipped to 0.
func costScores(models []catalog.ModelInfo) []float64 {
	out := make([]float64, len(models))

	var paid []float64
	for _, m := range models {
		if m.IsFree() {
			continue
		}
		if p := blendedPrice(m); p > 0 {
			paid = append(paid, p)
		}
	}

	// No paid models at all: free models get 1, the rest (shouldn't happen) 0.
	if len(paid) == 0 {
		for i, m := range models {
			if m.IsFree() {
				out[i] = 1.0
			}
		}
		return out
	}

	sort.Float64s(paid)
	minCost := paid[0]

	// P95 as the effective max (winsorization) to resist outliers.
	p95Idx := int(float64(len(paid)) * 0.95)
	if p95Idx >= len(paid) {
		p95Idx = len(paid) - 1
	}
	maxCost := paid[p95Idx]
	if maxCost <= minCost {
		// P95 collapsed onto the min (e.g. all paid models the same price);
		// fall back to the true max so the range is non-degenerate.
		maxCost = paid[len(paid)-1]
	}

	for i, m := range models {
		switch {
		case m.IsFree():
			out[i] = 1.0
		default:
			p := blendedPrice(m)
			if p <= 0 {
				out[i] = 0
				continue
			}
			if maxCost <= minCost {
				// All paid models have the same price: give them a neutral 0.5.
				out[i] = 0.5
				continue
			}
			if p > maxCost {
				out[i] = 0 // above P95 outlier
				continue
			}
			logMin := math.Log(minCost)
			logMax := math.Log(maxCost)
			logRange := logMax - logMin
			if logRange <= 0 {
				out[i] = 0.5
				continue
			}
			out[i] = clamp01((logMax - math.Log(p)) / logRange)
		}
	}
	return out
}

// blendedPrice is the 3:1 input:output blended price in USD per million
// tokens. The 3:1 ratio approximates a typical chat workload.
func blendedPrice(m catalog.ModelInfo) float64 {
	return (3.0*m.InputPricePerMTok + m.OutputPricePerMTok) / 4.0
}

// estCostCents estimates the request cost in US cents from the prompt and
// response token budgets and the model's per-million-token prices.
func estCostCents(m catalog.ModelInfo, promptTokens, maxResponseTokens int) float64 {
	if maxResponseTokens <= 0 {
		maxResponseTokens = 1024
	}
	in := float64(promptTokens) / 1e6 * m.InputPricePerMTok
	out := float64(maxResponseTokens) / 1e6 * m.OutputPricePerMTok
	return (in + out) * 100.0 // USD -> cents
}

// responseBudget resolves the response token cap for the cost estimate:
// request override, then prefs, then default.
func responseBudget(req RankRequest) int {
	if req.MaxResponseTokens > 0 {
		return req.MaxResponseTokens
	}
	if req.Prefs.MaxResponseTokens > 0 {
		return req.Prefs.MaxResponseTokens
	}
	return 1024
}

// preferenceOf resolves the effective preference, honoring only_local as a
// hard filter that downgrades scoring to balanced (the locals-only pool is
// scored with balanced weights).
func preferenceOf(p config.Prefs) string {
	if p.OnlyLocal {
		return prefLocalOnly
	}
	return normalizePreference(p.Priority)
}

func normalizePreference(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "speed", "quality", "quality_first":
		// "speed" -> fast; "quality"/"quality_first" -> accurate (legacy aliases).
		if strings.EqualFold(p, "speed") {
			return prefFast
		}
		return prefAccurate
	case prefBalanced, prefFast, prefCheap, prefAccurate, prefLocalOnly:
		return strings.ToLower(strings.TrimSpace(p))
	default:
		return prefBalanced
	}
}

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func normalizeWeights(w Weights) Weights {
	total := w.Quality + w.Speed + w.Cost
	if total <= 0 {
		return Weights{Quality: 1, Speed: 0, Cost: 0}
	}
	return Weights{Quality: w.Quality / total, Speed: w.Speed / total, Cost: w.Cost / total}
}
