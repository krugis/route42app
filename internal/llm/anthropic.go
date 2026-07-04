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
)

const (
	anthropicDefaultBaseURL = "https://api.anthropic.com/v1"
	anthropicVersion        = "2023-06-01"
	// Anthropic requires max_tokens; used when the request doesn't set one.
	anthropicDefaultMaxTokens = 4096
)

// anthropicProvider adapts the Anthropic Messages API.
type anthropicProvider struct {
	baseURL string
	key     func() string
	client  *http.Client
}

func newAnthropic(baseURL string, key func() string, client *http.Client) *anthropicProvider {
	if baseURL == "" {
		baseURL = anthropicDefaultBaseURL
	}
	return &anthropicProvider{baseURL: strings.TrimRight(baseURL, "/"), key: key, client: client}
}

func (p *anthropicProvider) Name() string { return "anthropic" }

func (p *anthropicProvider) buildRequest(ctx context.Context, req ChatRequest, stream bool) (*http.Request, error) {
	apiKey := p.key()
	if apiKey == "" {
		return nil, fmt.Errorf(`no api key configured for provider "anthropic"`)
	}

	// The Messages API takes system prompts as a top-level field.
	var system string
	messages := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "system" {
			if system != "" {
				system += "\n\n"
			}
			system += m.Content
			continue
		}
		messages = append(messages, m)
	}

	body := map[string]any{
		"model":      req.Model,
		"messages":   messages,
		"max_tokens": anthropicDefaultMaxTokens,
		"stream":     stream,
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature > 0 {
		body["temperature"] = req.Temperature
	}
	if system != "" {
		body["system"] = system
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/messages", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)
	httpReq.Header.Set("Content-Type", "application/json")
	return httpReq, nil
}

func (p *anthropicProvider) Complete(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	httpReq, err := p.buildRequest(ctx, req, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{Provider: "anthropic", StatusCode: resp.StatusCode, Body: string(body)}
	}

	var out struct {
		Content []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			Thinking string `json:"thinking"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("anthropic response decode: %w", err)
	}

	var text, reasoning strings.Builder
	for _, block := range out.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		}
	}
	if text.Len() == 0 {
		return nil, fmt.Errorf("anthropic: no text content in response")
	}
	return &ChatResponse{
		Text:         text.String(),
		Reasoning:    reasoning.String(),
		FinishReason: out.StopReason,
		Usage: Usage{
			PromptTokens:     out.Usage.InputTokens,
			CompletionTokens: out.Usage.OutputTokens,
		},
	}, nil
}

func (p *anthropicProvider) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	httpReq, err := p.buildRequest(ctx, req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("anthropic request: %w", err)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &APIError{Provider: "anthropic", StatusCode: resp.StatusCode, Body: string(body)}
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

			var event struct {
				Type  string `json:"type"`
				Delta struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					Thinking string `json:"thinking"`
				} `json:"delta"`
				Message struct {
					Usage struct {
						InputTokens int `json:"input_tokens"`
					} `json:"usage"`
				} `json:"message"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue
			}

			switch event.Type {
			case "message_start":
				usage.PromptTokens = event.Message.Usage.InputTokens
			case "content_block_delta":
				chunk := Chunk{}
				switch event.Delta.Type {
				case "text_delta":
					chunk.Delta = event.Delta.Text
				case "thinking_delta":
					chunk.ReasoningDelta = event.Delta.Thinking
				default:
					continue
				}
				select {
				case ch <- chunk:
				case <-ctx.Done():
					ch <- Chunk{Err: ctx.Err()}
					return
				}
			case "message_delta":
				usage.CompletionTokens = event.Usage.OutputTokens
			case "message_stop":
				ch <- Chunk{Done: true, Usage: usage}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Chunk{Err: fmt.Errorf("anthropic stream: %w", err)}
			return
		}
		ch <- Chunk{Done: true, Usage: usage}
	}()
	return ch, nil
}
