package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyPatchSchemaAvoidsLlamaGrammarSizedTextRepetitions(t *testing.T) {
	definition := NewApplyPatch(t.TempDir(), 4<<20).Definition()
	encoded, err := json.Marshal(definition.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"maxLength"`)) || bytes.Contains(encoded, []byte(`"maxItems":64`)) {
		t.Fatalf("apply_patch schema exposes runtime bounds as unsupported llama grammar repetitions: %s", encoded)
	}
}

func TestApplyPatchRejectsOversizedPathAtRuntime(t *testing.T) {
	tool := NewApplyPatch(t.TempDir(), 4<<20)
	path := strings.Repeat("a", 4097)
	payload, err := json.Marshal(map[string]any{"patches": []map[string]any{{"path": path, "old_text": "before", "new_text": "after"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Run(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "4096") {
		t.Fatalf("Run() error = %v, want runtime path length rejection", err)
	}
}

func TestApplyPatchPreservesConcurrentChangeCapturedDuringPromotion(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "file.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewApplyPatch(workspace, 1<<20)
	renameCalls := 0
	tool.rename = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 1 {
			if err := os.WriteFile(oldPath, []byte("USER CHANGE\n"), 0o600); err != nil {
				return err
			}
		}
		return os.Rename(oldPath, newPath)
	}
	result, err := tool.Run(context.Background(), []byte(`{"patches":[{"path":"file.txt","old_text":"alpha","new_text":"patched"}]}`))
	if err == nil {
		t.Fatal("Run() error = nil, want concurrent-change rejection")
	}
	if !result.Changed {
		t.Fatalf("result = %#v, want conservative Changed after transaction began", result)
	}
	assertFileContent(t, path, "USER CHANGE\n")
}

func TestApplyPatchRollsBackEveryFileWhenPromotionFails(t *testing.T) {
	workspace := t.TempDir()
	first := filepath.Join(workspace, "first.txt")
	second := filepath.Join(workspace, "second.txt")
	if err := os.WriteFile(first, []byte("alpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatch(workspace, 1<<20)
	renameCalls := 0
	tool.rename = func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 4 {
			return os.ErrPermission
		}
		return os.Rename(oldPath, newPath)
	}
	_, err := tool.Run(context.Background(), []byte(`{"patches":[{"path":"first.txt","old_text":"alpha","new_text":"one"},{"path":"second.txt","old_text":"beta","new_text":"two"}]}`))
	if err == nil {
		t.Fatal("Run() error = nil, want injected promotion failure")
	}
	assertFileContent(t, first, "alpha\n")
	assertFileContent(t, second, "beta\n")
}

func TestApplyPatchUpdatesMultipleFiles(t *testing.T) {
	workspace := t.TempDir()
	first := filepath.Join(workspace, "first.txt")
	second := filepath.Join(workspace, "second.txt")
	if err := os.WriteFile(first, []byte("alpha\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("beta\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewApplyPatch(workspace, 1<<20)
	result, err := tool.Run(context.Background(), []byte(`{"patches":[{"path":"first.txt","old_text":"alpha","new_text":"one"},{"path":"second.txt","old_text":"beta","new_text":"two"}]}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	assertFileContent(t, first, "one\n")
	assertFileContent(t, second, "two\n")
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed", result)
	}
}

func TestApplyPatchCleanupFailureStillReportsCommittedMutation(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "file.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewApplyPatch(workspace, 1<<20)
	removeCalls := 0
	tool.remove = func(path string) error {
		removeCalls++
		if removeCalls == 1 {
			return os.ErrPermission
		}
		return os.Remove(path)
	}
	result, err := tool.Run(context.Background(), []byte(`{"patches":[{"path":"file.txt","old_text":"before","new_text":"after"}]}`))
	if err == nil {
		t.Fatal("Run() error = nil, want injected cleanup failure")
	}
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed after committed promotion", result)
	}
	assertFileContent(t, path, "after\n")
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("content of %s = %q, want %q", path, content, want)
	}
}
