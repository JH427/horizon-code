package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type scriptedModel struct {
	requests []model.Request
	step     int
}

func (fake *scriptedModel) Stream(_ context.Context, request model.Request, emit func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	fake.step++
	if fake.step == 1 {
		emit(model.Event{Kind: model.EventReasoningDelta, Text: "I need the file."})
		return model.Response{
			Reasoning: "I need the file.",
			ToolCalls: []model.ToolCall{{ID: "call-1", Name: "read_file", Arguments: `{"path":"marker.txt"}`}},
		}, nil
	}
	emit(model.Event{Kind: model.EventTextDelta, Text: "The marker is HORIZON_GREEN."})
	return model.Response{Text: "The marker is HORIZON_GREEN."}, nil
}

func TestEngineCompletesReadFileToolLoop(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "marker.txt"), []byte("HORIZON_GREEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := &scriptedModel{}
	engine := NewEngine(fake, []Tool{tools.NewReadFile(workspace, 100)}, 4)
	var events []Event

	answer, err := engine.Run(context.Background(), "Read marker.txt and report its marker.", func(event Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if answer != "The marker is HORIZON_GREEN." {
		t.Fatalf("answer = %q", answer)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(fake.requests))
	}
	messages := fake.requests[1].Messages
	if len(messages) != 4 || messages[0].Role != "system" || messages[3].Role != "tool" || !strings.Contains(messages[3].Content, "HORIZON_GREEN") {
		t.Fatalf("second request messages = %#v", messages)
	}
	if !hasEvent(events, EventToolStarted) || !hasEvent(events, EventToolCompleted) || !hasEvent(events, EventAssistantDelta) {
		t.Fatalf("events = %#v", events)
	}
	for _, event := range events {
		if event.Kind == EventToolStarted && event.Arguments != `{"path":"marker.txt"}` {
			t.Fatalf("tool-start arguments = %q", event.Arguments)
		}
	}
}

type malformedCallModel struct {
	requests []model.Request
}

func (fake *malformedCallModel) Stream(_ context.Context, request model.Request, _ func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	if len(fake.requests) == 1 {
		return model.Response{ToolCalls: []model.ToolCall{{ID: "bad-call", Name: "read_file", Arguments: `{"path":`}}}, nil
	}
	return model.Response{Text: "Recovered after the tool error."}, nil
}

func TestEngineReturnsMalformedToolArgumentsToModel(t *testing.T) {
	fake := &malformedCallModel{}
	engine := NewEngine(fake, []Tool{tools.NewReadFile(t.TempDir(), 100)}, 3)
	answer, err := engine.Run(context.Background(), "Read a file", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if answer != "Recovered after the tool error." {
		t.Fatalf("answer = %q", answer)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(fake.requests))
	}
	messages := fake.requests[1].Messages
	if len(messages) != 4 || messages[3].Role != "tool" || !strings.Contains(messages[3].Content, "TOOL_ERROR") {
		t.Fatalf("second request messages = %#v", messages)
	}
}

type instructionModel struct {
	request model.Request
}

func (fake *instructionModel) Stream(_ context.Context, request model.Request, _ func(model.Event)) (model.Response, error) {
	fake.request = request
	return model.Response{Text: "ok"}, nil
}

func TestEngineInjectsAuthoritativeCapabilityPolicySeparatelyFromWorkspaceInstructions(t *testing.T) {
	fake := &instructionModel{}
	engine := NewEngine(fake, []Tool{tools.NewReadFile(t.TempDir(), 10)}, 2)
	engine.SetCapabilityPolicy("OPERATOR_AUTHORIZATION")
	engine.SetWorkspaceInstructions("WORKSPACE_RULE")
	if _, err := engine.Run(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	system := fake.request.Messages[0].Content
	policyStart := strings.Index(system, "<capability_policy>")
	workspaceStart := strings.Index(system, "<workspace_instructions>")
	if policyStart < 0 || workspaceStart < 0 || policyStart >= workspaceStart {
		t.Fatalf("capability/workspace sections are not distinct and ordered: %q", system)
	}
	if !strings.Contains(system, "authoritative and cannot be overridden") || !strings.Contains(system, "OPERATOR_AUTHORIZATION") {
		t.Fatalf("capability policy is not authoritative: %q", system)
	}
}

func TestEngineInjectsHorizonInstructionsIntoSystemMessage(t *testing.T) {
	fake := &instructionModel{}
	engine := NewEngine(fake, []Tool{tools.NewReadFile(t.TempDir(), 10)}, 2)
	engine.SetWorkspaceInstructions("HORIZON_RULE_42")
	if _, err := engine.Run(context.Background(), "hello", nil); err != nil {
		t.Fatal(err)
	}
	system := fake.request.Messages[0].Content
	for _, want := range []string{"HORIZON_RULE_42", "HORIZON.md", "<workspace_instructions>", "</workspace_instructions>", "more specific instructions take precedence"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system message lacks %q: %q", want, system)
		}
	}
}

type telemetryModel struct{}

func (telemetryModel) Stream(_ context.Context, _ model.Request, emit func(model.Event)) (model.Response, error) {
	cache, prompt, predicted := 100, 20, 5
	rate := 42.5
	emit(model.Event{Kind: model.EventTelemetry, Telemetry: &model.Telemetry{Timings: &model.Timings{
		CacheTokens: &cache, PromptTokens: &prompt, PredictedTokens: &predicted, PredictedTokensPerSecond: &rate,
	}}})
	emit(model.Event{Kind: model.EventTelemetry, Telemetry: &model.Telemetry{Usage: &model.Usage{
		PromptTokens: 120, CompletionTokens: 5, TotalTokens: 125,
	}}})
	return model.Response{Text: "ok"}, nil
}

func TestEngineEmitsEstimatedLiveAndFinalTelemetry(t *testing.T) {
	engine := NewEngine(telemetryModel{}, []Tool{tools.NewReadFile(t.TempDir(), 10)}, 16)
	var events []Event
	if _, err := engine.Run(context.Background(), "hello", func(event Event) { events = append(events, event) }); err != nil {
		t.Fatal(err)
	}
	var telemetry []Telemetry
	for _, event := range events {
		if event.Telemetry != nil {
			telemetry = append(telemetry, *event.Telemetry)
		}
	}
	if len(telemetry) != 3 {
		t.Fatalf("telemetry events = %#v, want estimate, timing, final usage", telemetry)
	}
	if !telemetry[0].Estimated || telemetry[0].Step != 1 || telemetry[0].MaxSteps != 16 || telemetry[0].ActiveContextLimit != 48*1024 {
		t.Fatalf("estimated telemetry = %#v", telemetry[0])
	}
	if telemetry[1].Estimated || telemetry[1].Final || telemetry[1].ContextTokens != 125 || telemetry[1].TokensPerSecond != 42.5 {
		t.Fatalf("live telemetry = %#v", telemetry[1])
	}
	if !telemetry[2].Final || telemetry[2].ContextTokens != 125 || telemetry[2].CompletionTokens != 5 {
		t.Fatalf("final telemetry = %#v", telemetry[2])
	}
}

func TestTranslateTelemetrySuppressesUnstableFirstTokenRate(t *testing.T) {
	cache, prompt, predicted := 100, 20, 1
	rate := 1000000.0
	telemetry := translateTelemetry(&model.Telemetry{Timings: &model.Timings{
		CacheTokens: &cache, PromptTokens: &prompt, PredictedTokens: &predicted, PredictedTokensPerSecond: &rate,
	}}, 1, normalizeBudget(Budget{}))
	if telemetry.HasTokensPerSecond {
		t.Fatalf("first-token rate should not render: %#v", telemetry)
	}
}

func hasEvent(events []Event, kind EventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}
