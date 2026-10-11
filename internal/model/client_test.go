package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestClientRequestsPerTokenServerTimings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["timings_per_token"] != true {
			t.Fatalf("timings_per_token = %#v, want true", body["timings_per_token"])
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"finish_reason":"stop","delta":{}}]}`)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL, Model: "qwen38-27b"}, server.Client())
	if _, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestClientAppliesRequestScopedInferenceOptions(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, body)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"finish_reason":"stop","delta":{"content":"ok"}}]}`)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	client := NewClient(Config{
		BaseURL: server.URL, Model: "qwen38-27b", MaxTokens: 16384,
		ReasoningEffort: "medium", ReasoningBudgetTokens: 4096, EnableThinking: true, PreserveThinking: true,
	}, server.Client())
	stream := func(options InferenceOptions) {
		t.Helper()
		_, err := client.Stream(context.Background(), Request{
			Messages: []Message{{Role: "user", Content: "hi"}}, InferenceOptions: options,
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
	}

	stream(InferenceOptions{})
	disabled := false
	low := "low"
	reasoningLimit := 128
	limit := 512
	stream(InferenceOptions{EnableThinking: &disabled, ReasoningEffort: &low, ReasoningBudgetTokens: &reasoningLimit, MaxTokens: &limit})
	stream(InferenceOptions{})

	if len(requests) != 3 {
		t.Fatalf("request count = %d, want 3", len(requests))
	}
	for _, index := range []int{0, 2} {
		kwargs, ok := requests[index]["chat_template_kwargs"].(map[string]any)
		if !ok {
			t.Fatalf("request %d kwargs = %#v", index, requests[index]["chat_template_kwargs"])
		}
		if kwargs["enable_thinking"] != true || kwargs["preserve_thinking"] != true || requests[index]["reasoning_effort"] != "medium" || requests[index]["thinking_budget_tokens"] != float64(4096) || requests[index]["max_tokens"] != float64(16384) {
			t.Fatalf("request %d did not use configured defaults: %#v", index, requests[index])
		}
	}
	kwargs := requests[1]["chat_template_kwargs"].(map[string]any)
	if kwargs["enable_thinking"] != false || kwargs["preserve_thinking"] != true {
		t.Fatalf("override kwargs = %#v", kwargs)
	}
	if requests[1]["reasoning_effort"] != "low" || requests[1]["thinking_budget_tokens"] != float64(128) || requests[1]["max_tokens"] != float64(512) {
		t.Fatalf("override request = %#v", requests[1])
	}
}

func TestClientStreamsReasoningTextAndToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"reasoning_content":"inspect "}}]}`)
		_, _ = fmt.Fprintln(writer)
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"content":"Checking."}}]}`)
		_, _ = fmt.Fprintln(writer)
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":"}}]}}]}`)
		_, _ = fmt.Fprintln(writer)
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"name":"read_file","arguments":"\"marker.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
		_, _ = fmt.Fprintln(writer)
		_, _ = fmt.Fprintln(writer, `data: {"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150}}`)
		_, _ = fmt.Fprintln(writer)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	var events []Event
	response, err := client.Stream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "read the marker"}},
		Tools:    []Tool{{Name: "read_file", Description: "Read a file", Parameters: map[string]any{"type": "object"}}},
	}, func(event Event) { events = append(events, event) })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	wantEvents := []Event{
		{Kind: EventReasoningDelta, Text: "inspect "},
		{Kind: EventTextDelta, Text: "Checking."},
		{Kind: EventTelemetry, Telemetry: &Telemetry{Usage: &Usage{PromptTokens: 120, CompletionTokens: 30, TotalTokens: 150}}},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	if len(response.ToolCalls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(response.ToolCalls))
	}
	call := response.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "read_file" || call.Arguments != `{"path":"marker.txt"}` {
		t.Fatalf("tool call = %#v", call)
	}
	if response.Usage.PromptTokens != 120 || response.Usage.CompletionTokens != 30 || response.Usage.TotalTokens != 150 {
		t.Fatalf("usage = %#v", response.Usage)
	}
}

