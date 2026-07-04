package api

import (
	"net/http"

	"github.com/krugis/route42app/internal/analyzer"
	"github.com/krugis/route42app/internal/ranking"
)

// handleRecommend runs the routing pipeline without executing, returning
// the ranked candidates and the deterministic explanation.
func (s *Server) handleRecommend(w http.ResponseWriter, r *http.Request) {
	var req RecommendRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body: "+err.Error(), "invalid_request_error")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required and must be non-empty", "invalid_request_error")
		return
	}

	prefs, err := s.store.GetPrefs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load preferences: "+err.Error(), "server_error")
		return
	}

	ctx := r.Context()
	locals, _ := s.localCandidates(ctx)

	analysis, err := s.analyzer.Analyze(ctx, toAnalyzerMessages(req.Messages))
	if err != nil {
		// Recommend is advisory; degrade to a neutral analysis rather than
		// failing the whole request.
		analysis = analyzer.AnalysisResult{Complexity: 0, Category: analyzer.CategoryGeneral, Analyzer: "fallback"}
	}

	pinned := req.Model != "" && req.Model != "auto"
	var rankResult *ranking.RankResult
	if pinned {
		prov, _ := s.lookupProvider(req.Model)
		writeJSON(w, http.StatusOK, RecommendResponse{
			XRoute42:    XRoute42{SelectedModel: req.Model, Provider: prov, Reason: "pinned", CandidatesConsidered: 1},
			Explanation: "model pinned by request (routing bypassed)",
		})
		return
	}

	rankResult, err = s.ranker.Rank(ranking.RankRequest{
		Analysis:          analysis,
		Prefs:             prefs,
		Available:         s.availableProviders(ctx, locals),
		LocalModels:       locals,
		Tools:             req.Tools,
		PromptTokens:      estimatePromptTokens(req.Messages),
		MaxResponseTokens: prefs.MaxResponseTokens,
	})
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "no model available: "+err.Error(), "server_error")
		return
	}

	cands := make([]RecommendCandidate, 0, len(rankResult.Candidates))
	for _, c := range rankResult.Candidates {
		cands = append(cands, RecommendCandidate{
			Model:        c.Model.ID,
			Provider:     c.Model.Provider,
			Source:       string(c.Model.Source),
			Composite:    c.Composite,
			Quality:      c.QualityScore,
			Speed:        c.SpeedScore,
			Cost:         c.CostScore,
			EstCostCents: c.EstCostCents,
		})
	}

	resp := RecommendResponse{
		XRoute42: XRoute42{
			SelectedModel:        selectedModelID(rankResult),
			Provider:             selectedProvider(rankResult),
			Analyzer:             analysis.Analyzer,
			Complexity:           analysis.Complexity,
			Category:             analysis.Category,
			Signals:              analysis.Signals,
			CandidatesConsidered: len(rankResult.Candidates),
			Reason:               "routed",
		},
		Candidates:  cands,
		Explanation: rankResult.Explain(),
	}
	if rankResult.Selected != nil {
		resp.XRoute42.EstCostCents = rankResult.Selected.EstCostCents
	}
	writeJSON(w, http.StatusOK, resp)
}

func selectedModelID(r *ranking.RankResult) string {
	if r == nil || r.Selected == nil {
		return ""
	}
	return r.Selected.Model.ID
}

func selectedProvider(r *ranking.RankResult) string {
	if r == nil || r.Selected == nil {
		return ""
	}
	return r.Selected.Model.Provider
}
