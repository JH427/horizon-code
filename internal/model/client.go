package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type Config struct {
	BaseURL          string
	Model            string
	Temperature      float64
	TopP             float64
	TopK             int
	MaxTokens        int
	ReasoningEffort  string
	EnableThinking   bool
	PreserveThinking bool
}

type Client struct {
	config      Config
	client      *http.Client
	contextSize int
}

type Message struct {
	Role             string         `json:"role"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCallWire `json:"tool_calls,omitempty"`
	ToolCallID       string         `json:"tool_call_id,omitempty"`
}

type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type ToolCallWire struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionWire `json:"function"`
}

type FunctionWire struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Request struct {
	Messages []Message
	Tools    []Tool
}

type Response struct {
	Text      string
	Reasoning string
	ToolCalls []ToolCall
	Usage     Usage
	Timings   *Timings
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Timings contains exact performance counters reported by llama.cpp. Pointer
// fields distinguish an omitted counter from a counter reported as zero.
type Timings struct {
	CacheTokens              *int     `json:"cache_n"`
	PromptTokens             *int     `json:"prompt_n"`
	PromptMilliseconds       *float64 `json:"prompt_ms"`
	PromptTokensPerSecond    *float64 `json:"prompt_per_second"`
	PredictedTokens          *int     `json:"predicted_n"`
	PredictedMilliseconds    *float64 `json:"predicted_ms"`
	PredictedTokensPerSecond *float64 `json:"predicted_per_second"`
}

type Telemetry struct {
	Usage   *Usage
	Timings *Timings
}

type EventKind string

const (
	EventReasoningDelta EventKind = "reasoning_delta"
	EventTextDelta      EventKind = "text_delta"
	EventTelemetry      EventKind = "telemetry"
)

type Event struct {
	Kind      EventKind
	Text      string
	Telemetry *Telemetry
}

type chatRequest struct {
	Model              string          `json:"model"`
	Messages           []Message       `json:"messages"`
	Tools              []toolWire      `json:"tools,omitempty"`
	Stream             bool            `json:"stream"`
	StreamOptions      streamOptions   `json:"stream_options"`
	TimingsPerToken    bool            `json:"timings_per_token"`
	Temperature        float64         `json:"temperature,omitempty"`
	TopP               float64         `json:"top_p,omitempty"`
	TopK               int             `json:"top_k,omitempty"`
	MaxTokens          int             `json:"max_tokens,omitempty"`
	ReasoningEffort    string          `json:"reasoning_effort,omitempty"`
	ChatTemplateKwargs map[string]bool `json:"chat_template_kwargs,omitempty"`
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type toolWire struct {
	Type     string           `json:"type"`
	Function functionToolWire `json:"function"`
}

type functionToolWire struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type streamChunk struct {
	Usage   *Usage   `json:"usage"`
	Timings *Timings `json:"timings"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

type modelsResponse struct {
	Data []struct {
		ID   string `json:"id"`
		Meta struct {
			ContextSize int `json:"n_ctx"`
		} `json:"meta"`
	} `json:"data"`
}

func NewClient(config Config, client *http.Client) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{config: config, client: client}
}

// ContextSize returns the configured model's context size reported by the
// latest successful Verify call, or zero when the endpoint omitted it.
func (client *Client) ContextSize() int {
	return client.contextSize
}

func (client *Client) Verify(ctx context.Context) error {
	url := strings.TrimRight(client.config.BaseURL, "/") + "/models"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("create model discovery request: %w", err)
	}
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("discover served models: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, readErr := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		if readErr != nil {
			return fmt.Errorf("model discovery returned %s and error body could not be read: %w", response.Status, readErr)
		}
		return fmt.Errorf("model discovery returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var models modelsResponse
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	if err := decoder.Decode(&models); err != nil {
		return fmt.Errorf("decode model discovery response: %w", err)
	}
	var served []string
	for _, discovered := range models.Data {
		served = append(served, discovered.ID)
		if discovered.ID == client.config.Model {
			client.contextSize = discovered.Meta.ContextSize
			return nil
		}
	}
	return fmt.Errorf("configured model %q is not served; endpoint reported %v", client.config.Model, served)
}