func TestClientEmitsUsageWhenStreamReportsZeroUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	var events []Event
	response, err := client.Stream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hello"}},
	}, func(event Event) { events = append(events, event) })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	usage := Usage{}
	wantEvents := []Event{{Kind: EventTelemetry, Telemetry: &Telemetry{Usage: &usage}}}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	if response.Usage != usage {
		t.Fatalf("response usage = %#v, want %#v", response.Usage, usage)
	}
}

func TestClientExposesExactServerTelemetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18},"timings":{"cache_n":0,"prompt_n":11,"prompt_ms":12.5,"prompt_per_second":8.8,"predicted_n":7,"predicted_ms":21.5,"predicted_per_second":2.2}}`)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	var events []Event
	response, err := client.Stream(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hello"}},
	}, func(event Event) { events = append(events, event) })
	if err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	zero, promptTokens, predictedTokens := 0, 11, 7
	promptMS, promptPerSecond := 12.5, 8.8
	predictedMS, predictedPerSecond := 21.5, 2.2
	timings := &Timings{
		CacheTokens:              &zero,
		PromptTokens:             &promptTokens,
		PromptMilliseconds:       &promptMS,
		PromptTokensPerSecond:    &promptPerSecond,
		PredictedTokens:          &predictedTokens,
		PredictedMilliseconds:    &predictedMS,
		PredictedTokensPerSecond: &predictedPerSecond,
	}
	usage := &Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18}
	wantEvents := []Event{{Kind: EventTelemetry, Telemetry: &Telemetry{Usage: usage, Timings: timings}}}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %#v, want %#v", events, wantEvents)
	}
	if !reflect.DeepEqual(response.Timings, timings) {
		t.Fatalf("response timings = %#v, want %#v", response.Timings, timings)
	}
	if response.Usage != *usage {
		t.Fatalf("response usage = %#v, want %#v", response.Usage, *usage)
	}
}

func TestClientClosesStreamingConnectionAfterEachRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !request.Close {
			t.Error("streaming request allowed connection reuse")
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"content":"ok"}}]}`)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	for range 2 {
		response, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}}, nil)
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		if response.Text != "ok" {
			t.Fatalf("response text = %q, want ok", response.Text)
		}
	}
}

func TestClientStreamCancellationStopsBlockedRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	done := make(chan error, 1)
	go func() {
		_, err := client.Stream(ctx, Request{Messages: []Message{{Role: "user", Content: "wait"}}}, nil)
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Stream() error = %v, want context.Canceled", err)
	}
	close(release)
}

func TestClientRejectsStreamWithoutDoneSentinel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"content":"partial"}}]}`)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	_, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "without [DONE]") {
		t.Fatalf("Stream() error = %v, want missing [DONE] error", err)
	}
}

func TestClientRejectsLengthTruncatedStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"content":"partial"},"finish_reason":"length"}]}`)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	_, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "token limit") {
		t.Fatalf("Stream() error = %v, want token limit error", err)
	}
}

func TestClientRejectsInvalidToolCallMetadata(t *testing.T) {
	tests := []struct {
		name  string
		event string
		want  string
	}{
		{name: "missing ID", event: `{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, want: "empty ID"},
		{name: "missing name", event: `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","function":{"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, want: "empty name"},
		{name: "duplicate ID", event: `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"same","function":{"name":"read_file","arguments":"{}"}},{"index":1,"id":"same","function":{"name":"read_file","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, want: "duplicate tool call ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprintln(writer, "data: "+test.event)
				_, _ = fmt.Fprintln(writer, "data: [DONE]")
			}))
			defer server.Close()
			client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
			_, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hello"}}}, nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Stream() error = %v, want %q", err, test.want)
			}
		})
	}
}
