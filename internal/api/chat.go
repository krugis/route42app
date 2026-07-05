package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/krugis/route42app/internal/analyzer"
	"github.com/krugis/route42app/internal/catalog"
	"github.com/krugis/route42app/internal/config"
	"github.com/krugis/route42app/internal/llm"
	"github.com/krugis/route42app/internal/ranking"
	"github.com/krugis/route42app/internal/store"
)

// handleChatCompletions is the OpenAI-compatible chat endpoint. Pipeline:
// parse → (pin or analyze → rank) → execute (with fallback) → log. Both
// streaming (SSE) and non-streaming are supported.
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error(), "invalid_request_error")
		return
	}
	if len(req.Messages) == 0 {
		writeError(w, http.StatusBadRequest, "messages is required and must be non-empty", "invalid_request_error")
		return
	}

	// Honor explicit model pin: a specific model bypasses routing. "auto"
	// or absent routes (unless prefs.default_model pins a default).
	pinned := req.Model != "" && req.Model != "auto"

	prefs, err := s.store.GetPrefs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "load preferences: "+err.Error(), "server_error")
		return
	}

	// If the request didn't pin a model but preferences define a default,
	// pin to that default. This lets a deployment force a specific model
	// without per-request configuration.
	if !pinned && prefs.DefaultModel != "" {
		req.Model = prefs.DefaultModel
		pinned = true
	}

	ctx := r.Context()
	locals, _ := s.localCandidates(ctx)

	var analysis analyzer.AnalysisResult
	var rankResult *ranking.RankResult
	reason := "pinned"

	if pinned {
		// Still analyze for the interaction log / x_route42, but never fail
		// the request on analysis for a pinned model.
		analysis, _ = s.analyzer.Analyze(ctx, toAnalyzerMessages(req.Messages))
	} else {
		analysis, err = s.analyzer.Analyze(ctx, toAnalyzerMessages(req.Messages))
		if err != nil {
			// Routing must never be blocked by analysis errors: fall back to
			// a neutral analysis (general, low complexity) and route anyway.
			s.logger.Warn("analyze failed; routing with neutral analysis", "err", err)
			analysis = analyzer.AnalysisResult{Complexity: 0, Category: analyzer.CategoryGeneral, Analyzer: "fallback"}
		}
		rankResult, err = s.ranker.Rank(ranking.RankRequest{
			Analysis:          analysis,
			Prefs:             prefs,
			Available:         s.availableProviders(ctx, locals),
			LocalModels:       locals,
			Tools:             req.Tools,
			PromptTokens:      estimatePromptTokens(req.Messages),
			MaxResponseTokens: intValue(req.MaxTokens),
		})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "no model available for this request: "+err.Error(), "server_error")
			return
		}
		reason = "routed"
	}

	// Build the execution chain: the pinned model, or the ranked candidates
	// up to fallback_depth (at least 1, the selected model).
	chain := s.executionChain(req, pinned, prefs, rankResult)
	if len(chain) == 0 {
		writeError(w, http.StatusServiceUnavailable, "no model available for this request", "server_error")
		return
	}

	maxTokens := intValue(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = prefs.MaxResponseTokens
	}

	if req.Stream {
		s.executeStream(w, r, req, chain, analysis, rankResult, reason, maxTokens)
	} else {
		s.executeComplete(w, r, req, chain, analysis, rankResult, reason, maxTokens)
	}
}

// execTarget is one model to try in the fallback chain.
type execTarget struct {
	model    string // provider-scoped id
	provider string
}

// executionChain resolves the ordered list of (provider, model) targets.
// For a pinned model, the provider is inferred from the catalog or defaults
// to the model's own provider; there is exactly one target. For routing,
// the chain walks the ranked candidates up to fallback_depth+1 (the
// selected model plus `fallback_depth` fallbacks).
func (s *Server) executionChain(req ChatRequest, pinned bool, prefs config.Prefs, rankResult *ranking.RankResult) []execTarget {
	if pinned {
		prov, _ := s.lookupProvider(req.Model)
		return []execTarget{{model: req.Model, provider: prov}}
	}
	if rankResult == nil || len(rankResult.Candidates) == 0 {
		return nil
	}
	depth := prefs.FallbackDepth
	if depth < 0 {
		depth = 0
	}
	limit := depth + 1
	if limit > len(rankResult.Candidates) {
		limit = len(rankResult.Candidates)
	}
	out := make([]execTarget, 0, limit)
	for i := 0; i < limit; i++ {
		c := rankResult.Candidates[i]
		out = append(out, execTarget{model: c.Model.ID, provider: c.Model.Provider})
	}
	return out
}

