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

// openAICompat serves every provider that speaks the OpenAI chat
// completions dialect (OpenAI, Groq, Mistral, DeepSeek, Moonshot,
// Alibaba, NVIDIA, OpenRouter, Z.ai) — one adapter, parameterized by
// base URL and provider quirks.
type openAICompat struct {
	name    string
	baseURL string
	key     func() string
	client  *http.Client
}

func newOpenAICompat(name, baseURL string, key func() string, client *http.Client) *openAICompat {
	return &openAICompat{name: name, baseURL: strings.TrimRight(baseURL, "/"), key: key, client: client}
}

func (p *openAICompat) Name() string { return p.name }

type openAIChatRequest struct {
	Model               string          `json:"model"`
	Messages            []Message       `json:"messages"`
	MaxTokens           int             `json:"max_tokens,omitempty"`
	MaxCompletionTokens int             `json:"max_completion_tokens,omitempty"`
	Temperature         float64         `json:"temperature,omitempty"`
	Tools               json.RawMessage `json:"tools,omitempty"`
	Stream              bool            `json:"stream"`
	StreamOptions       json.RawMessage `json:"stream_options,omitempty"`
}

func (p *openAICompat) buildRequest(ctx context.Context, req ChatRequest, stream bool) (*http.Request, error) {
	apiKey := p.key()
	if apiKey == "" {
		return nil, fmt.Errorf("no api key configured for provider %q", p.name)
	}

	model := req.Model
	if p.name == "openrouter" {
		// OpenRouter rejects the :free suffix used in catalog ids.
		model = strings.TrimSuffix(model, ":free")
	}

	body := openAIChatRequest{
		Model:       model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		Tools:       req.Tools,
		Stream:      stream,
	}
	if req.MaxTokens > 0 {
		// Newer OpenAI model families reject max_tokens.
		if p.name == "openai" && usesMaxCompletionTokens(model) {
			body.MaxCompletionTokens = req.MaxTokens
		} else {
			body.MaxTokens = req.MaxTokens
		}
	}
	if stream {
		body.StreamOptions = json.RawMessage(`{"include_usage":true}`)
	}

	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if p.name == "openrouter" {
		httpReq.Header.Set("HTTP-Referer", "https://github.com/krugis/route42app")
		httpReq.Header.Set("X-Title", "Route42")
	}
	return httpReq, nil
}

// usesMaxCompletionTokens reports whether an OpenAI model requires the
// max_completion_tokens field (o-series and gpt-5 family).
func usesMaxCompletionTokens(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	return strings.HasPrefix(m, "o") || strings.Contains(m, "gpt-5")
}

func (p *openAICompat) Complete(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	httpReq, err := p.buildRequest(ctx, req, false)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &APIError{Provider: p.name, StatusCode: resp.StatusCode, Body: string(body)}
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
				// Providers disagree on the reasoning field name.
				Reasoning        string          `json:"reasoning"`
				ReasoningContent string          `json:"reasoning_content"`
				ToolCalls        json.RawMessage `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("%s response decode: %w", p.name, err)
	}
	if len(out.Choices) == 0 {
		return nil, fmt.Errorf("%s: no choices in response", p.name)
	}

	choice := out.Choices[0]
	text, blockReasoning := decodeContent(choice.Message.Content)
	reasoning := choice.Message.ReasoningContent
	if reasoning == "" {
		reasoning = choice.Message.Reasoning
	}
	if reasoning == "" {
		reasoning = blockReasoning
	}
	if text == "" && len(choice.Message.ToolCalls) == 0 {
		// Some reasoning models put everything in the reasoning field.
		if reasoning == "" {
			return nil, fmt.Errorf("%s: empty content in response", p.name)
		}
		text = reasoning
	}

	return &ChatResponse{
		Text:         text,
		Reasoning:    reasoning,
		ToolCalls:    choice.Message.ToolCalls,
		FinishReason: choice.FinishReason,
		Usage:        out.Usage,
	}, nil
}

// decodeContent handles both content shapes: a plain string, and the
// block-array form used by Mistral reasoning models
// ([{"type":"text","text":...},{"type":"thinking","thinking":[...]}]).
func decodeContent(raw json.RawMessage) (text, reasoning string) {
	if len(raw) == 0 {
		return "", ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, ""
	}
	var blocks []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking []struct {
			Text string `json:"text"`
		} `json:"thinking"`
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return "", ""
	}
	var textB, reasonB strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "text":
			textB.WriteString(b.Text)
		case "thinking":
			for _, t := range b.Thinking {
				reasonB.WriteString(t.Text)
			}
		}
	}
	return textB.String(), reasonB.String()
}

func (p *openAICompat) Stream(ctx context.Context, req ChatRequest) (<-chan Chunk, error) {
	httpReq, err := p.buildRequest(ctx, req, true)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", p.name, err)
	}
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		return nil, &APIError{Provider: p.name, StatusCode: resp.StatusCode, Body: string(body)}
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
			if payload == "[DONE]" {
				ch <- Chunk{Done: true, Usage: usage}
				return
			}

			var event struct {
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						Reasoning        string `json:"reasoning"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
				} `json:"choices"`
				Usage *Usage `json:"usage"`
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				continue // tolerate unknown SSE events
			}
			if event.Usage != nil {
				usage = *event.Usage
			}
			if len(event.Choices) == 0 {
				continue
			}
			delta := event.Choices[0].Delta
			reasoning := delta.ReasoningContent
			if reasoning == "" {
				reasoning = delta.Reasoning
			}
			if delta.Content != "" || reasoning != "" {
				select {
				case ch <- Chunk{Delta: delta.Content, ReasoningDelta: reasoning}:
				case <-ctx.Done():
					ch <- Chunk{Err: ctx.Err()}
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- Chunk{Err: fmt.Errorf("%s stream: %w", p.name, err)}
			return
		}
		// Stream ended without [DONE]; treat as completed.
		ch <- Chunk{Done: true, Usage: usage}
	}()
	return ch, nil
}
