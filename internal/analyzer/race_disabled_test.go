//go:build !race

package analyzer

// raceEnabled reports whether the race detector is on (see
// race_enabled_test.go).
const raceEnabled = false
