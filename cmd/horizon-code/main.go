package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/JH427/horizon-code/internal/agent"
	"github.com/JH427/horizon-code/internal/instructions"
	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/resources"
	"github.com/JH427/horizon-code/internal/server"
	"github.com/JH427/horizon-code/internal/session"
	"github.com/JH427/horizon-code/internal/tools"
	"github.com/JH427/horizon-code/internal/tui"
)

type appConfig struct {
	serverCommand string
	baseURL       string
	healthURL     string
	model         string
	workspace     string
	sessionRoot   string
	prompt        string
	startup       time.Duration
	shutdown      time.Duration
	capability    capabilityMode
}

type capabilityMode string

const (
	capabilitySafe      capabilityMode = "safe"
	capabilityWorkspace capabilityMode = "workspace"
	capabilityOperator  capabilityMode = "operator"
)

func main() {
	config, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := run(config, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "horizon-code:", err)
		os.Exit(1)
	}
}

func parseConfig(arguments []string) (appConfig, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return appConfig{}, fmt.Errorf("get current workspace: %w", err)
	}
	config := appConfig{}
	serverCommand, err := defaultServerCommand()
	if err != nil {
		return appConfig{}, err
	}
	sessionRoot, err := defaultSessionRoot()
	if err != nil {
		return appConfig{}, err
	}
	flags := flag.NewFlagSet("horizon-code", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	capability := string(capabilitySafe)
	flags.StringVar(&config.serverCommand, "server-command", serverCommand, "owned inference server launcher")
	flags.StringVar(&config.baseURL, "base-url", "http://127.0.0.1:8080/v1", "OpenAI-compatible API base URL")
	flags.StringVar(&config.healthURL, "health-url", "http://127.0.0.1:8080/health", "inference readiness URL")
	flags.StringVar(&config.model, "model", "qwen38-27b", "served model ID")
	flags.StringVar(&config.workspace, "workspace", workingDirectory, "workspace available to coding tools")
	flags.StringVar(&config.sessionRoot, "session-dir", sessionRoot, "directory for private session logs")
	flags.StringVar(&config.prompt, "prompt", "", "run one headless prompt and exit")
	flags.DurationVar(&config.startup, "startup-timeout", 10*time.Minute, "inference readiness timeout")
	flags.DurationVar(&config.shutdown, "shutdown-timeout", 30*time.Second, "inference shutdown timeout")
	flags.StringVar(&capability, "capability", capability, "tool capability: safe, workspace, or operator")
	if err := flags.Parse(arguments); err != nil {
		return appConfig{}, err
	}
	config.capability = capabilityMode(capability)
	switch config.capability {
	case capabilitySafe, capabilityWorkspace, capabilityOperator:
	default:
		return appConfig{}, fmt.Errorf("unknown capability %q; expected safe, workspace, or operator", capability)
	}
	workspace, err := filepath.Abs(config.workspace)
	if err != nil {
		return appConfig{}, fmt.Errorf("resolve workspace: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil {
		return appConfig{}, fmt.Errorf("inspect workspace: %w", err)
	}
	if !info.IsDir() {
		return appConfig{}, fmt.Errorf("workspace is not a directory: %s", workspace)
	}
	config.workspace = workspace
	config.sessionRoot, err = filepath.Abs(config.sessionRoot)
	if err != nil {
		return appConfig{}, fmt.Errorf("resolve session directory: %w", err)
	}
	return config, nil
}

func defaultServerCommand() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for server launcher: %w", err)
	}
	return filepath.Join(home, ".config", "horizon-llm", "run-server.sh"), nil
}

