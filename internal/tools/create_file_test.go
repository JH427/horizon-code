package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCreateFileCreatesNewConfinedFileAndRefusesOverwrite(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	tool := NewCreateFile(workspace, 1<<20)
	result, err := tool.Run(context.Background(), []byte(`{"path":"src/new.go","content":"package new\n"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(workspace, "src", "new.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "package new\n" || !result.Changed {
		t.Fatalf("content = %q, result = %#v", content, result)
	}
	if _, err := tool.Run(context.Background(), []byte(`{"path":"src/new.go","content":"overwrite\n"}`)); err == nil {
		t.Fatal("second Run() error = nil, want overwrite refusal")
	}
}
