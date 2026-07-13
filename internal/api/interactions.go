package api

import (
	"net/http"
	"strconv"

	"github.com/krugis/route42app/internal/store"
)

// handleInteractions returns the most recent interaction records, newest
// first. The `limit` query parameter (default 50, max 500) bounds the page.
func (s *Server) handleInteractions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 {
			limit = v
		}
	}
	if limit > 500 {
		limit = 500
	}
	interactions, err := s.store.RecentInteractions(limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load interactions: "+err.Error(), "server_error")
		return
	}
	if interactions == nil {
		interactions = []store.Interaction{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"interactions": interactions})
}