func defaultSessionRoot() (string, error) {
	if stateHome := os.Getenv("XDG_STATE_HOME"); stateHome != "" {
		return filepath.Join(stateHome, "horizon-code", "sessions"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for sessions: %w", err)
	}
	return filepath.Join(home, ".local", "state", "horizon-code", "sessions"), nil
}

func run(config appConfig, output, errorOutput io.Writer) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	healthClient := &http.Client{Timeout: 2 * time.Second}
	manager := server.NewManager(server.Config{
		Command:         config.serverCommand,
		HealthURL:       config.healthURL,
		StartupTimeout:  config.startup,
		ShutdownTimeout: config.shutdown,
	}, healthClient)
	fmt.Fprintf(errorOutput, "Capability: %s\n", config.capability)
	fmt.Fprintf(errorOutput, "Starting %s…\n", config.model)
	if err := manager.Start(ctx); err != nil {
		return err
	}
	defer func() {
		fmt.Fprintln(errorOutput, "Stopping inference server…")
		if err := manager.Close(); err != nil {
			fmt.Fprintln(errorOutput, "inference shutdown error:", err)
		}
	}()

	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 2 * time.Minute,
		IdleConnTimeout:       90 * time.Second,
	}
	modelClient := model.NewClient(model.Config{
		BaseURL:          config.baseURL,
		Model:            config.model,
		Temperature:      1.0,
		TopP:             0.95,
		TopK:             20,
		MaxTokens:        16384,
		ReasoningEffort:  "medium",
		EnableThinking:   true,
		PreserveThinking: true,
	}, &http.Client{Transport: transport})
	verifyContext, cancelVerify := context.WithTimeout(ctx, 10*time.Second)
	defer cancelVerify()
	if err := modelClient.Verify(verifyContext); err != nil {
		return fmt.Errorf("verify served model: %w", err)
	}
	availableTools := workspaceTools(config.workspace, isGitWorkspace(config.workspace), config.capability)
	defer func() {
		if err := closeToolResources(availableTools); err != nil {
			fmt.Fprintln(errorOutput, "tool cleanup error:", err)
		}
	}()
	budget := agentBudget(config.capability)
	engine := agent.NewEngineWithBudget(modelClient, availableTools, budget)
	engine.SetCapabilityPolicy(capabilityInstructions(config.capability))
	instructionsFound, err := configureWorkspaceInstructions(engine, config.workspace)
	if err != nil {
		return err
	}
	if instructionsFound {
		fmt.Fprintln(errorOutput, "Loaded hierarchical HORIZON.md workspace instructions.")
	}
	store, err := session.NewStore(config.sessionRoot, session.Metadata{
		Model: config.model, Workspace: config.workspace,
	})
	if err != nil {
		return err
	}
	defer func() {
		if err := store.Close(); err != nil {
			fmt.Fprintln(errorOutput, "session close error:", err)
		}
	}()
	recordedEngine := session.NewRecordingRunner(engine, store)

	if config.prompt != "" {
		_, err := recordedEngine.Run(ctx, config.prompt, func(event agent.Event) {
			switch event.Kind {
			case agent.EventReasoningDelta:
				fmt.Fprint(errorOutput, safeTerminalText(event.Text))
			case agent.EventAssistantDelta:
				fmt.Fprint(output, safeTerminalText(event.Text))
			case agent.EventToolStarted:
				fmt.Fprintf(errorOutput, "\ntool: %s [%s]\narguments: %s\n", safeTerminalText(event.ToolName), safeTerminalText(event.CallID), safeTerminalText(event.Arguments))
			case agent.EventToolCompleted:
				fmt.Fprintf(errorOutput, "tool result: %s [%s]\n%s\n", safeTerminalText(event.ToolName), safeTerminalText(event.CallID), safeTerminalText(event.Text))
			case agent.EventToolFailed:
				fmt.Fprintf(errorOutput, "tool failed: %s: %s\n", safeTerminalText(event.ToolName), safeTerminalText(event.Err.Error()))
			}
		})
		if err == nil {
			fmt.Fprintln(output)
		}
		return err
	}

	interfaceModel := tui.NewModel(recordedEngine, config.model, config.workspace)
	interfaceModel.SetServedContext(modelClient.ContextSize())
	interfaceModel.SetActiveContextLimit(budget.MaxContextTokens)
	interfaceModel.SetCapability(string(config.capability))
	displayedTools := make([]string, 0, len(availableTools))
	for _, availableTool := range availableTools {
		displayedTools = append(displayedTools, availableTool.Definition().Name)
	}
	interfaceModel.SetAvailableTools(displayedTools)
	resourceSampler := resources.NewLinuxSampler("/proc/meminfo", "/sys/class/drm")
	interfaceModel.SetResourceSampler(resourceSampler)
	program := tea.NewProgram(interfaceModel, tea.WithContext(ctx))
	_, err = program.Run()
	return err
}

type workspaceInstructionTarget interface {
	SetWorkspaceInstructions(string)
}

func configureWorkspaceInstructions(target workspaceInstructionTarget, workspace string) (bool, error) {
	document, err := instructions.Load(workspace, 64<<10)
	if err != nil {
		return false, fmt.Errorf("load workspace instructions: %w", err)
	}
	if len(document.Paths) == 0 {
		return false, nil
	}
	target.SetWorkspaceInstructions(document.Content)
	return true, nil
}

