package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMovePathMovesSymlinkEntryWithoutTraversingTarget(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(workspace, "source-link")
	if err := os.Symlink(outsideFile, source); err != nil {
		t.Fatal(err)
	}
	tool := NewMovePath(workspace)

	result, err := tool.Run(context.Background(), []byte(`{"source":"source-link","destination":"destination-link"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Lstat(source); !os.IsNotExist(err) {
		t.Fatalf("source lstat error = %v, want not exist", err)
	}
	destination := filepath.Join(workspace, "destination-link")
	info, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 || !result.Changed {
		t.Fatalf("destination mode = %v, result = %#v", info.Mode(), result)
	}
	assertFileContent(t, outsideFile, "safe\n")
}

func TestMovePathRefusesToOverwriteDestination(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.txt")
	destination := filepath.Join(workspace, "destination.txt")
	if err := os.WriteFile(source, []byte("source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("destination\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewMovePath(workspace)

	if _, err := tool.Run(context.Background(), []byte(`{"source":"source.txt","destination":"destination.txt"}`)); err == nil {
		t.Fatal("Run() error = nil, want overwrite refusal")
	}
	assertFileContent(t, source, "source\n")
	assertFileContent(t, destination, "destination\n")
}

func TestMovePathMovesFileWithoutOverwriting(t *testing.T) {
	workspace := t.TempDir()
	source := filepath.Join(workspace, "source.txt")
	if err := os.WriteFile(source, []byte("content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewMovePath(workspace)

	result, err := tool.Run(context.Background(), []byte(`{"source":"source.txt","destination":"destination.txt"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source stat error = %v, want not exist", err)
	}
	assertFileContent(t, filepath.Join(workspace, "destination.txt"), "content\n")
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed", result)
	}
}
