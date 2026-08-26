package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type resultTool struct {
	name   string
	result tools.Result
}

func (tool resultTool) Definition() model.Tool {
	return model.Tool{Name: tool.name, Parameters: map[string]any{"type": "object"}}
}

func (tool resultTool) Run(context.Context, []byte) (tools.Result, error) {
	return tool.result, nil
}

type failedMutationTool struct{}

func (failedMutationTool) Definition() model.Tool {
	return model.Tool{Name: "edit_file", Parameters: map[string]any{"type": "object"}}
}

func (failedMutationTool) Run(context.Context, []byte) (tools.Result, error) {
	return tools.Result{Content: "COMMAND_RESULT\nexit_code: 1", Changed: true}, errors.New("command exited after modifying the workspace")
}

type verificationModel struct {
	step     int
	requests []model.Request
}

func (fake *verificationModel) Stream(_ context.Context, request model.Request, emit func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	fake.step++
	switch fake.step {
	case 1:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "edit-1", Name: "edit_file", Arguments: `{}`}}}, nil
	case 2:
		emit(model.Event{Kind: model.EventTextDelta, Text: "Done without tests."})
		return model.Response{Text: "Done without tests."}, nil
	case 3:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "verify-1", Name: "run_command", Arguments: `{}`}}}, nil
	default:
		emit(model.Event{Kind: model.EventTextDelta, Text: "Implemented and verified."})
		return model.Response{Text: "Implemented and verified."}, nil
	}
}

func TestEngineRequiresVerificationAfterChange(t *testing.T) {
	fake := &verificationModel{}
	engine := NewEngine(fake, []Tool{
		resultTool{name: "edit_file", result: tools.Result{Content: "EDIT_FILE_OK", Changed: true}},
		resultTool{name: "run_command", result: tools.Result{Content: "COMMAND_RESULT", Verified: true}},
	}, 6)
	var events []Event
	answer, err := engine.Run(context.Background(), "change it", func(event Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if answer != "Implemented and verified." {
		t.Fatalf("answer = %q", answer)
	}
	if len(fake.requests) != 4 {
		t.Fatalf("requests = %d, want 4", len(fake.requests))
	}
	messages := fake.requests[2].Messages
	last := messages[len(messages)-1]
	if last.Role != "user" || !strings.Contains(last.Content, "VERIFICATION_REQUIRED") {
		t.Fatalf("verification reminder = %#v", last)
	}
	for _, event := range events {
		if event.Kind == EventAssistantDelta && strings.Contains(event.Text, "Done without tests") {
			t.Fatalf("premature completion leaked to display events: %#v", events)
		}
	}
}

func TestEngineRequiresVerificationAfterFailedMutatingTool(t *testing.T) {
	fake := &verificationModel{}
	engine := NewEngine(fake, []Tool{
		failedMutationTool{},
		resultTool{name: "run_command", result: tools.Result{Content: "COMMAND_RESULT", Verified: true}},
	}, 6)
	answer, err := engine.Run(context.Background(), "change it with the shell", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if answer != "Implemented and verified." {
		t.Fatalf("answer = %q, want verified completion", answer)
	}
	if len(fake.requests) != 4 {
		t.Fatalf("requests = %d, want verification repair loop", len(fake.requests))
	}
}

type crossTurnVerificationModel struct {
	step int
}

func (fake *crossTurnVerificationModel) Stream(_ context.Context, _ model.Request, _ func(model.Event)) (model.Response, error) {
	fake.step++
	switch fake.step {
	case 1:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "edit-1", Name: "edit_file", Arguments: `{}`}}}, nil
	case 2:
		return model.Response{}, context.Canceled
	case 3:
		return model.Response{Text: "Done without verification."}, nil
	case 4:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "verify-1", Name: "run_command", Arguments: `{}`}}}, nil
	default:
		return model.Response{Text: "Verified on the next turn."}, nil
	}
}

func TestEngineCarriesPendingVerificationAcrossTurns(t *testing.T) {
	fake := &crossTurnVerificationModel{}
	engine := NewEngine(fake, []Tool{
		resultTool{name: "edit_file", result: tools.Result{Content: "EDIT_FILE_OK", Changed: true}},
		resultTool{name: "run_command", result: tools.Result{Content: "COMMAND_RESULT", Verified: true}},
	}, 6)
	if _, err := engine.Run(context.Background(), "change it", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("first Run() error = %v, want context.Canceled", err)
	}
	answer, err := engine.Run(context.Background(), "continue", nil)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if answer != "Verified on the next turn." {
		t.Fatalf("answer = %q", answer)
	}
}
