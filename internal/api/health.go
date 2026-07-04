package api

import (
	"net/http"
	"runtime"
	"time"
)

// handleHealth is the always-public liveness/readiness probe. It reports
// the gateway status, configured analyzer mode, and whether the store is
// reachable. It never requires auth.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	storeOK := true
	if _, err := s.store.GetPrefs(); err != nil {
		storeOK = false
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":         status,
		"version":        "dev",
		"analyzer":       s.cfg.Analyzer.Mode,
		"store_ok":       storeOK,
		"catalog_models": len(s.catalog.Models),
		"uptime":         time.Since(startTime()).Round(time.Second).String(),
		"go_version":     runtime.Version(),
	})
}

// startTime is the process start time, captured once. Overridable in tests.
var startTime = func() func() time.Time {
	t := time.Now()
	return func() time.Time { return t }
}()