// lookupProvider finds the catalog provider for a model id, returning
// ("", false) when unknown (the registry will then error on a real call).
func (s *Server) lookupProvider(modelID string) (string, bool) {
	for _, m := range s.catalog.Models {
		if m.ID == modelID {
			return m.Provider, true
		}
	}
	return "", false
}

// executeComplete runs the non-streaming path: try each target until one
// succeeds, then build the OpenAI response with the x_route42 extension.
func (s *Server) executeComplete(w http.ResponseWriter, r *http.Request, req ChatRequest, chain []execTarget, analysis analyzer.AnalysisResult, rankResult *ranking.RankResult, reason string, maxTokens int) {
	start := time.Now()
	providerReq := llm.ChatRequest{
		Model:       chain[0].model,
		Messages:    toLLMMessages(req.Messages),
		MaxTokens:   maxTokens,
		Temperature: floatValue(req.Temperature),
		Tools:       req.Tools,
	}

	var lastErr error
	attempts := 0
	for i, t := range chain {
		attempts = i
		provider, err := s.registry.Provider(t.provider)
		if err != nil {
			lastErr = err
			continue
		}
		providerReq.Model = t.model
		resp, err := provider.Complete(r.Context(), providerReq)
		if err != nil {
			lastErr = err
			if !retryable(err) || i == len(chain)-1 {
				break
			}
			continue // fall back to next candidate
		}
		latency := time.Since(start)
		selected := t
		s.logInteraction(analysis, selected, resp.Usage.PromptTokens, resp.Usage.CompletionTokens, attempts, latency, "ok", rankResult)
		writeJSON(w, http.StatusOK, s.buildCompleteResponse(req, t, resp, analysis, rankResult, reason, attempts))
		return
	}

	// All targets failed.
	s.logInteraction(analysis, chain[len(chain)-1], 0, 0, attempts, time.Since(start), "error", rankResult)
	status := http.StatusBadGateway
	if lastErr != nil {
		s.logger.Error("chat completion failed", "err", lastErr, "attempts", attempts+1)
	}
	writeError(w, status, fmt.Sprintf("all providers failed: %v", lastErr), "server_error")
}

