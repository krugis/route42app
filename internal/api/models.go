package api

import (
	"net/http"

	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/llm"
)

// handleModels returns the OpenAI-style models list: the catalog merged
// with local Ollama discovery, each annotated with availability and
// Route42 metadata under x_route42.
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	locals, _ := s.localCandidates(ctx)
	available := make(map[string]bool)
	for _, p := range s.availableProviders(ctx, locals) {
		available[p] = true
	}

	// Discovered local model ids (for marking availability).
	localIDs := make(map[string]bool, len(locals))
	for _, m := range locals {
		localIDs[m.ID] = true
	}

	out := make([]ModelInfo, 0, len(s.catalog.Models)+len(locals))
	seen := make(map[string]bool)

	add := func(m catalog.ModelInfo, availableNow bool) {
		key := m.Provider + "/" + m.ID
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, ModelInfo{
			ID:      m.ID,
			Object:  "model",
			Created: nowUnix(),
			OwnedBy: m.Provider,
			XRoute42: ModelMeta{
				Provider:           m.Provider,
				Source:             string(m.Source),
				QualityScore:       m.QualityScore,
				OutputTokensPerSec: m.OutputTokensPerSecond,
				TimeToFirstTokenMs: m.TimeToFirstTokenMs,
				InputPricePerMTok:  m.InputPricePerMTok,
				OutputPricePerMTok: m.OutputPricePerMTok,
				SupportsTools:      m.SupportsTools,
				Available:          availableNow,
			},
		})
	}

	// Catalog cloud models for available providers.
	for _, m := range s.catalog.Models {
		if m.Source == catalog.SourceLocal {
			continue
		}
		add(m, available[llm.CanonicalProvider(m.Provider)])
	}
	// Discovered local models.
	for _, m := range locals {
		add(m, true)
	}

	writeJSON(w, http.StatusOK, ModelsResponse{Object: "list", Data: out})
}
