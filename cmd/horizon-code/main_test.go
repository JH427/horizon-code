package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JH427/horizon-code/internal/agent"
	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
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
	if config.lockAfterVerification {
		t.Fatal("artifact locking must be opt-in")
	}
	if config.capability != capabilitySafe {
		t.Fatalf("capability = %q, want %q", config.capability, capabilitySafe)
	}
	if config.decisionMode != agent.DecisionOff || config.decisionURL != "http://127.0.0.1:8001" || config.decisionCheckpoint != "doofz/THX-01" || config.decisionTimeout != 2*time.Second || config.decisionMinConfidence != 0.55 || config.maxSteps != 0 {
		t.Fatalf("decision/budget defaults = mode %q url %q timeout %s confidence %g max_steps %d", config.decisionMode, config.decisionURL, config.decisionTimeout, config.decisionMinConfidence, config.maxSteps)
	}
}

func TestParseConfigAcceptsTaskSpecificArtifactLock(t *testing.T) {
	config, err := parseConfig([]string{"--workspace", t.TempDir(), "--lock-after-verification"})
	if err != nil || !config.lockAfterVerification {
		t.Fatalf("lock=%t err=%v", config.lockAfterVerification, err)
	}
}

func TestParseConfigAcceptsEnabledDecisionConfigurations(t *testing.T) {
	for _, mode := range []agent.DecisionMode{agent.DecisionShadow, agent.DecisionActive} {
		config, err := parseConfig([]string{
			"--workspace", t.TempDir(), "--decision-mode", string(mode),
			"--decision-url", "http://localhost:9001/laya", "--decision-checkpoint", "doofz/THX-01", "--decision-timeout", "125ms",
			"--decision-min-confidence", "0.8", "--max-steps", "48",
		})
		if err != nil {
			t.Fatal(err)
		}
		if config.decisionMode != mode || config.decisionURL != "http://localhost:9001/laya" || config.decisionCheckpoint != "doofz/THX-01" || config.decisionTimeout != 125*time.Millisecond || config.decisionMinConfidence != 0.8 || config.maxSteps != 48 {
			t.Fatalf("decision config = %#v", config)
		}
	}
}

func TestParseConfigRejectsInvalidDecisionConfiguration(t *testing.T) {
	tests := [][]string{
		{"--decision-mode", "automatic"},
		{"--decision-timeout", "0s"},
		{"--decision-min-confidence", "0"},
		{"--decision-min-confidence", "1.1"},
		{"--decision-min-confidence", "NaN"},
		{"--max-steps", "-1"},
		{"--max-steps", "257"},
		{"--decision-url", "https://127.0.0.1:8001"},
		{"--decision-url", "http://192.0.2.1:8001"},
		{"--decision-url", "http://user@127.0.0.1:8001"},
		{"--decision-url", "http://127.0.0.1:8001?token=x"},
		{"--decision-url", "not-a-url"},
	}
	for _, arguments := range tests {
		t.Run(strings.Join(arguments, "_"), func(t *testing.T) {
			arguments = append([]string{"--workspace", t.TempDir()}, arguments...)
			if _, err := parseConfig(arguments); err == nil {
				t.Fatalf("parseConfig(%v) succeeded", arguments)
			}
		})
	}
}

func TestParseConfigAcceptsExplicitAutonomyModes(t *testing.T) {
	workspace := t.TempDir()
	for _, mode := range []capabilityMode{capabilityWorkspace, capabilityOperator} {
		config, err := parseConfig([]string{"--workspace", workspace, "--capability", string(mode)})
		if err != nil {
			t.Fatalf("parseConfig(%q) error = %v", mode, err)
		}
		if config.capability != mode {
			t.Fatalf("capability = %q, want %q", config.capability, mode)
		}
	}
}

