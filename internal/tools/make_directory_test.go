package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMakeDirectoryCreatesNestedDirectories(t *testing.T) {
	workspace := t.TempDir()
	tool := NewMakeDirectory(workspace)

	result, err := tool.Run(context.Background(), []byte(`{"path":"parent/child"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(workspace, "parent", "child"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || !result.Changed {
		t.Fatalf("info.IsDir() = %t, result = %#v", info.IsDir(), result)
	}
}

func TestMakeDirectoryFailureAfterCreatingParentReportsChanged(t *testing.T) {
	workspace := t.TempDir()
	result, err := NewMakeDirectory(workspace).Run(context.Background(), []byte(`{"path":"created/`+strings.Repeat("a", 300)+`"}`))
	if err == nil {
		t.Fatal("Run() error = nil, want filename-too-long failure")
	}
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed after partial MkdirAll", result)
	}
	if info, statErr := os.Stat(filepath.Join(workspace, "created")); statErr != nil || !info.IsDir() {
		t.Fatalf("created parent state = %#v, %v", info, statErr)
	}
}
