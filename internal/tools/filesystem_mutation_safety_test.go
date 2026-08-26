package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFilesystemMutationToolsDoNotTraverseSymlinksOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if _, err := NewMakeDirectory(workspace).Run(context.Background(), []byte(`{"path":"outside-link/new"}`)); err == nil {
		t.Fatal("make_directory traversed an outside symlink")
	}
	if _, err := NewApplyPatch(workspace, 1<<20).Run(context.Background(), []byte(`{"patches":[{"path":"outside-link/outside.txt","old_text":"safe","new_text":"changed"}]}`)); err == nil {
		t.Fatal("apply_patch traversed an outside symlink")
	}
	if _, err := NewMovePath(workspace).Run(context.Background(), []byte(`{"source":"outside-link/outside.txt","destination":"moved.txt"}`)); err == nil {
		t.Fatal("move_path traversed an outside symlink")
	}
	assertFileContent(t, outsideFile, "safe\n")

	result, err := NewDeletePath(workspace).Run(context.Background(), []byte(`{"path":"outside-link","recursive":true}`))
	if err != nil {
		t.Fatalf("delete_path symlink Run() error = %v", err)
	}
	if !result.Changed {
		t.Fatalf("result = %#v, want Changed", result)
	}
	assertFileContent(t, outsideFile, "safe\n")
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("symlink lstat error = %v, want not exist", err)
	}
}

func TestDeletePathRejectsWorkspaceRoot(t *testing.T) {
	workspace := t.TempDir()
	tool := NewDeletePath(workspace)

	if _, err := tool.Run(context.Background(), []byte(`{"path":"."}`)); err == nil {
		t.Fatal("delete_path allowed deleting the workspace root")
	}
	if _, err := os.Stat(workspace); err != nil {
		t.Fatalf("workspace changed after rejected delete: %v", err)
	}
}

func TestApplyPatchPreflightsEveryReplacementBeforeWriting(t *testing.T) {
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

	_, err := tool.Run(context.Background(), []byte(`{"patches":[{"path":"first.txt","old_text":"alpha","new_text":"one"},{"path":"second.txt","old_text":"missing","new_text":"two"}]}`))
	if err == nil {
		t.Fatal("Run() error = nil, want missing match rejection")
	}
	assertFileContent(t, first, "alpha\n")
	assertFileContent(t, second, "beta\n")
}