// executeStream runs the streaming (SSE) path. The first successful
// provider streams deltas; a provider that fails before the first byte is
// retried on the next candidate. After the content stream completes, a
// final metadata chunk carries the x_route42 extension.
func (s *Server) executeStream(w http.ResponseWriter, r *http.Request, req ChatRequest, chain []execTarget, analysis analyzer.AnalysisResult, rankResult *ranking.RankResult, reason string, maxTokens int) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported", "server_error")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	start := time.Now()
	providerReq := llm.ChatRequest{
		Messages:    toLLMMessages(req.Messages),
		MaxTokens:   maxTokens,
		Temperature: floatValue(req.Temperature),
		Tools:       req.Tools,
	}

	var lastErr error
	attempts := 0
	for i, t := range chain {
		attempts = i
		provider, err := s.registry.Provider(t.provider)
		if err != nil {
			lastErr = err
			continue
		}
		providerReq.Model = t.model
		ch, err := provider.Stream(r.Context(), providerReq)
		if err != nil {
			lastErr = err
			if !retryable(err) || i == len(chain)-1 {
				break
			}
			continue
		}

		// Stream deltas. If the first chunk is an error before any byte was
		// sent, fall back to the next candidate.
		anyByte := false
		promptTokens, completionTokens := 0, 0
		var finishReason string
		id := "chatcmpl-" + randID()

		for chunk := range ch {
			if chunk.Err != nil {
				lastErr = chunk.Err
				if !anyByte && retryable(chunk.Err) && i < len(chain)-1 {
					// try next candidate without having sent anything
					goto nextCandidate
				}
				// Already streaming or non-retryable: emit the error inline.
				writeSSE(w, flusher, StreamChunk{
					ID: id, Object: "chat.completion.chunk", Created: nowUnix(), Model: t.model,
					Choices: []StreamChoice{{Index: 0, Delta: ChatDelta{}, FinishReason: ptrString("error")}},
				})
				writeSSEDone(w, flusher)
				s.logInteraction(analysis, t, promptTokens, completionTokens, attempts, time.Since(start), "error", rankResult)
				return
			}
			anyByte = true
			if chunk.Usage.PromptTokens > 0 {
				promptTokens = chunk.Usage.PromptTokens
			}
			if chunk.Usage.CompletionTokens > 0 {
				completionTokens = chunk.Usage.CompletionTokens
			}
			if chunk.Done {
				finishReason = "stop"
				continue
			}
			// Reasoning deltas are surfaced on their own field
			// (reasoning_content, the de-facto convention) — never as
			// answer content, and never displacing real content.
			delta := ChatDelta{Content: chunk.Delta, ReasoningContent: chunk.ReasoningDelta}
			writeSSE(w, flusher, StreamChunk{
				ID: id, Object: "chat.completion.chunk", Created: nowUnix(), Model: t.model,
				Choices: []StreamChoice{{Index: 0, Delta: delta}},
			})
		}

		if finishReason == "" {
			finishReason = "stop"
		}
		if promptTokens == 0 {
			promptTokens = estimatePromptTokens(req.Messages)
		}
		// Final content chunk with finish_reason.
		writeSSE(w, flusher, StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: nowUnix(), Model: t.model,
			Choices: []StreamChoice{{Index: 0, Delta: ChatDelta{}, FinishReason: ptrString(finishReason)}},
		})
		// Final metadata chunk: empty choices (OpenAI include_usage
		// convention) carrying usage and the x_route42 extension.
		writeSSE(w, flusher, StreamChunk{
			ID: id, Object: "chat.completion.chunk", Created: nowUnix(), Model: t.model,
			Choices: []StreamChoice{},
			Usage: &ChatUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
				TotalTokens:      promptTokens + completionTokens,
			},
			XRoute42: s.buildXRoute42(t, analysis, rankResult, reason, attempts),
		})
		writeSSEDone(w, flusher)
		s.logInteraction(analysis, t, promptTokens, completionTokens, attempts, time.Since(start), "ok", rankResult)
		return

	nextCandidate:
	}

	// No candidate could start a stream.
	s.logInteraction(analysis, chain[len(chain)-1], 0, 0, attempts, time.Since(start), "error", rankResult)
	if lastErr != nil {
		s.logger.Error("stream failed", "err", lastErr, "attempts", attempts+1)
	}
	writeSSE(w, flusher, StreamChunk{
		Object: "chat.completion.chunk", Created: nowUnix(),
		Choices:  []StreamChoice{{Index: 0, Delta: ChatDelta{}, FinishReason: ptrString("error")}},
		XRoute42: &XRoute42{Reason: "error", CandidatesConsidered: len(chain)},
	})
	writeSSEDone(w, flusher)
}

// buildCompleteResponse assembles the non-streaming OpenAI response with
// the x_route42 extension.
func (s *Server) buildCompleteResponse(req ChatRequest, t execTarget, resp *llm.ChatResponse, analysis analyzer.AnalysisResult, rankResult *ranking.RankResult, reason string, attempts int) ChatResponse {
	promptTokens := resp.Usage.PromptTokens
	if promptTokens == 0 {
		promptTokens = estimatePromptTokens(req.Messages)
	}
	return ChatResponse{
		ID:      "chatcmpl-" + randID(),
		Object:  "chat.completion",
		Created: nowUnix(),
		Model:   t.model,
		Choices: []ChatChoice{{
			Index:        0,
			Message:      ChatMessage{Role: "assistant", Content: resp.Text, ToolCalls: resp.ToolCalls},
			FinishReason: finishReason(resp.FinishReason),
		}},
		Usage: &ChatUsage{
			PromptTokens:     promptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      promptTokens + resp.Usage.CompletionTokens,
		},
		XRoute42: s.buildXRoute42(t, analysis, rankResult, reason, attempts),
	}
}

