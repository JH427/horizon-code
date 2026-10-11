package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type finalizationTool struct {
	name string
	runs int
}

func (tool *finalizationTool) Definition() model.Tool {
	return model.Tool{Name: tool.name, Description: tool.name, Parameters: map[string]any{"type": "object"}}
}

func (tool *finalizationTool) Run(context.Context, []byte) (tools.Result, error) {
	tool.runs++
	switch tool.name {
	case "edit_file":
		return tools.Result{Content: "changed", Changed: true}, nil
	case "run_command":
		return tools.Result{Content: "tests passed", Verified: true}, nil
	default:
		return tools.Result{Content: "read"}, nil
	}
}

type finalizationModel struct {
	requests []model.Request
}

func (fake *finalizationModel) Stream(_ context.Context, request model.Request, _ func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	switch len(fake.requests) {
	case 1:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "edit-1", Name: "edit_file", Arguments: `{}`}}}, nil
	case 2:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "verify-1", Name: "run_command", Arguments: `{}`}}}, nil
	case 3:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "edit-2", Name: "edit_file", Arguments: `{}`}}}, nil
	default:
		return model.Response{Text: "finished"}, nil
	}
}

func TestVerifiedArtifactIsLockedAndFinalizationIsBounded(t *testing.T) {
	fake := &finalizationModel{}
	editTool := &finalizationTool{name: "edit_file"}
	verifyTool := &finalizationTool{name: "run_command"}
	engine := NewEngine(fake, []Tool{editTool, verifyTool}, 8)
	engine.SetLockAfterVerification(true)
	var blocked bool
	answer, err := engine.Run(context.Background(), "change and verify", func(event Event) {
		if event.Kind == EventToolFailed && event.ToolName == "edit_file" && event.Err != nil && strings.Contains(event.Err.Error(), "VERIFIED_ARTIFACT_LOCKED") {
			blocked = true
		}
	})
	if err != nil || answer != "finished" {
		t.Fatalf("Run() = %q, %v", answer, err)
	}
	if editTool.runs != 1 || verifyTool.runs != 1 || !blocked {
		t.Fatalf("tool runs edit=%d verify=%d blocked=%t", editTool.runs, verifyTool.runs, blocked)
	}
	if len(fake.requests) != 4 {
		t.Fatalf("model requests = %d, want 4", len(fake.requests))
	}
	for _, index := range []int{2, 3} {
		options := fake.requests[index].InferenceOptions
		if options.EnableThinking == nil || *options.EnableThinking || options.MaxTokens == nil || *options.MaxTokens != 1024 {
			t.Fatalf("finalization request %d options = %#v", index+1, options)
		}
	}
}

type endlessFinalizationModel struct{ steps int }

func (fake *endlessFinalizationModel) Stream(_ context.Context, _ model.Request, _ func(model.Event)) (model.Response, error) {
	fake.steps++
	switch fake.steps {
	case 1:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "edit", Name: "edit_file", Arguments: `{}`}}}, nil
	case 2:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "verify", Name: "run_command", Arguments: `{}`}}}, nil
	default:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "read", Name: "read_file", Arguments: `{}`}}}, nil
	}
}

func TestVerifiedFinalizationStopsAfterTwoModelSteps(t *testing.T) {
	fake := &endlessFinalizationModel{}
	engine := NewEngine(fake, []Tool{
		&finalizationTool{name: "edit_file"},
		&finalizationTool{name: "run_command"},
		&finalizationTool{name: "read_file"},
	}, 8)
	engine.SetLockAfterVerification(true)
	_, err := engine.Run(context.Background(), "change and verify", nil)
	if err == nil || !strings.Contains(err.Error(), "verified workspace preserved") {
		t.Fatalf("Run() error = %v", err)
	}
	if fake.steps != 4 {
		t.Fatalf("model steps = %d, want 4", fake.steps)
	}
}
