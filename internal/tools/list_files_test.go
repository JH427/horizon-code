package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListFilesReturnsBoundedSortedDirectoryEntries(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "gallery.html"), []byte("<html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "README.txt"), []byte("read me"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "media"), 0o700); err != nil {
		t.Fatal(err)
	}

	result, err := NewListFiles(workspace, 2).Run(context.Background(), []byte(`{"path":"."}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"LIST_FILES_OK", "entries: 2", "truncated: true", "[file] README.txt", "[file] gallery.html"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("result does not contain %q:\n%s", want, result.Content)
		}
	}
	if strings.Contains(result.Content, "<html>") {
		t.Fatalf("listing leaked file contents: %s", result.Content)
	}
}

func TestListFilesRejectsPathOutsideWorkspace(t *testing.T) {
	workspace := t.TempDir()
	_, err := NewListFiles(workspace, 10).Run(context.Background(), []byte(`{"path":"../"}`))
	if err == nil {
		t.Fatal("Run() error = nil, want workspace escape rejection")
	}
}
