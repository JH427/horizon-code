package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/JH427/horizon-code/internal/model"
)

type Shell struct {
	workspace      string
	maxOutputBytes int
	maxTimeout     time.Duration
}

type shellArguments struct {
	Command        string `json:"command"`
	WorkingDir     string `json:"cwd,omitempty"`
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"`
	Purpose        string `json:"purpose"`
}

func NewShell(workspace string, maxOutputBytes int, maxTimeout time.Duration) *Shell {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 64 << 10
	}
	if maxTimeout <= 0 {
		maxTimeout = 2 * time.Minute
	}
	return &Shell{workspace: workspace, maxOutputBytes: maxOutputBytes, maxTimeout: maxTimeout}
}

func (tool *Shell) Definition() model.Tool {
	return model.Tool{
		Name:        "shell",
		Description: "Operator-only: run a command with bash -lc for inspection or modification. Every shell execution conservatively marks the workspace changed; use run_command for hardened verification. The cwd is workspace-relative; this does not sandbox the shell.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":         map[string]any{"type": "string"},
				"cwd":             map[string]any{"type": "string", "description": "Optional workspace-relative existing directory"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": int(tool.maxTimeout.Seconds())},
				"purpose":         map[string]any{"type": "string", "enum": []string{"inspect", "modify"}},
			},
			"required":             []string{"command", "purpose"},
			"additionalProperties": false,
		},
	}
}

func (tool *Shell) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	var arguments shellArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode shell arguments: %w", err)
	}
	if arguments.Command == "" {
		return Result{}, errors.New("shell command is required")
	}
	if arguments.Purpose == "verify" {
		return Result{}, errors.New("shell cannot provide hardened verification; use run_command")
	}
	if arguments.Purpose != "inspect" && arguments.Purpose != "modify" {
		return Result{}, errors.New("shell purpose must be inspect or modify")
	}
	workingDirectory, err := commandWorkingDirectory(tool.workspace, arguments.WorkingDir)
	if err != nil {
		return Result{}, fmt.Errorf("shell cwd: %w", err)
	}
	timeout, err := commandTimeout(arguments.TimeoutSeconds, tool.maxTimeout)
	if err != nil {
		return Result{}, fmt.Errorf("shell: %w", err)
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, "bash", "-lc", arguments.Command)
	command.Dir = workingDirectory
	configureProcessGroup(command)
	output := newBoundedBuffer(tool.maxOutputBytes)
	command.Stdout = output
	command.Stderr = output
	started := time.Now()
	runErr := command.Run()
	content := commandResult("bash", []string{"-lc", arguments.Command}, runErr, time.Since(started), output)
	failureResult := resultForExecutionFailure(arguments.Purpose, content, command.Process != nil)
	if commandContext.Err() != nil {
		return failureResult, fmt.Errorf("shell cancelled or timed out: %w\n%s", commandContext.Err(), content)
	}
	if runErr != nil {
		return failureResult, fmt.Errorf("shell failed: %w\n%s", runErr, content)
	}
	return resultForPurpose(arguments.Purpose, content), nil
}
