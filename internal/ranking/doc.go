// Package ranking filters candidate models against user constraints and
// ranks them with a composite quality/speed/cost score adjusted by
// preference mode and prompt complexity.
//
// The pipeline is: build candidates (catalog models for providers with a
// configured key, plus discovered local models) → hard filters
// (disallowed, only_local, tool-capable) → soft filters with FILTER_RESET
// (only_free, max cost, latency tolerance, quality floor) → score →
// select. Selection picks the top composite for balanced, or the best by
// the dominant factor within a top-N window for cheap/fast/accurate.
//
// Scoring is deterministic given the inputs and every decision carries a
// per-factor breakdown (see RankResult.Explain) so the "why this model?"
// question is always answerable.
package ranking