// buildXRoute42 assembles the routing-decision extension. est_cost_cents
// comes from the ranker when available; for a pinned model it is 0.
func (s *Server) buildXRoute42(t execTarget, analysis analyzer.AnalysisResult, rankResult *ranking.RankResult, reason string, attempts int) *XRoute42 {
	x := &XRoute42{
		SelectedModel:    t.model,
		Provider:         t.provider,
		Analyzer:         analysis.Analyzer,
		Complexity:       analysis.Complexity,
		Category:         analysis.Category,
		Signals:          analysis.Signals,
		Reason:           reason,
		FallbackAttempts: attempts,
	}
	if rankResult != nil {
		x.CandidatesConsidered = len(rankResult.Candidates)
		if rankResult.Selected != nil {
			x.EstCostCents = rankResult.Selected.EstCostCents
		}
	} else {
		x.CandidatesConsidered = 1
	}
	return x
}

// logInteraction records one routed completion (or failure) in the store.
func (s *Server) logInteraction(analysis analyzer.AnalysisResult, t execTarget, promptTokens, completionTokens, attempts int, latency time.Duration, status string, rankResult *ranking.RankResult) {
	var cost float64
	if rankResult != nil && rankResult.Selected != nil {
		// Estimate from actual completion tokens when available.
		cost = estimateActualCostCents(rankResult.Selected.Model, promptTokens, completionTokens)
	}
	err := s.store.AddInteraction(store.Interaction{
		Model:            t.model,
		Provider:         t.provider,
		Category:         analysis.Category,
		Complexity:       analysis.Complexity,
		Analyzer:         analysis.Analyzer,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CostCents:        cost,
		LatencyMs:        int(latency.Milliseconds()),
		Status:           status,
		FallbackAttempts: attempts,
	})
	if err != nil {
		s.logger.Warn("log interaction", "err", err)
	}
}

// toLLMMessages converts wire ChatMessages to llm.Message, preserving
// tool_calls and tool_call_id for provider pass-through.
func toLLMMessages(msgs []ChatMessage) []llm.Message {
	out := make([]llm.Message, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, llm.Message{Role: m.Role, Content: m.Content})
	}
	return out
}

// retryable reports whether a provider error is worth a fallback attempt.
func retryable(err error) bool {
	var apiErr *llm.APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable()
	}
	// Network/timeout errors are retryable.
	return true
}

// decodeJSON decodes a JSON body, rejecting obviously large payloads. It
// is non-strict (unknown fields ignored) for client compatibility.
func decodeJSON(r *http.Request, v any) error {
	const maxBody = 8 << 20 // 8MB
	r.Body = http.MaxBytesReader(nil, r.Body, maxBody)
	dec := json.NewDecoder(r.Body)
	// Non-strict: unknown fields are ignored for client compatibility.
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Guard against trailing garbage.
	if dec.More() {
		return fmt.Errorf("unexpected trailing content")
	}
	return nil
}

// writeSSE serializes one chunk as an SSE event and flushes it.
func writeSSE(w io.Writer, flusher http.Flusher, chunk StreamChunk) {
	data, err := json.Marshal(chunk)
	if err != nil {
		slog.Default().Error("marshal sse chunk", "err", err)
		return
	}
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(data)
	_, _ = w.Write([]byte("\n\n"))
	flusher.Flush()
}

// writeSSEDone emits the OpenAI stream terminator. SDKs (openai-python,
// openai-node) rely on it to end iteration cleanly.
func writeSSEDone(w io.Writer, flusher http.Flusher) {
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
	flusher.Flush()
}

func intValue(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

func floatValue(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func ptrString(s string) *string { return &s }

func finishReason(s string) string {
	if s == "" {
		return "stop"
	}
	return s
}

// estimateActualCostCents computes the realized cost from actual token
// counts (vs the ranker's pre-execution estimate).
func estimateActualCostCents(m catalog.ModelInfo, promptTokens, completionTokens int) float64 {
	in := float64(promptTokens) / 1e6 * m.InputPricePerMTok
	out := float64(completionTokens) / 1e6 * m.OutputPricePerMTok
	return (in + out) * 100.0
}

// randID returns a short random id for response/chunk correlation. Kept
// deterministic-friendly (no crypto) since it is only a correlation id.
func randID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
