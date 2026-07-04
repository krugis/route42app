package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const geminiDefaultBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// geminiProvider adapts the Google Generative Language API.
type geminiProvider struct {
	baseURL string
	key     func() string
	client  *http.Client
}

func newGemini(baseURL string, key func() string, client *http.Client) *geminiProvider {
	if baseURL == "" {
		baseURL = geminiDefaultBaseURL
	}
	return &geminiProvider{baseURL: strings.TrimRight(baseURL, "/"), key: key, client: client}
}

func (p *geminiProvider) Name() string { return "gemini" }

type geminiContent struct {
	Role  string `json:"role,omitempty"`
	Parts []struct {
		Text    string `json:"text"`
		Thought bool   `json:"thought,omitempty"`
	} `json:"parts"`
}

func (p *geminiProvider) buildRequest(ctx context.Context, req ChatRequest, stream bool) (*http.Request, error) {
	apiKey := p.key()
	if apiKey == "" {
		return nil, fmt.Errorf(`no api key configured for provider "gemini"`)
	}

	// Map OpenAI-style messages to Gemini contents. System prompts go to
	// systemInstruction; assistant becomes "model".
	var systemParts []map[string]string
	contents := make([]map[string]any, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			systemParts = append(systemParts, map[string]string{"text": m.Content})
		case "assistant":
			contents = append(contents, map[string]any{
				"role":  "model",
				"parts": []map[string]string{{"text": m.Content}},
			})
		default:
			contents = append(contents, map[string]any{
				"role":  "user",
				"parts": []map[string]string{{"text": m.Content}},
			})
		}
	}

	body := map[string]any{"contents": contents}
	if len(systemParts) > 0 {
		body["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	genCfg := map[string]any{}
	if req.MaxTokens > 0 {
		genCfg["maxOutputTokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		genCfg["temperature"] = req.Temperature
	}
	if len(genCfg) > 0 {
		body["generationConfig"] = genCfg
	}

	method := "generateContent"
	query := ""
	if stream {
		method = "streamGenerateContent"
		query = "&alt=sse"
	}
	endpoint := fmt.Sprintf("%s/models/%s:%s?key=%s%s",
		p.baseURL, url.PathEscape(req.Model), method, url.QueryEscape(apiKey), query)

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

type geminiResponse struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func (r *geminiResponse) textAndReasoning() (text, reasoning string) {
	if len(r.Candidates) == 0 {
		return "", ""
	}
	var t, re strings.Builder
	for _, part := range r.Candidates[0].Content.Parts {
		if part.Thought {
			re.WriteString(part.Text)
		} else {
			t.WriteString(part.Text)
		}
	}
	return t.String(), re.String()
}

func (p *geminiProvider) Complete(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	httpReq, err := p.buildRequest(ctx, req, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{Provider: "gemini", StatusCode: resp.StatusCode, Body: string(body)}
	}

	var out geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("gemini response decode: %w", err)
	}
	text, reasoning := out.textAndReasoning()
	if text == "" {
		return nil, fmt.Errorf("gemini: response missing candidates")
	}
	finish := ""
	if len(out.Candidates) > 0 {
		finish = strings.ToLower(out.Candidates[0].FinishReason)
	}
	return &ChatResponse{
		Text:         text,
		Reasoning:    reasoning,
		FinishReason: finish,
		Usage: Usage{
			PromptTokens:     out.UsageMetadata.PromptTokenCount,
			CompletionTokens: out.UsageMetadata.CandidatesTokenCount,
		},
	}, nil
}

func (p *geminiProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	httpReq, err := p.buildRequest(ctx, req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini request: %w", err)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &APIError{Provider: "gemini", StatusCode: resp.StatusCode, Body: string(body)}
	}

	ch := make(chan Chunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		var usage Usage
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))

			var event geminiResponse
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}
			if event.UsageMetadata.PromptTokenCount > 0 {
				usage.PromptTokens = event.UsageMetadata.PromptTokenCount
			}
			if event.UsageMetadata.CandidatesTokenCount > 0 {
				usage.CompletionTokens = event.UsageMetadata.CandidatesTokenCount
			}
			text, reasoning := event.textAndReasoning()
			if text != "" || reasoning != "" {
				select {
				case ch <- Chunk{Delta: text, ReasoningDelta: reasoning}:
				case <-ctx.Done():
					ch <- Chunk{Err: ctx.Err()}
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Chunk{Err: fmt.Errorf("gemini stream: %w", err)}
			return
		}
		ch <- Chunk{Done: true, Usage: usage}
	}()
	return ch, nil
}
