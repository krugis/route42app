//go:build race

package analyzer

// raceEnabled reports whether the race detector is on. Wall-clock timing
// assertions are skipped under -race: its 5-10x instrumentation overhead
// on shared CI runners makes them meaningless (the benchmark tracks the
// real budget).
const raceEnabled = true
