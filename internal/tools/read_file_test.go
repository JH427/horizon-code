package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileReturnsBoundedNumberedLines(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "marker.txt")
	if err := os.WriteFile(path, []byte("alpha\nHORIZON_MARKER\nomega\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFile(workspace, 2)
	result, err := tool.Execute(context.Background(), []byte(`{"path":"marker.txt"}`))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.Contains(result.Content, "1|alpha\n2|HORIZON_MARKER") {
		t.Fatalf("content = %q", result.Content)
	}
	if !result.Truncated || result.TotalLines != 3 || result.ShownLines != 2 {
		t.Fatalf("result = %#v", result)
	}
}

func TestReadFileRejectsSymlinkOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "escape.txt")); err != nil {
		t.Fatal(err)
	}

	tool := NewReadFile(workspace, 100)
	_, err := tool.Execute(context.Background(), []byte(`{"path":"escape.txt"}`))
	if err == nil {
		t.Fatal("Execute() error = nil, want workspace escape rejection")
	}
}
