package session

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/JH427/horizon-code/internal/agent"
)

type fakeRunner struct{}

func (fakeRunner) Run(_ context.Context, prompt string, emit func(agent.Event)) (string, error) {
	emit(agent.Event{Kind: agent.EventTelemetry, Telemetry: &agent.Telemetry{ContextTokens: 120, TokensPerSecond: 40, HasContext: true, HasTokensPerSecond: true}})
	emit(agent.Event{Kind: agent.EventTelemetry, Telemetry: &agent.Telemetry{ContextTokens: 125, TokensPerSecond: 42.5, HasContext: true, HasTokensPerSecond: true, Final: true}})
	emit(agent.Event{Kind: agent.EventToolStarted, ToolName: "read_file", CallID: "call-1"})
	emit(agent.Event{Kind: agent.EventAssistantDelta, Text: "done"})
	return "done", nil
}

func TestRecordingRunnerPersistsReplayableJSONL(t *testing.T) {
	store, err := NewStore(t.TempDir(), Metadata{Model: "qwen38-27b", Workspace: "/workspace"})
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	runner := NewRecordingRunner(fakeRunner{}, store)
	answer, err := runner.Run(context.Background(), "inspect marker", func(agent.Event) {})
	if err != nil || answer != "done" {
		t.Fatalf("Run() = %q, %v", answer, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	records, err := Replay(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, record := range records {
		kinds = append(kinds, record.Kind)
	}
	want := []string{"session_started", "turn_started", "telemetry", "tool_started", "assistant_delta", "turn_completed"}
	if len(kinds) != len(want) {
		t.Fatalf("record kinds = %v, want %v; file=%s", kinds, want, filepath.Base(store.Path()))
	}
	for index := range want {
		if kinds[index] != want[index] {
			t.Fatalf("record kinds = %v, want %v", kinds, want)
		}
	}
	if records[2].Telemetry == nil || records[2].Telemetry.ContextTokens != 125 || records[2].Telemetry.TokensPerSecond != 42.5 {
		t.Fatalf("telemetry record = %#v", records[2].Telemetry)
	}
}
