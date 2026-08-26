package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMutatingAndSearchToolsRejectWorkspaceEscape(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "outside-link")); err != nil {
		t.Fatal(err)
	}

	create := NewCreateFile(workspace, 1<<20)
	if _, err := create.Run(context.Background(), []byte(`{"path":"outside-link/owned.txt","content":"no"}`)); err == nil {
		t.Fatal("create_file allowed a symlink escape")
	}
	if _, err := os.Stat(filepath.Join(outside, "owned.txt")); !os.IsNotExist(err) {
		t.Fatalf("outside file exists or stat failed unexpectedly: %v", err)
	}

	search := NewSearchCode(workspace, 10, 4096)
	if _, err := search.Run(context.Background(), []byte(`{"query":"secret","path":"../"}`)); err == nil {
		t.Fatal("search_code allowed a parent-directory escape")
	}
}
