package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type lifecycleModel struct {
	requests []model.Request
	respond  func(context.Context, int, func(model.Event)) (model.Response, error)
}

func (fake *lifecycleModel) Stream(ctx context.Context, request model.Request, emit func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	return fake.respond(ctx, len(fake.requests), emit)
}

type lifecycleTool struct {
	name string
	run  func(context.Context) (tools.Result, error)
}

func (tool lifecycleTool) Definition() model.Tool {
	return model.Tool{Name: tool.name, Parameters: map[string]any{"type": "object"}}
}

func (tool lifecycleTool) Run(ctx context.Context, _ []byte) (tools.Result, error) {
	return tool.run(ctx)
}

func lifecycleCall(id, name string) model.Response {
	return model.Response{ToolCalls: []model.ToolCall{{ID: id, Name: name, Arguments: `{}`}}}
}

func TestEnginePreservesConversationAndToolEvidenceAcrossTurns(t *testing.T) {
	fake := &lifecycleModel{respond: func(_ context.Context, step int, _ func(model.Event)) (model.Response, error) {
		if step == 1 {
			return lifecycleCall("read-1", "inspect"), nil
		}
		return model.Response{Text: fmt.Sprintf("answer-%d", step)}, nil
	}}
	engine := NewEngine(fake, []Tool{resultTool{name: "inspect", result: tools.Result{Content: "PRIOR_EVIDENCE"}}}, 3)
	if _, err := engine.Run(context.Background(), "PRIOR_REQUEST", nil); err != nil {
		t.Fatal(err)
	}
	engine.SetWorkspaceInstructions("UPDATED_POLICY")
	if _, err := engine.Run(context.Background(), "Use the previous result", nil); err != nil {
		t.Fatal(err)
	}
	messages := fake.requests[2].Messages
	for _, marker := range []string{"PRIOR_REQUEST", "PRIOR_EVIDENCE", "answer-2", "Use the previous result", "UPDATED_POLICY"} {
		if !strings.Contains(fmt.Sprint(messages), marker) {
			t.Fatalf("follow-up lacks %q: %#v", marker, messages)
		}
	}
	assertToolProtocol(t, messages)
}

func TestEngineClosesInterruptedMultiCallRoundBeforeNextTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executed := 0
	fake := &lifecycleModel{respond: func(_ context.Context, step int, _ func(model.Event)) (model.Response, error) {
		switch step {
		case 1:
			return model.Response{ToolCalls: []model.ToolCall{
				{ID: "partial", Name: "modify", Arguments: `{}`},
				{ID: "unstarted-1", Name: "modify", Arguments: `{}`},
				{ID: "unstarted-2", Name: "modify", Arguments: `{}`},
			}}, nil
		case 2:
			return lifecycleCall("verification", "verify"), nil
		default:
			return model.Response{Text: "Recovered and verified"}, nil
		}
	}}
	engine := NewEngine(fake, []Tool{
		lifecycleTool{name: "modify", run: func(ctx context.Context) (tools.Result, error) {
			executed++
			cancel()
			return tools.Result{Content: "PARTIAL_MUTATION_EVIDENCE", Changed: true}, ctx.Err()
		}},
		resultTool{name: "verify", result: tools.Result{Content: "PASS", Verified: true}},
	}, 4)
	if _, err := engine.Run(ctx, "Original request", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("first turn: %v", err)
	}
	if executed != 1 {
		t.Fatalf("executed %d calls after cancellation", executed)
	}
	if _, err := engine.Run(context.Background(), "Continue", nil); err != nil {
		t.Fatal(err)
	}
	messages := fake.requests[1].Messages
	assertToolProtocol(t, messages)
	for _, marker := range []string{"Original request", "PARTIAL_MUTATION_EVIDENCE", "Do not assume it succeeded"} {
		if !strings.Contains(fmt.Sprint(messages), marker) {
			t.Fatalf("resumed request lacks %q: %#v", marker, messages)
		}
	}
}