func (client *Client) Stream(ctx context.Context, request Request, emit func(Event)) (Response, error) {
	body := chatRequest{
		Model:           client.config.Model,
		Messages:        request.Messages,
		Stream:          true,
		StreamOptions:   streamOptions{IncludeUsage: true},
		TimingsPerToken: true,
		Temperature:     client.config.Temperature,
		TopP:            client.config.TopP,
		TopK:            client.config.TopK,
		MaxTokens:       client.config.MaxTokens,
		ReasoningEffort: client.config.ReasoningEffort,
		ChatTemplateKwargs: map[string]bool{
			"enable_thinking":   client.config.EnableThinking,
			"preserve_thinking": client.config.PreserveThinking,
		},
	}
	for _, tool := range request.Tools {
		body.Tools = append(body.Tools, toolWire{
			Type: "function",
			Function: functionToolWire{
				Name: tool.Name, Description: tool.Description, Parameters: tool.Parameters,
			},
		})
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return Response{}, fmt.Errorf("encode chat request: %w", err)
	}
	url := strings.TrimRight(client.config.BaseURL, "/") + "/chat/completions"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(encoded))
	if err != nil {
		return Response{}, fmt.Errorf("create chat request: %w", err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	// llama-server can close an idle keep-alive socket between multi-step tool
	// requests. Do not reuse streaming connections, avoiding a later POST on a
	// stale socket after earlier tool rounds have completed successfully.
	httpRequest.Close = true
	httpResponse, err := client.client.Do(httpRequest)
	if err != nil {
		return Response{}, fmt.Errorf("send chat request: %w", err)
	}
	defer httpResponse.Body.Close()
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		message, readErr := io.ReadAll(io.LimitReader(httpResponse.Body, 64<<10))
		if readErr != nil {
			return Response{}, fmt.Errorf("chat request returned %s and error body could not be read: %w", httpResponse.Status, readErr)
		}
		return Response{}, fmt.Errorf("chat request returned %s: %s", httpResponse.Status, strings.TrimSpace(string(message)))
	}

	var result Response
	calls := make(map[int]*ToolCall)
	doneSeen := false
	finishReason := ""
	scanner := bufio.NewScanner(httpResponse.Body)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			doneSeen = true
			continue
		}
		var chunk streamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return Response{}, fmt.Errorf("decode chat stream event: %w", err)
		}
		if chunk.Usage != nil {
			result.Usage = *chunk.Usage
		}
		if chunk.Timings != nil {
			result.Timings = chunk.Timings
		}
		if emit != nil && (chunk.Usage != nil || chunk.Timings != nil) {
			emit(Event{Kind: EventTelemetry, Telemetry: &Telemetry{
				Usage: chunk.Usage, Timings: chunk.Timings,
			}})
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			reasoning := choice.Delta.ReasoningContent
			if reasoning == "" {
				reasoning = choice.Delta.Reasoning
			}
			if reasoning != "" {
				result.Reasoning += reasoning
				if emit != nil {
					emit(Event{Kind: EventReasoningDelta, Text: reasoning})
				}
			}
			if choice.Delta.Content != "" {
				result.Text += choice.Delta.Content
				if emit != nil {
					emit(Event{Kind: EventTextDelta, Text: choice.Delta.Content})
				}
			}
			for _, delta := range choice.Delta.ToolCalls {
				call, ok := calls[delta.Index]
				if !ok {
					call = &ToolCall{}
					calls[delta.Index] = call
				}
				call.ID = mergeStreamMetadata(call.ID, delta.ID)
				call.Name = mergeStreamMetadata(call.Name, delta.Function.Name)
				call.Arguments += delta.Function.Arguments
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Response{}, fmt.Errorf("read chat stream: %w", err)
	}
	if !doneSeen {
		return Response{}, errors.New("chat stream ended without [DONE] sentinel")
	}
	switch finishReason {
	case "", "stop", "tool_calls":
	case "length":
		return Response{}, errors.New("chat stream reached the configured token limit before completion")
	case "content_filter":
		return Response{}, errors.New("chat stream was stopped by content filtering")
	default:
		return Response{}, fmt.Errorf("chat stream ended with unsupported finish reason %q", finishReason)
	}
	seenCallIDs := make(map[string]struct{}, len(calls))
	for index := 0; index < len(calls); index++ {
		call, ok := calls[index]
		if !ok {
			return Response{}, errors.New("chat stream returned non-contiguous tool call indexes")
		}
		if call.ID == "" {
			return Response{}, fmt.Errorf("chat stream tool call %d has empty ID", index)
		}
		if call.Name == "" {
			return Response{}, fmt.Errorf("chat stream tool call %d has empty name", index)
		}
		if _, exists := seenCallIDs[call.ID]; exists {
			return Response{}, fmt.Errorf("chat stream returned duplicate tool call ID %q", call.ID)
		}
		seenCallIDs[call.ID] = struct{}{}
		result.ToolCalls = append(result.ToolCalls, *call)
	}
	return result, nil
}

func mergeStreamMetadata(current, delta string) string {
	if delta == "" || delta == current || strings.HasPrefix(current, delta) {
		return current
	}
	if current == "" || strings.HasPrefix(delta, current) {
		return delta
	}
	return current + delta
}
