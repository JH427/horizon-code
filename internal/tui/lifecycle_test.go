package tui

import (
	"context"
	"testing"
	"time"

	"github.com/JH427/horizon-code/internal/agent"
)

type contextRunner struct {
	started chan context.Context
	flood   bool
}

func (runner contextRunner) Run(ctx context.Context, _ string, emit func(agent.Event)) (string, error) {
	runner.started <- ctx
	<-ctx.Done()
	if runner.flood {
		for range 200 {
			emit(agent.Event{Kind: agent.EventAssistantDelta, Text: "late event"})
		}
	}
	return "", ctx.Err()
}

func TestTUITurnInheritsApplicationCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := contextRunner{started: make(chan context.Context, 1)}
	model := NewModelWithContext(parent, runner, "model", "/workspace")
	model.composer.SetValue("Long task")
	_, command := model.submit()
	started := command().(runStartedMsg)
	var turn context.Context
	select {
	case turn = <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}
	cancel()
	select {
	case <-turn.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("application cancellation did not reach turn")
	}
	drainRunEvents(t, started.events)
}

func TestCancelledTurnStillDeliversTerminalUpdateWhileAppIsAlive(t *testing.T) {
	runner := contextRunner{started: make(chan context.Context, 1)}
	model := NewModelWithContext(context.Background(), runner, "model", "/workspace")
	model.composer.SetValue("Long task")
	updated, command := model.submit()
	state := updated.(Model)
	started := command().(runStartedMsg)
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}
	state.cancel()
	select {
	case update := <-started.events:
		if !update.done || update.err != context.Canceled {
			t.Fatalf("cancelled turn did not deliver completion: %#v", update)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("turn cancellation left the UI busy")
	}
	drainRunEvents(t, started.events)
}

func TestApplicationShutdownDoesNotBlockOnAbandonedEventConsumer(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := contextRunner{started: make(chan context.Context, 1), flood: true}
	model := NewModelWithContext(parent, runner, "model", "/workspace")
	model.composer.SetValue("Long task")
	_, command := model.submit()
	started := command().(runStartedMsg)
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}
	cancel()
	drainRunEvents(t, started.events)
}

func drainRunEvents(t *testing.T, events <-chan runUpdate) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case _, ok := <-events:
			if !ok {
				return
			}
		case <-deadline.C:
			t.Fatal("run-event producer did not stop")
		}
	}
}
