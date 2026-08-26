package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitDiffReturnsBoundedWorkspaceDiff(t *testing.T) {
	workspace := t.TempDir()
	runGitTest(t, workspace, "init", "-q")
	runGitTest(t, workspace, "config", "user.email", "test@example.invalid")
	runGitTest(t, workspace, "config", "user.name", "Test")
	path := filepath.Join(workspace, "marker.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, workspace, "add", "marker.txt")
	runGitTest(t, workspace, "commit", "-qm", "baseline")
	if err := os.WriteFile(path, []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewGitDiff(workspace, 64<<10)
	result, err := tool.Run(context.Background(), []byte(`{"path":"marker.txt"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.Content, "-before") || !strings.Contains(result.Content, "+after") {
		t.Fatalf("content = %q", result.Content)
	}
}

func TestGitDiffRejectsNonRepositoryConcisely(t *testing.T) {
	tool := NewGitDiff(t.TempDir(), 64<<10)
	_, err := tool.Run(context.Background(), []byte(`{}`))
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if !strings.Contains(err.Error(), "not a Git repository") {
		t.Fatalf("Run() error = %q", err)
	}
	if len(err.Error()) > 512 {
		t.Fatalf("Run() error is too verbose: %d bytes", len(err.Error()))
	}
}

func runGitTest(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
