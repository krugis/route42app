package api

import (
	"net/http"
	"sort"
	"strings"

	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/llm"
	"github.com/krugis/route42app/internal/store"
)

// keyRequest is the body of POST /api/keys.
type keyRequest struct {
	Provider string `json:"provider"`
	APIKey   string `json:"api_key"`
}

// keyEntry is the masked representation returned by GET /api/keys. The key
// value itself is write-only and never exposed.
type keyEntry struct {
	Provider string `json:"provider"`
	KeyMask  string `json:"key_mask"` // e.g. "sk-...7a2f"
	HasKey   bool   `json:"has_key"`
}

// handleAddKey stores (or replaces) a provider API key.
func (s *Server) handleAddKey(w http.ResponseWriter, r *http.Request) {
	var req keyRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error(), "invalid_request_error")
		return
	}
	if req.Provider == "" {
		writeError(w, http.StatusBadRequest, "provider is required", "invalid_request_error")
		return
	}
	if req.APIKey == "" {
		writeError(w, http.StatusBadRequest, "api_key is required", "invalid_request_error")
		return
	}
	if err := s.store.SetProviderKey(req.Provider, req.APIKey); err != nil {
		writeError(w, http.StatusInternalServerError, "store key: "+err.Error(), "server_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"provider": llm.CanonicalProvider(req.Provider),
		"status":   "ok",
	})
}

// handleListKeys returns the configured providers with masked keys.
func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	providers, err := s.store.ListProviders()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list keys: "+err.Error(), "server_error")
		return
	}
	// Include config-file providers too (their keys are not in the store).
	seen := make(map[string]bool, len(providers))
	for _, p := range providers {
		seen[p] = true
	}
	for name, p := range s.cfg.Providers {
		if p.APIKey == "" {
			continue
		}
		c := llm.CanonicalProvider(name)
		if !seen[c] {
			seen[c] = true
			providers = append(providers, c)
		}
	}

	names := make([]string, 0, len(seen))
	for prov := range seen {
		names = append(names, prov)
	}
	sort.Strings(names)
	out := make([]keyEntry, 0, len(names))
	for _, prov := range names {
		out = append(out, keyEntry{Provider: prov, HasKey: true, KeyMask: maskKey(s.keyFor(prov))})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// handleDeleteKey removes a stored provider key.
func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	provider := r.URL.Query().Get("provider")
	if provider == "" {
		writeError(w, http.StatusBadRequest, "provider query parameter is required", "invalid_request_error")
		return
	}
	if err := s.store.DeleteProviderKey(provider); err != nil {
		writeError(w, http.StatusInternalServerError, "delete key: "+err.Error(), "server_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": llm.CanonicalProvider(provider), "status": "deleted"})
}

// keyFor resolves the effective key for a provider (see resolveProviderKey).
func (s *Server) keyFor(provider string) string {
	return resolveProviderKey(s.store, s.cfg, provider)
}

// resolveProviderKey returns the effective API key for a provider:
// runtime (store) keys win over config-file keys, so a key set via
// POST /api/keys takes effect immediately. A store read error falls
// through to the config-file key rather than disabling the provider.
// Config lookup accepts both canonical and alias spellings.
func resolveProviderKey(st *store.Store, cfg *config.Config, provider string) string {
	canonical := llm.CanonicalProvider(provider)
	if k, err := st.GetProviderKey(canonical); err == nil && k != "" {
		return k
	}
	if p, ok := cfg.Providers[canonical]; ok && p.APIKey != "" {
		return p.APIKey
	}
	for name, p := range cfg.Providers {
		if llm.CanonicalProvider(name) == canonical && p.APIKey != "" {
			return p.APIKey
		}
	}
	return ""
}

// maskKey renders a key as "prefix...suffix" for display. Empty keys return
// an empty mask.
func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 8 {
		return strings.Repeat("*", len(k))
	}
	return k[:3] + "..." + k[len(k)-4:]
}
