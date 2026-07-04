package api

import (
	"net/http"
	"strconv"
)

// handleStats returns usage aggregates over a time window. The `days`
// query parameter (default 0 = all time) selects the window.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	days := 0
	if d := r.URL.Query().Get("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v >= 0 {
			days = v
		}
	}
	stats, err := s.store.GetStats(days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load stats: "+err.Error(), "server_error")
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
