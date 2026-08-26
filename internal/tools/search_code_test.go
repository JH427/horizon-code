package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchCodeReturnsBoundedWorkspaceMatches(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "a.go"), []byte("needle one\nneedle two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "src", "b.txt"), []byte("needle ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewSearchCode(workspace, 1, 4096)
	result, err := tool.Run(context.Background(), []byte(`{"query":"needle","path":"src","file_glob":"*.go"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Content, "src/a.go:1:1:needle one") || strings.Contains(result.Content, "needle two") {
		t.Fatalf("content = %q", result.Content)
	}
	if !strings.Contains(result.Content, "truncated: true") {
		t.Fatalf("content does not report truncation: %q", result.Content)
	}
}
