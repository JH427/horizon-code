package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCommandExecutesAllowlistedReadOnlyCommand(t *testing.T) {
	workspace := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = workspace
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}

	tool := NewRunCommand(workspace, 64<<10, 30*time.Second)
	result, err := tool.Run(context.Background(), []byte(`{"program":"git","args":["status","--short"]}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Verified || !strings.Contains(result.Content, "exit_code: 0") {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunCommandMarksSuccessfulTestAsVerification(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "go.mod"), []byte("module example.invalid/smoke\n\ngo 1.25.8\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "smoke_test.go"), []byte("package smoke\n\nimport \"testing\"\n\nfunc TestSmoke(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool := NewRunCommand(workspace, 64<<10, 30*time.Second)
	result, err := tool.Run(context.Background(), []byte(`{"program":"go","args":["test","./..."]}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Verified {
		t.Fatalf("result = %#v, want verification", result)
	}
}

func TestRunCommandRejectsShellExecution(t *testing.T) {
	tool := NewRunCommand(t.TempDir(), 64<<10, 30*time.Second)
	_, err := tool.Run(context.Background(), []byte(`{"program":"bash","args":["-lc","touch owned"]}`))
	if err == nil {
		t.Fatal("Run() error = nil, want non-allowlisted program rejection")
	}
}

func TestRunCommandRejectsArgumentsThatEscapeWorkspace(t *testing.T) {
	tests := []struct {
		program string
		args    []string
	}{
		{program: "pytest", args: []string{"/tmp/outside_test.py"}},
		{program: "go", args: []string{"test", "../outside"}},
		{program: "go", args: []string{"test", "-exec=/bin/sh", "./..."}},
		{program: "go", args: []string{"test", "-coverprofile=/tmp/owned", "./..."}},
		{program: "go", args: []string{"build", "-o", "escaped-binary", "./..."}},
		{program: "git", args: []string{"diff", "--output=/tmp/escaped.diff"}},
		{program: "git", args: []string{"status", "--git-dir=/tmp/outside.git"}},
		{program: "cargo", args: []string{"test", "--manifest-path", "/tmp/Cargo.toml"}},
		{program: "npm", args: []string{"--prefix", "/tmp/outside", "test"}},
	}
	for _, test := range tests {
		t.Run(test.program+strings.Join(test.args, "_"), func(t *testing.T) {
			if err := validateVerificationCommand(test.program, test.args); err == nil {
				t.Fatalf("validateVerificationCommand(%q, %q) accepted workspace escape", test.program, test.args)
			}
		})
	}
}

func TestRunCommandRejectsCommandsThatDoNotVerify(t *testing.T) {
	tests := []struct {
		program string
		args    []string
	}{
		{program: "pytest", args: []string{"--help"}},
		{program: "pytest", args: []string{"--collect-only"}},
		{program: "go", args: []string{"test", "--help"}},
		{program: "go", args: []string{"build", "-n", "./..."}},
		{program: "cargo", args: []string{"test", "--version"}},
	}
	for _, test := range tests {
		t.Run(test.program+strings.Join(test.args, "_"), func(t *testing.T) {
			if err := validateVerificationCommand(test.program, test.args); err == nil {
				t.Fatalf("validateVerificationCommand(%q, %q) accepted non-verification", test.program, test.args)
			}
		})
	}
}

func TestRunCommandOmitsAndRejectsGitOutsideWorktree(t *testing.T) {
	tool := NewRunCommandForWorkspace(t.TempDir(), 1024, time.Second, false)
	properties := tool.Definition().Parameters["properties"].(map[string]any)
	program := properties["program"].(map[string]any)
	for _, candidate := range program["enum"].([]string) {
		if candidate == "git" {
			t.Fatal("non-Git run_command advertised git")
		}
	}
	_, err := tool.Run(context.Background(), []byte(`{"program":"git","args":["status"]}`))
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("Run() error = %v, want Git unavailable", err)
	}
}
