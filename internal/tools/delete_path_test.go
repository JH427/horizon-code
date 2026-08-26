package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDeletePathRecursivelyDeletesDirectoryOnlyWhenExplicit(t *testing.T) {
	workspace := t.TempDir()
	directory := filepath.Join(workspace, "tree")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "child.txt"), []byte("child\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewDeletePath(workspace)

	if _, err := tool.Run(context.Background(), []byte(`{"path":"tree"}`)); err == nil {
		t.Fatal("Run() error = nil, want recursive opt-in requirement")
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("directory changed after rejected delete: %v", err)
	}
	result, err := tool.Run(context.Background(), []byte(`{"path":"tree","recursive":true}`))
	if err != nil {
		t.Fatalf("recursive Run() error = %v", err)
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("stat error = %v, want not exist", err)
	}
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed", result)
	}
}

func TestDeletePathDeletesFile(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "old.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewDeletePath(workspace)

	result, err := tool.Run(context.Background(), []byte(`{"path":"old.txt"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat error = %v, want not exist", err)
	}
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed", result)
	}
}

func TestDeletePathPartialRecursiveFailureReportsChanged(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "tree")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := NewDeletePath(workspace)
	tool.removeAll = func(path string) error {
		if err := os.WriteFile(filepath.Join(path, "partial-marker"), []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
		return os.ErrPermission
	}
	result, err := tool.Run(context.Background(), []byte(`{"path":"tree","recursive":true}`))
	if err == nil {
		t.Fatal("Run() error = nil, want injected partial failure")
	}
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed after partial RemoveAll", result)
	}
}
