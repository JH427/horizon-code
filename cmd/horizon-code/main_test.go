package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseConfigUsesHorizonDefaultsAndAllowsWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workspace := t.TempDir()
	config, err := parseConfig([]string{"--workspace", workspace, "--prompt", "hello"})
	if err != nil {
		t.Fatalf("parseConfig() error = %v", err)
	}
	wantServerCommand := filepath.Join(home, ".config", "horizon-llm", "run-server.sh")
	if config.serverCommand != wantServerCommand {
		t.Fatalf("server command = %q, want %q", config.serverCommand, wantServerCommand)
	}
	if config.baseURL != "http://127.0.0.1:8080/v1" || config.healthURL != "http://127.0.0.1:8080/health" || config.model != "qwen38-27b" {
		t.Fatalf("model endpoint = %q health = %q model = %q", config.baseURL, config.healthURL, config.model)
	}
	if config.workspace != workspace || config.prompt != "hello" {
		t.Fatalf("workspace/prompt = %q %q", config.workspace, config.prompt)
	}
}

func TestParseConfigStoresSessionsOutsideWorkspace(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_STATE_HOME", "")
	workspace := t.TempDir()
	config, err := parseConfig([]string{"--workspace", workspace})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, ".local", "state", "horizon-code", "sessions")
	if config.sessionRoot != want {
		t.Fatalf("session root = %q, want %q", config.sessionRoot, want)
	}
}

type recordingInstructionTarget struct {
	content string
}

func (target *recordingInstructionTarget) SetWorkspaceInstructions(content string) {
	target.content = content
}

func TestConfigureWorkspaceInstructionsLoadsHorizonFilesAndReportsFound(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "HORIZON.md"), []byte("LOCAL_QWEN_RULE"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := &recordingInstructionTarget{}

	found, err := configureWorkspaceInstructions(target, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("configureWorkspaceInstructions() found = false, want true")
	}
	if target.content == "" || target.content == "LOCAL_QWEN_RULE" {
		t.Fatalf("configured content = %q, want source-labeled instructions", target.content)
	}
}

func TestConfigureWorkspaceInstructionsReportsNotFound(t *testing.T) {
	target := &recordingInstructionTarget{}

	found, err := configureWorkspaceInstructions(target, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("configureWorkspaceInstructions() found = true, want false")
	}
	if target.content != "" {
		t.Fatalf("configured content = %q, want empty", target.content)
	}
}

func TestWorkspaceToolsMakeGitOptional(t *testing.T) {
	withoutGit := workspaceTools(t.TempDir(), false)
	for _, tool := range withoutGit {
		if tool.Definition().Name == "git_diff" {
			t.Fatal("non-Git workspace unexpectedly received git_diff")
		}
	}
	withGit := workspaceTools(t.TempDir(), true)
	found := false
	for _, tool := range withGit {
		found = found || tool.Definition().Name == "git_diff"
	}
	if !found {
		t.Fatal("Git workspace did not receive git_diff")
	}
}

func TestSafeTerminalTextStripsControlSequences(t *testing.T) {
	got := safeTerminalText("visible\x1b]0;PWNED\a")
	if got != "visible" {
		t.Fatalf("safeTerminalText() = %q", got)
	}
}