func TestParseConfigRejectsUnknownCapability(t *testing.T) {
	_, err := parseConfig([]string{"--workspace", t.TempDir(), "--capability", "unlimited"})
	if err == nil || !strings.Contains(err.Error(), "capability") {
		t.Fatalf("parseConfig() error = %v, want capability error", err)
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

func TestCapabilityInstructionsDescribeAuthorizedAutonomy(t *testing.T) {
	workspacePolicy := capabilityInstructions(capabilityWorkspace)
	if !strings.Contains(workspacePolicy, "exec_command") || !strings.Contains(workspacePolicy, "not sandboxed") || strings.Contains(workspacePolicy, "unrestricted shell is not authorized") {
		t.Fatalf("workspace policy = %q", workspacePolicy)
	}
	operatorPolicy := capabilityInstructions(capabilityOperator)
	for _, required := range []string{"shell", "network", "Git writes", "background_process"} {
		if !strings.Contains(operatorPolicy, required) {
			t.Fatalf("operator policy does not contain %q: %q", required, operatorPolicy)
		}
	}
}

func TestWorkspaceToolsMakeGitOptional(t *testing.T) {
	withoutGit := workspaceTools(t.TempDir(), false, capabilitySafe)
	for _, tool := range withoutGit {
		if tool.Definition().Name == "git_diff" {
			t.Fatal("non-Git workspace unexpectedly received git_diff")
		}
	}
	withGit := workspaceTools(t.TempDir(), true, capabilitySafe)
	found := false
	for _, tool := range withGit {
		found = found || tool.Definition().Name == "git_diff"
	}
	if !found {
		t.Fatal("Git workspace did not receive git_diff")
	}
}

func TestAgentBudgetScalesAutonomyWithoutIncreasingActiveContext(t *testing.T) {
	safe := agentBudget(capabilitySafe)
	workspace := agentBudget(capabilityWorkspace)
	operator := agentBudget(capabilityOperator)
	if safe.MaxSteps != 16 || workspace.MaxSteps <= safe.MaxSteps || operator.MaxSteps <= workspace.MaxSteps {
		t.Fatalf("step budgets safe/workspace/operator = %d/%d/%d", safe.MaxSteps, workspace.MaxSteps, operator.MaxSteps)
	}
	if operator.MaxToolCalls < 64 || operator.MaxRepairs < 12 {
		t.Fatalf("operator budgets = %#v", operator)
	}
	if safe.MaxContextTokens != 48*1024 || workspace.MaxContextTokens != safe.MaxContextTokens || operator.MaxContextTokens != safe.MaxContextTokens {
		t.Fatalf("active context budgets safe/workspace/operator = %d/%d/%d", safe.MaxContextTokens, workspace.MaxContextTokens, operator.MaxContextTokens)
	}
}

func TestWorkspaceToolsExposeAutonomyByCapability(t *testing.T) {
	workspace := t.TempDir()
	safe := toolNames(workspaceTools(workspace, false, capabilitySafe))
	workspaceMode := toolNames(workspaceTools(workspace, false, capabilityWorkspace))
	operator := toolNames(workspaceTools(workspace, false, capabilityOperator))

	for _, forbidden := range []string{"apply_patch", "make_directory", "move_path", "delete_path", "exec_command", "background_process", "shell"} {
		if safe[forbidden] {
			t.Fatalf("safe mode unexpectedly exposes %s", forbidden)
		}
	}
	for _, required := range []string{"apply_patch", "make_directory", "move_path", "delete_path", "exec_command", "background_process"} {
		if !workspaceMode[required] {
			t.Fatalf("workspace mode does not expose %s", required)
		}
	}
	if workspaceMode["shell"] {
		t.Fatal("workspace mode unexpectedly exposes unrestricted shell")
	}
	if !operator["shell"] {
		t.Fatal("operator mode does not expose unrestricted shell")
	}
}

func toolNames(available []agent.Tool) map[string]bool {
	names := make(map[string]bool, len(available))
	for _, tool := range available {
		names[tool.Definition().Name] = true
	}
	return names
}

type closableTestTool struct{ closed bool }

func (tool *closableTestTool) Definition() model.Tool { return model.Tool{Name: "closable"} }
func (tool *closableTestTool) Run(context.Context, []byte) (tools.Result, error) {
	return tools.Result{}, nil
}
func (tool *closableTestTool) Close() error { tool.closed = true; return nil }

func TestCloseToolResourcesClosesManagedTools(t *testing.T) {
	tool := &closableTestTool{}
	if err := closeToolResources([]agent.Tool{tool}); err != nil {
		t.Fatal(err)
	}
	if !tool.closed {
		t.Fatal("managed tool was not closed")
	}
}

func TestSafeTerminalTextStripsControlSequences(t *testing.T) {
	got := safeTerminalText("visible\x1b]0;PWNED\a")
	if got != "visible" {
		t.Fatalf("safeTerminalText() = %q", got)
	}
}
