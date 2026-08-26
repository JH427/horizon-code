package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCombinesHorizonFilesFromParentToWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "project", "app")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "HORIZON.md"), []byte("ROOT_ONLY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "project", "HORIZON.md"), []byte("PROJECT_ONLY"), 0o600); err != nil {
		t.Fatal(err)
	}

	document, err := Load(workspace, 64<<10)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Paths) != 2 {
		t.Fatalf("paths = %#v", document.Paths)
	}
	if strings.Index(document.Content, "ROOT_ONLY") > strings.Index(document.Content, "PROJECT_ONLY") {
		t.Fatalf("instructions are not parent-first:\n%s", document.Content)
	}
}

func TestLoadRejectsInstructionsOverBudget(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "HORIZON.md"), []byte(strings.Repeat("x", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(workspace, 10); err == nil {
		t.Fatal("Load() error = nil, want budget rejection")
	}
}