func TestEngineDoesNotExecuteCallCancelledByRecordingCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executed := 0
	fake := &lifecycleModel{respond: func(_ context.Context, _ int, _ func(model.Event)) (model.Response, error) {
		return lifecycleCall("call", "modify"), nil
	}}
	engine := NewEngine(fake, []Tool{lifecycleTool{name: "modify", run: func(context.Context) (tools.Result, error) {
		executed++
		return tools.Result{Changed: true}, nil
	}}}, 2)
	_, err := engine.Run(ctx, "Change it", func(event Event) {
		if event.Kind == EventToolStarted {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || executed != 0 {
		t.Fatalf("err=%v, executed=%d", err, executed)
	}
	if engine.lastToolName != "modify" || engine.lastToolSucceeded != nil {
		t.Fatalf("cancelled tool outcome = name %q, succeeded %v; want unknown", engine.lastToolName, engine.lastToolSucceeded)
	}
	assertToolProtocol(t, engine.history)
}

type trackedBackgroundTool struct{ running bool }

func (tool *trackedBackgroundTool) Definition() model.Tool {
	return model.Tool{Name: "background_process", Parameters: map[string]any{"type": "object"}}
}

func (tool *trackedBackgroundTool) Run(_ context.Context, args []byte) (tools.Result, error) {
	tool.running = !strings.Contains(string(args), "stop")
	return tools.Result{Content: "BACKGROUND_PROCESS", Changed: true}, nil
}

func (tool *trackedBackgroundTool) HasRunningProcesses() bool { return tool.running }

func TestEngineRequiresJobsStoppedBeforeFreshVerification(t *testing.T) {
	background := &trackedBackgroundTool{}
	fake := &lifecycleModel{respond: func(_ context.Context, step int, emit func(model.Event)) (model.Response, error) {
		switch step {
		case 1:
			return lifecycleCall("start", "background_process"), nil
		case 2, 6:
			return lifecycleCall(fmt.Sprintf("verify-%d", step), "verify"), nil
		case 4:
			return model.Response{ToolCalls: []model.ToolCall{{ID: "stop", Name: "background_process", Arguments: `{"operation":"stop"}`}}}, nil
		case 3, 5:
			emit(model.Event{Kind: model.EventTextDelta, Text: "PREMATURE_SUCCESS"})
			return model.Response{Text: "PREMATURE_SUCCESS"}, nil
		default:
			emit(model.Event{Kind: model.EventTextDelta, Text: "Freshly verified"})
			return model.Response{Text: "Freshly verified"}, nil
		}
	}}
	engine := NewEngine(fake, []Tool{background, resultTool{name: "verify", result: tools.Result{Content: "PASS", Verified: true}}}, 8)
	var displayed strings.Builder
	answer, err := engine.Run(context.Background(), "Run work and verify", func(event Event) {
		if event.Kind == EventAssistantDelta {
			displayed.WriteString(event.Text)
		}
	})
	if err != nil || answer != "Freshly verified" || len(fake.requests) != 7 {
		t.Fatalf("answer=%q, err=%v, requests=%d", answer, err, len(fake.requests))
	}
	if strings.Contains(displayed.String(), "PREMATURE_SUCCESS") {
		t.Fatalf("unverified completion leaked: %s", displayed.String())
	}
	if !strings.Contains(fmt.Sprint(fake.requests[2].Messages), "does not clear pending verification") {
		t.Fatal("verification while jobs were active was not withheld")
	}
}

func TestEngineRejectsVerificationWhenJobExitsDuringCheck(t *testing.T) {
	background := &trackedBackgroundTool{}
	checks := 0
	fake := &lifecycleModel{respond: func(_ context.Context, step int, _ func(model.Event)) (model.Response, error) {
		switch step {
		case 1:
			return lifecycleCall("start", "background_process"), nil
		case 2, 4:
			return lifecycleCall(fmt.Sprintf("verify-%d", step), "verify"), nil
		case 3:
			return model.Response{Text: "Stale verification"}, nil
		default:
			return model.Response{Text: "Fresh verification"}, nil
		}
	}}
	engine := NewEngine(fake, []Tool{background, lifecycleTool{name: "verify", run: func(context.Context) (tools.Result, error) {
		checks++
		background.running = false
		return tools.Result{Content: "PASS", Verified: true}, nil
	}}}, 6)
	answer, err := engine.Run(context.Background(), "Verify after jobs exit", nil)
	if err != nil || checks != 2 || answer != "Fresh verification" {
		t.Fatalf("answer=%q checks=%d err=%v", answer, checks, err)
	}
}

func TestEngineCarriesBackgroundVerificationAcrossCancelledTurns(t *testing.T) {
	background := &trackedBackgroundTool{}
	fake := &lifecycleModel{respond: func(_ context.Context, step int, _ func(model.Event)) (model.Response, error) {
		switch step {
		case 1:
			return lifecycleCall("start", "background_process"), nil
		case 2:
			return model.Response{}, context.Canceled
		case 3:
			return model.Response{Text: "Unverified after job exit"}, nil
		case 4:
			return lifecycleCall("verify", "verify"), nil
		default:
			return model.Response{Text: "Verified after job exit"}, nil
		}
	}}
	engine := NewEngine(fake, []Tool{background, resultTool{name: "verify", result: tools.Result{Verified: true}}}, 5)
	if _, err := engine.Run(context.Background(), "Start work", nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	background.running = false
	answer, err := engine.Run(context.Background(), "Continue", nil)
	if err != nil || answer != "Verified after job exit" {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
}

func TestEngineResetsStepAndToolBudgetsForEachTurn(t *testing.T) {
	fake := &lifecycleModel{respond: func(_ context.Context, step int, _ func(model.Event)) (model.Response, error) {
		if step%2 == 1 {
			return lifecycleCall(fmt.Sprintf("call-%d", step), "inspect"), nil
		}
		return model.Response{Text: "Done"}, nil
	}}
	engine := NewEngineWithBudget(fake, []Tool{resultTool{name: "inspect", result: tools.Result{Content: "EVIDENCE"}}}, Budget{MaxSteps: 2, MaxToolCalls: 1})
	for range 3 {
		if _, err := engine.Run(context.Background(), "Inspect", nil); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.requests) != 6 {
		t.Fatalf("requests=%d", len(fake.requests))
	}
	assertToolProtocol(t, fake.requests[4].Messages)
}

func TestEngineRejectsConcurrentTurnWithoutCorruptingHistory(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	fake := &lifecycleModel{respond: func(_ context.Context, step int, _ func(model.Event)) (model.Response, error) {
		if step == 1 {
			close(started)
			<-release
		}
		return model.Response{Text: "Done"}, nil
	}}
	engine := NewEngine(fake, []Tool{resultTool{name: "inspect"}}, 2)
	done := make(chan error, 1)
	go func() { _, err := engine.Run(context.Background(), "First", nil); done <- err }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn did not start")
	}
	if _, err := engine.Run(context.Background(), "Concurrent", nil); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("concurrent turn admitted: %v", err)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "Next", nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprint(fake.requests[1].Messages), "Concurrent") {
		t.Fatal("rejected turn contaminated conversation history")
	}
}

func TestEngineCompactsHistoryAcrossTurnsWithoutLosingCurrentRequest(t *testing.T) {
	fake := &lifecycleModel{respond: func(_ context.Context, step int, _ func(model.Event)) (model.Response, error) {
		if step%2 == 1 {
			return lifecycleCall(fmt.Sprintf("call-%d", step), "inspect"), nil
		}
		return model.Response{Text: "Done"}, nil
	}}
	engine := NewEngineWithBudget(fake, []Tool{resultTool{name: "inspect", result: tools.Result{Content: strings.Repeat("EVIDENCE", 150)}}}, Budget{
		MaxSteps: 2, MaxContextTokens: 800, CompactionKeepRecentRounds: 1, MaxCompactionSummaryBytes: 200,
	})
	if _, err := engine.Run(context.Background(), "ORIGINAL_REQUEST", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "CURRENT_REQUEST", nil); err != nil {
		t.Fatal(err)
	}
	messages := fake.requests[3].Messages
	for _, marker := range []string{"ORIGINAL_REQUEST", "CURRENT_REQUEST", compactSummaryLabel} {
		if !strings.Contains(fmt.Sprint(messages), marker) {
			t.Fatalf("cross-turn compaction lost %q: %#v", marker, messages)
		}
	}
	assertToolProtocol(t, messages)
}
