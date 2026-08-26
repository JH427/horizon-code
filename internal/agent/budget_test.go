package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type budgetModel struct {
	response model.Response
	calls    int
}

func (fake *budgetModel) Stream(_ context.Context, _ model.Request, _ func(model.Event)) (model.Response, error) {
	fake.calls++
	return fake.response, nil
}

type countingTool struct {
	runs   int
	result tools.Result
}

func (tool *countingTool) Definition() model.Tool {
	return model.Tool{Name: "counting", Parameters: map[string]any{"type": "object"}}
}

func (tool *countingTool) Run(context.Context, []byte) (tools.Result, error) {
	tool.runs++
	return tool.result, nil
}

type duplicateFailureTool struct{ content string }

func (tool duplicateFailureTool) Definition() model.Tool {
	return model.Tool{Name: "counting", Parameters: map[string]any{"type": "object"}}
}
func (tool duplicateFailureTool) Run(context.Context, []byte) (tools.Result, error) {
	return tools.Result{Content: tool.content}, errors.New("command failed: " + tool.content)
}

type repairAfterFailureModel struct{ step int }

func (fake *repairAfterFailureModel) Stream(_ context.Context, _ model.Request, _ func(model.Event)) (model.Response, error) {
	fake.step++
	if fake.step == 1 {
		return model.Response{ToolCalls: []model.ToolCall{{ID: "one", Name: "counting", Arguments: `{}`}}}, nil
	}
	return model.Response{Text: "repaired"}, nil
}

func TestEngineCountsFailedToolOutputOnce(t *testing.T) {
	content := strings.Repeat("x", 80)
	fake := &repairAfterFailureModel{}
	engine := NewEngineWithBudget(fake, []Tool{duplicateFailureTool{content: content}}, Budget{
		MaxSteps: 2, MaxToolCalls: 2, MaxRepairs: 1, MaxToolResultBytes: 140, MaxContextTokens: 2048,
	})
	answer, err := engine.Run(context.Background(), "repair", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if answer != "repaired" {
		t.Fatalf("answer = %q", answer)
	}
}

func TestEngineRejectsToolCallsBeforeExceedingBudget(t *testing.T) {
	fake := &budgetModel{response: model.Response{ToolCalls: []model.ToolCall{
		{ID: "one", Name: "counting", Arguments: `{}`},
		{ID: "two", Name: "counting", Arguments: `{}`},
	}}}
	tool := &countingTool{}
	engine := NewEngineWithBudget(fake, []Tool{tool}, Budget{MaxSteps: 2, MaxToolCalls: 1, MaxRepairs: 1, MaxToolResultBytes: 1024, MaxContextTokens: 1024})
	_, err := engine.Run(context.Background(), "run tools", nil)
	if err == nil || !strings.Contains(err.Error(), "tool-call budget") {
		t.Fatalf("Run() error = %v, want tool-call budget error", err)
	}
	if tool.runs != 0 {
		t.Fatalf("tool runs = %d, want 0", tool.runs)
	}
}

func TestEngineRejectsToolResultBeforeExceedingBudget(t *testing.T) {
	fake := &budgetModel{response: model.Response{ToolCalls: []model.ToolCall{{ID: "one", Name: "counting", Arguments: `{}`}}}}
	tool := &countingTool{result: tools.Result{Content: strings.Repeat("x", 33)}}
	engine := NewEngineWithBudget(fake, []Tool{tool}, Budget{MaxSteps: 2, MaxToolCalls: 2, MaxRepairs: 1, MaxToolResultBytes: 32, MaxContextTokens: 1024})
	_, err := engine.Run(context.Background(), "run tool", nil)
	if err == nil || !strings.Contains(err.Error(), "tool-result budget") {
		t.Fatalf("Run() error = %v, want tool-result budget error", err)
	}
}

func TestEngineRejectsPromptBeforeExceedingContextBudget(t *testing.T) {
	fake := &budgetModel{response: model.Response{Text: "should not run"}}
	engine := NewEngineWithBudget(fake, []Tool{&countingTool{}}, Budget{MaxSteps: 2, MaxToolCalls: 2, MaxRepairs: 1, MaxToolResultBytes: 1024, MaxContextTokens: 32})
	_, err := engine.Run(context.Background(), strings.Repeat("large prompt ", 100), nil)
	if err == nil || !strings.Contains(err.Error(), "context budget") {
		t.Fatalf("Run() error = %v, want context budget error", err)
	}
	if fake.calls != 0 {
		t.Fatalf("model calls = %d, want 0", fake.calls)
	}
}

func TestEngineRejectsReportedUsageOverContextBudget(t *testing.T) {
	fake := &budgetModel{response: model.Response{Text: "too large", Usage: model.Usage{TotalTokens: 1025}}}
	engine := NewEngineWithBudget(fake, []Tool{&countingTool{}}, Budget{MaxSteps: 2, MaxToolCalls: 2, MaxRepairs: 1, MaxToolResultBytes: 1024, MaxContextTokens: 1024})
	_, err := engine.Run(context.Background(), "small", nil)
	if err == nil || !strings.Contains(err.Error(), "reported context budget") {
		t.Fatalf("Run() error = %v, want reported context budget error", err)
	}
}
