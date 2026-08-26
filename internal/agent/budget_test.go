package agent

import (
	"context"
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
