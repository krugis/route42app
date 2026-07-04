package api

import "net/http"

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

	// Usage stats.
	mux.HandleFunc("GET /api/stats", s.handleStats)

	// Models list (catalog + local discovery + availability).
	mux.HandleFunc("GET /api/models", s.handleModels)
	mux.HandleFunc("GET /v1/models", s.handleModels)

	// Health (always public).
	mux.HandleFunc("GET /health", s.handleHealth)
}
