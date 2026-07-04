package api

import (
	"fmt"
	"net/http"

	"github.com/krugis/route42app/internal/config"
)

// handleGetPrefs returns the stored routing preferences.
func (s *Server) handleGetPrefs(w http.ResponseWriter, r *http.Request) {
	prefs, err := s.store.GetPrefs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load prefs: "+err.Error(), "server_error")
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// handleSetPrefs replaces the stored routing preferences.
func (s *Server) handleSetPrefs(w http.ResponseWriter, r *http.Request) {
	var prefs config.Prefs
	if err := decodeJSON(r, &prefs); err != nil {
		writeError(w, http.StatusBadRequest, "invalid prefs: "+err.Error(), "invalid_request_error")
		return
	}
	if err := validatePrefs(prefs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	if err := s.store.SetPrefs(prefs); err != nil {
		writeError(w, http.StatusInternalServerError, "save prefs: "+err.Error(), "server_error")
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// validatePrefs mirrors config.Validate's preference checks so a bad PUT is
// rejected with a clear 400 rather than a 500 at routing time.
func validatePrefs(p config.Prefs) error {
	switch p.Priority {
	case "balanced", "fast", "cheap", "accurate":
	default:
		return errf("priority %q is not supported (use balanced, fast, cheap, or accurate)", p.Priority)
	}
	if p.FallbackDepth < 0 {
		return errf("fallback_depth must be >= 0, got %d", p.FallbackDepth)
	}
	if p.MaxCostCents < 0 {
		return errf("max_cost_cents must be >= 0, got %g", p.MaxCostCents)
	}
	if p.LatencyToleranceMs < 0 {
		return errf("latency_tolerance_ms must be >= 0, got %d", p.LatencyToleranceMs)
	}
	if p.MaxResponseTokens < 0 {
		return errf("max_response_tokens must be >= 0, got %d", p.MaxResponseTokens)
	}
	return nil
}

// errf is a small helper to format validation errors.
func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

type prefsError struct{ msg string }

func (e *prefsError) Error() string { return e.msg }
