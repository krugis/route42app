package api

import (
	"net/http"

	"github.com/krugis/route42app/internal/webui"
)

// registerRoutes wires every Route42 endpoint onto the ServeMux. /v1/*
// mirror /api/* so any OpenAI SDK pointed at the gateway works unchanged.
// Health is the only always-public route.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Chat completions (OpenAI-compatible). Both /api and /v1 paths.
	mux.HandleFunc("POST /api/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("POST /v1/chat/completions", s.handleChatCompletions)

	// Provider keys: write-only values (masked on read).
	mux.HandleFunc("POST /api/keys", s.handleAddKey)
	mux.HandleFunc("GET /api/keys", s.handleListKeys)
	mux.HandleFunc("DELETE /api/keys", s.handleDeleteKey)

	// Routing preferences.
	mux.HandleFunc("GET /api/prefs", s.handleGetPrefs)
	mux.HandleFunc("PUT /api/prefs", s.handleSetPrefs)

	// Routing recommendation (no execution).
	mux.HandleFunc("POST /api/recommend", s.handleRecommend)

	// Usage stats and the interaction log.
	mux.HandleFunc("GET /api/stats", s.handleStats)
	mux.HandleFunc("GET /api/interactions", s.handleInteractions)

	// Models list (catalog + local discovery + availability).
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /v1/models", s.handleModels)

	// Health (always public).
	mux.HandleFunc("GET /health", s.handleHealth)

	// Embedded web console (optional; server.ui). Registered on the GET /
	// catch-all so every API route above still wins. The gateway is fully
	// usable without it via the CLI and the HTTP API.
	if s.cfg.Server.UI {
		mux.Handle("GET /", webui.Handler())
	}
}
