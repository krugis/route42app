package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ollamaProvider adapts the local Ollama chat API. Local models cost
// nothing and need no API key.
type ollamaProvider struct {
	baseURL string
	client  *http.Client
}

func newOllama(baseURL string, client *http.Client) *ollamaProvider {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	return &ollamaProvider{baseURL: strings.TrimRight(baseURL, "/"), client: client}
}

func (p *ollamaProvider) Name() string { return "ollama" }

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []Message       `json:"messages"`
	Stream   bool            `json:"stream"`
	Tools    json.RawMessage `json:"tools,omitempty"`
	Options  map[string]any  `json:"options,omitempty"`
}

// ollamaChatEvent is one response object: the whole reply when
// stream=false, or one NDJSON line when streaming.
type ollamaChatEvent struct {
	Message struct {
		Content   string          `json:"content"`
		Thinking  string          `json:"thinking"`
		ToolCalls json.RawMessage `json:"tool_calls"`
	} `json:"message"`
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
}

func (p *ollamaProvider) buildRequest(ctx context.Context, req ChatRequest, stream bool) (*http.Request, error) {
	options := map[string]any{}
	if req.MaxTokens > 0 {
		options["num_predict"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		options["temperature"] = req.Temperature
	}
	body := ollamaChatRequest{
		Model:    req.Model,
		Messages: req.Messages,
		Stream:   stream,
		Tools:    req.Tools,
	}
	if len(options) > 0 {
		body.Options = options
	}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/api/chat", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

func (p *ollamaProvider) Complete(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	httpReq, err := p.buildRequest(ctx, req, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{Provider: "ollama", StatusCode: resp.StatusCode, Body: string(body)}
	}

	var out ollamaChatEvent
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama response decode: %w", err)
	}
	if out.Message.Content == "" && len(out.Message.ToolCalls) == 0 {
		return nil, fmt.Errorf("ollama: empty content in response")
	}
	return &ChatResponse{
		Text:         out.Message.Content,
		Reasoning:    out.Message.Thinking,
		ToolCalls:    out.Message.ToolCalls,
		FinishReason: out.DoneReason,
		Usage: Usage{
			PromptTokens:     out.PromptEvalCount,
			CompletionTokens: out.EvalCount,
		},
	}, nil
}

func (p *ollamaProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	httpReq, err := p.buildRequest(ctx, req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ollama request: %w", err)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &APIError{Provider: "ollama", StatusCode: resp.StatusCode, Body: string(body)}
	}

	ch := make(chan Chunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		// Ollama streams newline-delimited JSON, not SSE.
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				continue
			}
			var event ollamaChatEvent
			if err := json.Unmarshal([]byte(line), &event); err != nil {
				continue
			}
			if event.Done {
				ch <- Chunk{Done: true, Usage: Usage{
					PromptTokens:     event.PromptEvalCount,
					CompletionTokens: event.EvalCount,
				}}
				return
			}
			if event.Message.Content != "" || event.Message.Thinking != "" {
				select {
				case ch <- Chunk{Delta: event.Message.Content, ReasoningDelta: event.Message.Thinking}:
				case <-ctx.Done():
					ch <- Chunk{Err: ctx.Err()}
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Chunk{Err: fmt.Errorf("ollama stream: %w", err)}
			return
		}
		ch <- Chunk{Done: true}
	}()
	return ch, nil
}

// LocalModel is a model discovered on the local Ollama instance.
type LocalModel struct {
	Name       string `json:"name"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	Family     string `json:"family,omitempty"`
	Parameters string `json:"parameters,omitempty"` // e.g. "7.6B"
	Quant      string `json:"quantization,omitempty"`
	Running    bool   `json:"running"`
}

// DiscoverOllama lists installed models via the Ollama HTTP API
// (/api/tags), marking currently loaded ones via /api/ps. A missing or
// unreachable Ollama yields an error the caller should treat as
// "no local models", never as fatal.
func DiscoverOllama(ctx context.Context, baseURL string) ([]LocalModel, error) {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = "http://localhost:11434"
	}
	client := &http.Client{Timeout: 5 * time.Second}

	var tags struct {
		Models []struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Details struct {
				Family            string `json:"family"`
				ParameterSize     string `json:"parameter_size"`
				QuantizationLevel string `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := getJSON(ctx, client, base+"/api/tags", &tags); err != nil {
		return nil, fmt.Errorf("ollama discovery: %w", err)
	}

	running := map[string]bool{}
	var ps struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := getJSON(ctx, client, base+"/api/ps", &ps); err == nil {
		for _, m := range ps.Models {
			running[m.Name] = true
			if idx := strings.Index(m.Name, ":"); idx > 0 {
				running[m.Name[:idx]] = true
			}
		}
	}

	models := make([]LocalModel, 0, len(tags.Models))
	for _, m := range tags.Models {
		family := m.Details.Family
		if family == "" {
			family = m.Name
			if idx := strings.Index(family, ":"); idx > 0 {
				family = family[:idx]
			}
		}
		isRunning := running[m.Name]
		if !isRunning {
			if idx := strings.Index(m.Name, ":"); idx > 0 {
				isRunning = running[m.Name[:idx]]
			}
		}
		models = append(models, LocalModel{
			Name:       m.Name,
			SizeBytes:  m.Size,
			Family:     family,
			Parameters: m.Details.ParameterSize,
			Quant:      m.Details.QuantizationLevel,
			Running:    isRunning,
		})
	}
	return models, nil
}

func getJSON(ctx context.Context, client *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
