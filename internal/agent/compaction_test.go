package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type compactionModel struct {
	requests []model.Request
}

func (fake *compactionModel) Stream(_ context.Context, request model.Request, _ func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	switch len(fake.requests) {
	case 1:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "old", Name: "counting", Arguments: `{"path":"old-marker.txt"}`}}}, nil
	case 2:
		return model.Response{ToolCalls: []model.ToolCall{{ID: "recent", Name: "counting", Arguments: `{"path":"recent-marker.txt"}`}}}, nil
	default:
		return model.Response{Text: "compacted and complete"}, nil
	}
}

type markerTool struct{}

func (markerTool) Definition() model.Tool {
	return model.Tool{Name: "counting", Parameters: map[string]any{"type": "object"}}
}

func (markerTool) Run(_ context.Context, arguments []byte) (tools.Result, error) {
	marker := "OLD_OUTCOME"
	if strings.Contains(string(arguments), "recent-marker") {
		marker = "RECENT_OUTCOME"
	}
	return tools.Result{Content: marker + strings.Repeat("x", 900)}, nil
}

func TestEngineCompactsBeforeRetryingContextEstimate(t *testing.T) {
	fake := &compactionModel{}
	engine := NewEngineWithBudget(fake, []Tool{markerTool{}}, Budget{
		MaxSteps: 4, MaxToolCalls: 4, MaxRepairs: 2, MaxToolResultBytes: 4096, MaxContextTokens: 650,
		CompactionKeepRecentRounds: 1, MaxCompactionSummaryBytes: 256,
	})

	answer, err := engine.Run(context.Background(), "ORIGINAL_COMPACTION_REQUEST", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if answer != "compacted and complete" || len(fake.requests) != 3 {
		t.Fatalf("answer = %q, requests = %d", answer, len(fake.requests))
	}
	messages := fake.requests[2].Messages
	if messages[0].Role != "system" || messages[1].Role != "user" || messages[1].Content != "ORIGINAL_COMPACTION_REQUEST" {
		t.Fatalf("original context not preserved: %#v", messages)
	}
	joined := fmt.Sprint(messages)
	for _, marker := range []string{compactSummaryLabel, "old-marker.txt", "OLD_OUTCOME", "recent-marker.txt", "RECENT_OUTCOME"} {
		if !strings.Contains(joined, marker) {
			t.Fatalf("compacted request lacks %q: %#v", marker, messages)
		}
	}
	if strings.Contains(messages[2].Content, strings.Repeat("x", 300)) || !strings.Contains(messages[len(messages)-1].Content, strings.Repeat("x", 900)) {
		t.Fatalf("older full outcome was not reduced while recent evidence was retained: %#v", messages)
	}
	assertToolProtocol(t, messages)
}

type oversizedRecentTool struct{}

func (oversizedRecentTool) Definition() model.Tool {
	return model.Tool{Name: "counting", Parameters: map[string]any{"type": "object"}}
}

func (oversizedRecentTool) Run(_ context.Context, arguments []byte) (tools.Result, error) {
	if strings.Contains(string(arguments), "recent-marker") {
		return tools.Result{Content: "RECENT_TOO_LARGE" + strings.Repeat("y", 5000)}, nil
	}
	return tools.Result{Content: "old small result"}, nil
}

func TestEngineFailsClosedWhenCompactionRemainsOverLimit(t *testing.T) {
	fake := &compactionModel{}
	engine := NewEngineWithBudget(fake, []Tool{oversizedRecentTool{}}, Budget{
		MaxSteps: 4, MaxToolCalls: 4, MaxRepairs: 2, MaxToolResultBytes: 8192, MaxContextTokens: 650,
		CompactionKeepRecentRounds: 1, MaxCompactionSummaryBytes: 128,
	})

	_, err := engine.Run(context.Background(), "request", nil)
	if err == nil || !strings.Contains(err.Error(), "context budget") || !strings.Contains(err.Error(), "after compaction") {
		t.Fatalf("Run() error = %v, want fail-closed context error after compaction", err)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("model requests = %d, want no over-limit third request", len(fake.requests))
	}
}

func TestCompactMessagesPreservesProtocolAndRecentRound(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "SYSTEM_MARKER"},
		{Role: "user", Content: "ORIGINAL_REQUEST"},
		{Role: "assistant", ToolCalls: []model.ToolCallWire{
			{ID: "old-read", Type: "function", Function: model.FunctionWire{Name: "read_file", Arguments: `{"path":"internal/agent/engine.go"}`}},
			{ID: "old-test", Type: "function", Function: model.FunctionWire{Name: "run_command", Arguments: `{"command":"go test ./internal/agent"}`}},
		}},
		{Role: "tool", ToolCallID: "old-read", Content: "ENGINE_MARKER\n" + strings.Repeat("old evidence ", 80)},
		{Role: "tool", ToolCallID: "old-test", Content: "PASS_MARKER\nok"},
		{Role: "assistant", ToolCalls: []model.ToolCallWire{
			{ID: "recent-diff", Type: "function", Function: model.FunctionWire{Name: "git_diff", Arguments: `{}`}},
		}},
		{Role: "tool", ToolCallID: "recent-diff", Content: "RECENT_EVIDENCE_MARKER"},
	}

	compacted, changed := compactMessages(messages, 1, 512)
	if !changed {
		t.Fatal("compactMessages() changed = false, want true")
	}
	if compacted[0].Role != "system" || compacted[0].Content != "SYSTEM_MARKER" || compacted[1].Role != "user" || compacted[1].Content != "ORIGINAL_REQUEST" {
		t.Fatalf("leading messages changed: %#v", compacted[:2])
	}
	if len(compacted) != 5 {
		t.Fatalf("compacted messages = %#v, want system, user, summary, recent assistant, recent tool", compacted)
	}
	summary := compacted[2]
	if summary.Role != "user" {
		t.Fatalf("summary role = %q, want user", summary.Role)
	}
	for _, marker := range []string{"COMPACTED_TOOL_HISTORY", "read_file", "internal/agent/engine.go", "ENGINE_MARKER", "run_command", "go test ./internal/agent", "PASS_MARKER"} {
		if !strings.Contains(summary.Content, marker) {
			t.Fatalf("summary lacks %q: %q", marker, summary.Content)
		}
	}
	if len(summary.Content) > 512 {
		t.Fatalf("summary bytes = %d, want <= 512", len(summary.Content))
	}
	if compacted[3].ToolCalls[0].ID != "recent-diff" || compacted[4].ToolCallID != "recent-diff" || compacted[4].Content != "RECENT_EVIDENCE_MARKER" {
		t.Fatalf("recent round changed: %#v", compacted[3:])
	}
	assertToolProtocol(t, compacted)
}