func capabilityInstructions(capability capabilityMode) string {
	switch capability {
	case capabilityWorkspace:
		return "Workspace autonomy is enabled. Use apply_patch and path tools for filesystem changes, exec_command for broad structured project commands, and background_process for managed long-running jobs. The dedicated shell tool is not exposed, but direct executables, interpreters, and wrappers are not sandboxed and may have all permissions of the current user. Arbitrary command execution conservatively marks the workspace changed even when used for inspection. Use run_command, or exec_command with an accepted build/test/static-check program, for hardened verification. Keep task-owned writes inside the selected workspace and stop background jobs when no longer needed."
	case capabilityOperator:
		return "Operator autonomy is explicitly enabled. You may use apply_patch and path tools, exec_command, the unrestricted shell, network access, package managers, Git writes, and background_process when needed to finish the task. Arbitrary exec_command, shell, and background execution conservatively marks the workspace changed, including inspection. Shell and background_process cannot self-certify verification: use run_command, or exec_command with an accepted build/test/static-check program, after the latest mutation. Preserve user work, avoid needless destructive actions, and stop every background job when finished."
	default:
		return ""
	}
}

func agentBudget(capability capabilityMode) agent.Budget {
	budget := agent.Budget{
		MaxSteps: 16, MaxToolCalls: 32, MaxRepairs: 8,
		MaxToolResultBytes: 128 << 10, MaxContextTokens: 48 * 1024,
		CompactionKeepRecentRounds: 4, MaxCompactionSummaryBytes: 8 << 10,
	}
	switch capability {
	case capabilityWorkspace:
		budget.MaxSteps = 24
		budget.MaxToolCalls = 64
		budget.MaxRepairs = 12
		budget.MaxToolResultBytes = 256 << 10
		budget.CompactionKeepRecentRounds = 6
		budget.MaxCompactionSummaryBytes = 12 << 10
	case capabilityOperator:
		budget.MaxSteps = 32
		budget.MaxToolCalls = 96
		budget.MaxRepairs = 16
		budget.MaxToolResultBytes = 384 << 10
		budget.CompactionKeepRecentRounds = 8
		budget.MaxCompactionSummaryBytes = 16 << 10
	}
	return budget
}

func workspaceTools(workspace string, includeGit bool, capability capabilityMode) []agent.Tool {
	available := []agent.Tool{
		tools.NewListFiles(workspace, 200),
		tools.NewReadFile(workspace, 500),
		tools.NewSearchCode(workspace, 100, 64<<10),
		tools.NewEditFile(workspace, 4<<20),
		tools.NewCreateFile(workspace, 4<<20),
		tools.NewRunCommandForWorkspace(workspace, 128<<10, 2*time.Minute, includeGit),
	}
	if includeGit {
		available = append(available, tools.NewGitDiff(workspace, 128<<10))
	}
	if capability == capabilitySafe {
		return available
	}

	available = append(available,
		tools.NewApplyPatch(workspace, 4<<20),
		tools.NewMakeDirectory(workspace),
		tools.NewMovePath(workspace),
		tools.NewDeletePath(workspace),
	)
	if capability == capabilityOperator {
		available = append(available,
			tools.NewOperatorExecCommand(workspace, 128<<10, 10*time.Minute),
			tools.NewOperatorBackgroundProcess(workspace, 128<<10, 30*time.Minute),
			tools.NewShell(workspace, 128<<10, 10*time.Minute),
		)
		return available
	}
	available = append(available,
		tools.NewExecCommand(workspace, 128<<10, 10*time.Minute),
		tools.NewBackgroundProcess(workspace, 128<<10, 30*time.Minute),
	)
	return available
}

func closeToolResources(available []agent.Tool) error {
	var closeErrors []error
	for _, tool := range available {
		closer, ok := tool.(interface{ Close() error })
		if !ok {
			continue
		}
		if err := closer.Close(); err != nil {
			closeErrors = append(closeErrors, fmt.Errorf("close tool %s: %w", tool.Definition().Name, err))
		}
	}
	return errors.Join(closeErrors...)
}

func isGitWorkspace(workspace string) bool {
	command := exec.Command("git", "-C", workspace, "rev-parse", "--is-inside-work-tree")
	return command.Run() == nil
}

func safeTerminalText(value string) string {
	return ansi.Strip(value)
}
