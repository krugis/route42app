package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/krugis/route42app/internal/analyzer"
	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/llm"
	"github.com/krugis/route42app/internal/ranking"
	"github.com/krugis/route42app/internal/store"
)

// Server is the Route42 HTTP gateway. It wires the analyzer, ranking
// engine, provider registry, catalog, and store into an OpenAI-compatible
// API. Safe for concurrent use.
type Server struct {
	cfg      *config.Config
	store    *store.Store
	analyzer analyzer.PromptAnalyzer
	ranker   *ranking.Engine
	registry *llm.Registry
	catalog  *catalog.Catalog
	logger   *slog.Logger
	version  string
}

// Options configures optional Server dependencies (used in tests).
type Options struct {
	// Catalog overrides the embedded catalog snapshot.
	Catalog *catalog.Catalog
	// Logger overrides the default slog logger.
	Logger *slog.Logger
	// Registry overrides the default provider registry (offline-test seam).
	Registry *llm.Registry
	// Version is the build version reported by /health ("dev" when empty).
	Version string
}

// New builds a Server from the effective config and an opened store. The
// analyzer, ranking engine, and provider registry are constructed from the
// config; Options can override them for tests.
func New(cfg *config.Config, st *store.Store, opts Options) (*Server, error) {
	if cfg == nil {
		return nil, errors.New("api: config is required")
	}
	if st == nil {
		return nil, errors.New("api: store is required")
	}

	pa, err := analyzer.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("api: build analyzer: %w", err)
	}

	cat := opts.Catalog
	if cat == nil {
		cat, err = catalog.Load()
		if err != nil {
			return nil, fmt.Errorf("api: load catalog: %w", err)
		}
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	registry := opts.Registry
	if registry == nil {
		keyFor := func(provider string) string {
			return resolveProviderKey(st, cfg, provider)
		}
		registry = llm.NewRegistry(keyFor, providerBaseURLs(cfg), cfg.Ollama.BaseURL)
	}

	version := opts.Version
	if version == "" {
		version = "dev"
	}

	return &Server{
		cfg:      cfg,
		store:    st,
		analyzer: pa,
		ranker:   ranking.New(cat),
		registry: registry,
		catalog:  cat,
		logger:   logger,
		version:  version,
	}, nil
}

func providerBaseURLs(cfg *config.Config) map[string]string {
	out := make(map[string]string, len(cfg.Providers))
	for name, p := range cfg.Providers {
		if p.BaseURL != "" {
			out[name] = p.BaseURL
		}
	}
	return out
}

// availableProviders returns canonical names of providers with a stored OR
// config-file API key, plus "ollama" only if local models are discovered.
// (Ollama never needs a key; its availability is determined at discovery
// time in the chat path.)
func (s *Server) availableProviders(ctx context.Context, locals []catalog.ModelInfo) []string {
	configured := make(map[string]bool)
	for name, p := range s.cfg.Providers {
		if p.APIKey != "" {
			configured[llm.CanonicalProvider(name)] = true
		}
	}
	if stored, err := s.store.ListProviders(); err == nil {
		for _, p := range stored {
			configured[p] = true
		}
	}
	// Ollama counts as available only when it actually has models.
	if len(locals) > 0 {
		configured["ollama"] = true
	}
	out := make([]string, 0, len(configured))
	for name := range configured {
		out = append(out, name)
	}
	return out
}

// localCandidates discovers Ollama models and enriches them with catalog
// metrics where a matching catalog entry exists (provider=ollama). An
// unreachable Ollama returns (nil, nil) — local-first means a missing
// Ollama is never fatal.
func (s *Server) localCandidates(ctx context.Context) ([]catalog.ModelInfo, error) {
	discovered, err := llm.DiscoverOllama(ctx, s.cfg.Ollama.BaseURL)
	if err != nil {
		s.logger.Debug("ollama discovery unavailable", "err", err)
		return nil, nil
	}
	byID := make(map[string]catalog.ModelInfo)
	for _, m := range s.catalog.Models {
		if m.Provider == "ollama" {
			byID[strings.ToLower(m.ID)] = m
		}
	}
	out := make([]catalog.ModelInfo, 0, len(discovered))
	for _, d := range discovered {
		m := catalog.ModelInfo{
			ID:       d.Name,
			Provider: "ollama",
			Source:   catalog.SourceLocal,
			// Local models have no cost and (by default) unknown metrics;
			// the ranker treats local-no-data as a fast latency tier.
		}
		if matched, ok := byID[strings.ToLower(d.Name)]; ok {
			m.QualityScore = matched.QualityScore
			m.OutputTokensPerSecond = matched.OutputTokensPerSecond
			m.TimeToFirstTokenMs = matched.TimeToFirstTokenMs
			m.ContextWindow = matched.ContextWindow
			m.SupportsTools = matched.SupportsTools
			m.SupportsVision = matched.SupportsVision
		}
		out = append(out, m)
	}
	return out, nil
}

// Handler returns the HTTP handler serving all Route42 routes. Routes use
// Go 1.22 ServeMux method+path patterns; /v1/* are aliases of /api/* for
// OpenAI-client compatibility. The mux is wrapped with panic recovery and
// structured per-request logging, then the optional auth layer.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerRoutes(mux)
	return s.withRecover(s.withLogging(s.withAuth(mux)))
}

// withAuth applies the optional static-token auth to /api and /v1 routes
// when server.api_token is set. /health is always public.
func (s *Server) withAuth(h http.Handler) http.Handler {
	token := s.cfg.Server.APIToken
	if token == "" {
		return h
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/health") {
			h.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("Authorization")
		if got != "Bearer "+token {
			writeError(w, http.StatusUnauthorized, "invalid or missing API token", "authentication_error")
			return
		}
		h.ServeHTTP(w, r)
	})
}

// Run serves the gateway on the configured port until ctx is cancelled.
func (s *Server) Run(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	s.logger.Info("route42 gateway listening", "addr", addr, "analyzer", s.cfg.Analyzer.Mode)

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// writeJSON writes a JSON response with the given status. Errors are
// logged and rendered as a 500.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Default().Error("write json response", "err", err)
	}
}

// writeError writes an OpenAI-style error envelope.
func writeError(w http.ResponseWriter, status int, msg, errType string) {
	writeJSON(w, status, ErrorJSON{Error: ErrorBody{Message: msg, Type: errType}})
}
