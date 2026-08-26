package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditFileReplacesOneExactMatchAtomically(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "main.go")
	if err := os.WriteFile(path, []byte("package main\n\nconst value = \"old\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFile(workspace, 1<<20)
	result, err := tool.Run(context.Background(), []byte(`{"path":"main.go","old_text":"const value = \"old\"","new_text":"const value = \"new\""}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `const value = "new"`) || !result.Changed {
		t.Fatalf("content = %q, result = %#v", content, result)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o, want 640", info.Mode().Perm())
	}
}

func TestEditFileRejectsAmbiguousMatch(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "main.txt")
	if err := os.WriteFile(path, []byte("same\nsame\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewEditFile(workspace, 1<<20)
	_, err := tool.Run(context.Background(), []byte(`{"path":"main.txt","old_text":"same","new_text":"changed"}`))
	if err == nil {
		t.Fatal("Run() error = nil, want ambiguous match rejection")
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "same\nsame\n" {
		t.Fatalf("file changed after rejected edit: %q", content)
	}
}
