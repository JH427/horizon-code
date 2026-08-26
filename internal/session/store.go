package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JH427/horizon-code/internal/agent"
)

type Metadata struct {
	Model     string `json:"model"`
	Workspace string `json:"workspace"`
}

type Record struct {
	Time      time.Time        `json:"time"`
	Kind      string           `json:"kind"`
	TurnID    string           `json:"turn_id,omitempty"`
	Text      string           `json:"text,omitempty"`
	ToolName  string           `json:"tool_name,omitempty"`
	CallID    string           `json:"call_id,omitempty"`
	Arguments string           `json:"arguments,omitempty"`
	Error     string           `json:"error,omitempty"`
	Metadata  *Metadata        `json:"metadata,omitempty"`
	Telemetry *agent.Telemetry `json:"telemetry,omitempty"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	file    *os.File
	encoder *json.Encoder
	closed  bool
}

type Runner interface {
	Run(context.Context, string, func(agent.Event)) (string, error)
}

type RecordingRunner struct {
	runner Runner
	store  *Store
	turns  atomic.Uint64
}

func NewStore(root string, metadata Metadata) (*Store, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create session directory: %w", err)
	}
	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		return nil, fmt.Errorf("create session ID: %w", err)
	}
	name := time.Now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(suffix) + ".jsonl"
	path := filepath.Join(root, name)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create session log: %w", err)
	}
	store := &Store{path: path, file: file, encoder: json.NewEncoder(file)}
	if err := store.Append(Record{Kind: "session_started", Metadata: &metadata}); err != nil {
		_ = file.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Path() string {
	return store.path
}

func (store *Store) Append(record Record) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return errors.New("session store is closed")
	}
	if record.Time.IsZero() {
		record.Time = time.Now().UTC()
	}
	if err := store.encoder.Encode(record); err != nil {
		return fmt.Errorf("append session record: %w", err)
	}
	if record.Kind == "session_started" || record.Kind == "turn_completed" || record.Kind == "turn_failed" {
		if err := store.file.Sync(); err != nil {
			return fmt.Errorf("sync session record: %w", err)
		}
	}
	return nil
}

func (store *Store) Close() error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.closed {
		return nil
	}
	store.closed = true
	if err := store.file.Close(); err != nil {
		return fmt.Errorf("close session log: %w", err)
	}
	return nil
}

func Replay(path string) ([]Record, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open session log: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(bufio.NewReader(file))
	var records []Record
	for {
		var record Record
		if err := decoder.Decode(&record); err != nil {
			if errors.Is(err, io.EOF) {
				return records, nil
			}
			return nil, fmt.Errorf("decode session record %d: %w", len(records)+1, err)
		}
		records = append(records, record)
	}
}

func NewRecordingRunner(runner Runner, store *Store) *RecordingRunner {
	return &RecordingRunner{runner: runner, store: store}
}

func (runner *RecordingRunner) Run(ctx context.Context, prompt string, emit func(agent.Event)) (string, error) {
	turnID := fmt.Sprintf("turn-%d", runner.turns.Add(1))
	if err := runner.store.Append(Record{Kind: "turn_started", TurnID: turnID, Text: prompt}); err != nil {
		return "", err
	}
	answer, err := runner.runner.Run(ctx, prompt, func(event agent.Event) {
		record := Record{
			Kind:      string(event.Kind),
			TurnID:    turnID,
			Text:      event.Text,
			ToolName:  event.ToolName,
			CallID:    event.CallID,
			Arguments: event.Arguments,
			Telemetry: event.Telemetry,
		}
		if event.Err != nil {
			record.Error = event.Err.Error()
		}
		if event.Telemetry == nil || event.Telemetry.Estimated || event.Telemetry.Final {
			_ = runner.store.Append(record)
		}
		if emit != nil {
			emit(event)
		}
	})
	completion := Record{Kind: "turn_completed", TurnID: turnID, Text: answer}
	if err != nil {
		completion.Kind = "turn_failed"
		completion.Error = err.Error()
	}
	if appendErr := runner.store.Append(completion); appendErr != nil && err == nil {
		return answer, appendErr
	}
	return answer, err
}
