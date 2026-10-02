package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/JH427/horizon-code/internal/agent"
	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type runnerFunc func(context.Context, string, func(agent.Event)) (string, error)

func (run runnerFunc) Run(ctx context.Context, prompt string, emit func(agent.Event)) (string, error) {
	return run(ctx, prompt, emit)
}

type failingLog struct {
	failKind string
	failure  error
	records  []Record
}

func (log *failingLog) Append(record Record) error {
	if record.Kind == log.failKind {
		return log.failure
	}
	log.records = append(log.records, record)
	return nil
}

type recordingModel struct{ calls int }

func (fake *recordingModel) Stream(context.Context, model.Request, func(model.Event)) (model.Response, error) {
	fake.calls++
	return model.Response{ToolCalls: []model.ToolCall{{ID: "call-1", Name: "modify", Arguments: `{}`}}}, nil
}

type recordingTool struct{ calls int }

func (tool *recordingTool) Definition() model.Tool {
	return model.Tool{Name: "modify", Parameters: map[string]any{"type": "object"}}
}

func (tool *recordingTool) Run(context.Context, []byte) (tools.Result, error) {
	tool.calls++
	return tools.Result{Changed: true}, nil
}

func TestRecordingFailuresStopFurtherModelAndToolExecution(t *testing.T) {
	for _, test := range []struct {
		kind       agent.EventKind
		modelCalls int
		toolCalls  int
	}{
		{agent.EventTelemetry, 0, 0},
		{agent.EventToolStarted, 1, 0},
		{agent.EventToolCompleted, 1, 1},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			diskErr := errors.New("disk full")
			log := &failingLog{failKind: string(test.kind), failure: diskErr}
			fake := &recordingModel{}
			tool := &recordingTool{}
			runner := NewRecordingRunner(agent.NewEngine(fake, []agent.Tool{tool}, 3), log)
			defer runner.Close()
			var displayed []agent.Event
			answer, err := runner.Run(context.Background(), "Modify files", func(event agent.Event) { displayed = append(displayed, event) })
			if answer != "" || !errors.Is(err, diskErr) || tool.calls != test.toolCalls || fake.calls != test.modelCalls {
				t.Fatalf("answer=%q err=%v tool calls=%d model calls=%d", answer, err, tool.calls, fake.calls)
			}
			for _, event := range displayed {
				if event.Kind == test.kind {
					t.Fatal("unrecorded event reached display")
				}
			}
			if len(log.records) == 0 || log.records[len(log.records)-1].Kind != "turn_failed" {
				t.Fatalf("failure not recorded when logger recovered: %#v", log.records)
			}
			if _, err := runner.Run(context.Background(), "Try another turn", nil); !errors.Is(err, diskErr) || fake.calls != test.modelCalls {
				t.Fatalf("recording failure was not sticky: err=%v model calls=%d", err, fake.calls)
			}
		})
	}
}

func TestRecordingTurnStartFailureDoesNotInvokeRunner(t *testing.T) {
	failure := errors.New("cannot record start")
	calls := 0
	runner := NewRecordingRunner(runnerFunc(func(context.Context, string, func(agent.Event)) (string, error) {
		calls++
		return "unexpected", nil
	}), &failingLog{failKind: "turn_started", failure: failure})
	defer runner.Close()
	if answer, err := runner.Run(context.Background(), "Task", nil); answer != "" || !errors.Is(err, failure) || calls != 0 {
		t.Fatalf("answer=%q err=%v calls=%d", answer, err, calls)
	}
}

func TestRecordingFinalWriteFailureCannotReportSuccess(t *testing.T) {
	failure := errors.New("cannot sync completion")
	runner := NewRecordingRunner(runnerFunc(func(context.Context, string, func(agent.Event)) (string, error) {
		return "success", nil
	}), &failingLog{failKind: "turn_completed", failure: failure})
	defer runner.Close()
	if answer, err := runner.Run(context.Background(), "Task", nil); answer != "" || !errors.Is(err, failure) {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
}

func TestRecordingPreservesBothExecutionAndCompletionWriteErrors(t *testing.T) {
	executionErr := errors.New("model failed")
	writeErr := errors.New("failure record could not be written")
	runner := NewRecordingRunner(runnerFunc(func(context.Context, string, func(agent.Event)) (string, error) {
		return "", executionErr
	}), &failingLog{failKind: "turn_failed", failure: writeErr})
	defer runner.Close()
	_, err := runner.Run(context.Background(), "Task", nil)
	if !errors.Is(err, executionErr) || !errors.Is(err, writeErr) {
		t.Fatalf("errors were swallowed: %v", err)
	}
}

func TestRecordingFailureDetectedEvenWhenUnderlyingRunnerIgnoresCancellation(t *testing.T) {
	failure := errors.New("event write failed")
	runner := NewRecordingRunner(runnerFunc(func(ctx context.Context, _ string, emit func(agent.Event)) (string, error) {
		emit(agent.Event{Kind: agent.EventAssistantDelta, Text: "unrecorded"})
		if ctx.Err() == nil {
			t.Error("recording failure did not cancel active context")
		}
		return "false success", nil
	}), &failingLog{failKind: string(agent.EventAssistantDelta), failure: failure})
	defer runner.Close()
	answer, err := runner.Run(context.Background(), "Task", func(agent.Event) { t.Error("unrecorded event was displayed") })
	if answer != "" || !errors.Is(err, failure) {
		t.Fatalf("answer=%q err=%v", answer, err)
	}
}

func TestRecordingCloseCancelsAndJoinsBeforeResourcesCanClose(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	runner := NewRecordingRunner(runnerFunc(func(ctx context.Context, _ string, _ func(agent.Event)) (string, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return "", ctx.Err()
	}), &failingLog{})
	turnDone := make(chan error, 1)
	go func() {
		_, err := runner.Run(context.Background(), "Long task", nil)
		turnDone <- err
	}()
	awaitLifecycle(t, started)
	closeDone := make(chan struct{})
	go func() { runner.Close(); close(closeDone) }()
	awaitLifecycle(t, cancelled)
	select {
	case <-closeDone:
		t.Fatal("Close returned before runner drained")
	default:
	}
	if _, err := runner.Run(context.Background(), "New turn", nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("new turn admitted during shutdown: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	awaitLifecycle(t, closeDone)
	if err := <-turnDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled turn returned %v", err)
	}
	runner.Close()
}

func TestRecordingParentCancellationPropagatesToRunner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	runner := NewRecordingRunner(runnerFunc(func(ctx context.Context, _ string, _ func(agent.Event)) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}), &failingLog{})
	defer runner.Close()
	done := make(chan error, 1)
	go func() { _, err := runner.Run(ctx, "Long task", nil); done <- err }()
	awaitLifecycle(t, started)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("parent cancellation did not drain the runner")
	}
}

func awaitLifecycle(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("lifecycle operation timed out")
	}
}
