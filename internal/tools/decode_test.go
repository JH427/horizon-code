package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestToolArgumentsRejectTrailingJSONValue(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "marker.txt"), []byte("safe\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewReadFile(workspace, 10)
	_, err := tool.Run(context.Background(), []byte(`{"path":"marker.txt"} {}`))
	if err == nil || !strings.Contains(err.Error(), "trailing JSON") {
		t.Fatalf("Run() error = %v, want trailing JSON error", err)
	}
}