func TestCompactMessagesCoalescesPriorCompactionSummary(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "request"},
		{Role: "user", Content: compactSummaryLabel + " source=older complete assistant/tool rounds PREVIOUS_MARKER"},
		{Role: "assistant", ToolCalls: []model.ToolCallWire{{ID: "middle", Type: "function", Function: model.FunctionWire{Name: "read_file", Arguments: `{"path":"middle.txt"}`}}}},
		{Role: "tool", ToolCallID: "middle", Content: "MIDDLE_MARKER"},
		{Role: "assistant", ToolCalls: []model.ToolCallWire{{ID: "recent", Type: "function", Function: model.FunctionWire{Name: "run_command", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "recent", Content: "RECENT_MARKER"},
	}

	compacted, changed := compactMessages(messages, 1, 512)
	if !changed {
		t.Fatal("compactMessages() changed = false")
	}
	joined := fmt.Sprint(compacted)
	if strings.Count(joined, compactSummaryLabel) != 1 || !strings.Contains(joined, "PREVIOUS_MARKER") || !strings.Contains(joined, "MIDDLE_MARKER") {
		t.Fatalf("prior summary was not coalesced: %#v", compacted)
	}
	assertToolProtocol(t, compacted)
}

func TestCompactMessagesRefusesSummaryTooSmallForSourceLabel(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "request"},
		{Role: "assistant", ToolCalls: []model.ToolCallWire{{ID: "call", Type: "function", Function: model.FunctionWire{Name: "read_file", Arguments: `{}`}}}},
		{Role: "tool", ToolCallID: "call", Content: "outcome"},
	}

	compacted, changed := compactMessages(messages, 0, len(compactSummaryLabel)-1)
	if changed || len(compacted) != len(messages) {
		t.Fatalf("compaction produced an unlabeled summary: %#v", compacted)
	}
}

func TestCompactMessagesIsDeterministicBoundedAndTerminalSafe(t *testing.T) {
	messages := []model.Message{
		{Role: "system", Content: "system"},
		{Role: "user", Content: "request"},
		{Role: "assistant", ToolCalls: []model.ToolCallWire{{
			ID: "old", Type: "function", Function: model.FunctionWire{Name: "read_file", Arguments: "{\"path\":\"marker.txt\"}\x1b[31m"},
		}}},
		{Role: "tool", ToolCallID: "old", Content: "OUTCOME_MARKER\x00\x1b\u009b" + strings.Repeat("z", 500) + string([]byte{0xff})},
	}

	first, changed := compactMessages(messages, 0, 256)
	second, changedAgain := compactMessages(messages, 0, 256)
	if !changed || !changedAgain || first[2].Content != second[2].Content {
		t.Fatalf("compaction is not deterministic: first=%#v second=%#v", first, second)
	}
	summary := first[2].Content
	if len(summary) > 256 || !strings.Contains(summary, "[truncated]") {
		t.Fatalf("summary is not bounded with explicit truncation: %q", summary)
	}
	if strings.ContainsAny(summary, "\x00\x1b") || strings.ContainsRune(summary, '\u009b') || strings.ToValidUTF8(summary, "") != summary {
		t.Fatalf("summary is not terminal safe UTF-8: %q", summary)
	}
}

func assertToolProtocol(t *testing.T, messages []model.Message) {
	t.Helper()
	consumedTools := make(map[int]bool)
	for index, message := range messages {
		if message.Role != "assistant" || len(message.ToolCalls) == 0 {
			continue
		}
		for offset, call := range message.ToolCalls {
			toolIndex := index + 1 + offset
			if toolIndex >= len(messages) || messages[toolIndex].Role != "tool" || messages[toolIndex].ToolCallID != call.ID {
				t.Fatalf("tool protocol broken for call %q at %d: %#v", call.ID, index, messages)
			}
			consumedTools[toolIndex] = true
		}
	}
	for index, message := range messages {
		if message.Role == "tool" && !consumedTools[index] {
			t.Fatalf("orphan tool message at %d: %#v", index, message)
		}
	}
}
