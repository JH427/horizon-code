package main

import (
	"context"
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
}

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
	flags.StringVar(&config.serverCommand, "server-command", serverCommand, "owned inference server launcher")
	flags.StringVar(&config.baseURL, "base-url", "http://127.0.0.1:8080/v1", "OpenAI-compatible API base URL")
	flags.StringVar(&config.healthURL, "health-url", "http://127.0.0.1:8080/health", "inference readiness URL")
	flags.StringVar(&config.model, "model", "qwen38-27b", "served model ID")
	flags.StringVar(&config.workspace, "workspace", workingDirectory, "workspace available to coding tools")
	flags.StringVar(&config.sessionRoot, "session-dir", sessionRoot, "directory for private session logs")
	flags.StringVar(&config.prompt, "prompt", "", "run one headless prompt and exit")
	flags.DurationVar(&config.startup, "startup-timeout", 10*time.Minute, "inference readiness timeout")
	flags.DurationVar(&config.shutdown, "shutdown-timeout", 30*time.Second, "inference shutdown timeout")
	if err := flags.Parse(arguments); err != nil {
		return appConfig{}, err
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
	engine := agent.NewEngine(modelClient, workspaceTools(config.workspace, isGitWorkspace(config.workspace)), 16)
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

func workspaceTools(workspace string, includeGit bool) []agent.Tool {
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
	return available
}

func isGitWorkspace(workspace string) bool {
	command := exec.Command("git", "-C", workspace, "rev-parse", "--is-inside-work-tree")
	return command.Run() == nil
}

func safeTerminalText(value string) string {
	return ansi.Strip(value)
}
