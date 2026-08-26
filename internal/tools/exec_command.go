package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/JH427/horizon-code/internal/model"
)

type ExecCommand struct {
	workspace      string
	maxOutputBytes int
	maxTimeout     time.Duration
	allowShell     bool
}

type execCommandArguments struct {
	Program        string   `json:"program"`
	Arguments      []string `json:"args"`
	WorkingDir     string   `json:"cwd,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Purpose        string   `json:"purpose"`
}

func NewExecCommand(workspace string, maxOutputBytes int, maxTimeout time.Duration) *ExecCommand {
	return newExecCommand(workspace, maxOutputBytes, maxTimeout, false)
}

func NewOperatorExecCommand(workspace string, maxOutputBytes int, maxTimeout time.Duration) *ExecCommand {
	return newExecCommand(workspace, maxOutputBytes, maxTimeout, true)
}

func newExecCommand(workspace string, maxOutputBytes int, maxTimeout time.Duration, allowShell bool) *ExecCommand {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 64 << 10
	}
	if maxTimeout <= 0 {
		maxTimeout = 2 * time.Minute
	}
	return &ExecCommand{workspace: workspace, maxOutputBytes: maxOutputBytes, maxTimeout: maxTimeout, allowShell: allowShell}
}

func (tool *ExecCommand) Definition() model.Tool {
	description := "Run a non-shell executable directly. Every arbitrary execution conservatively marks the workspace changed, including inspect; verify is accepted only for the hardened build/test/static-check allowlist. Shell programs are denied. The cwd is workspace-relative; non-shell executables are not sandboxed."
	if tool.allowShell {
		description = "Operator-authorized: run an executable directly, including shell programs. Every arbitrary execution conservatively marks the workspace changed, including inspect; verify is accepted only for the hardened build/test/static-check allowlist. The cwd is workspace-relative; executables are not sandboxed."
	}
	return model.Tool{
		Name:        "exec_command",
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"program":         map[string]any{"type": "string"},
				"args":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"cwd":             map[string]any{"type": "string", "description": "Optional workspace-relative existing directory"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": int(tool.maxTimeout.Seconds())},
				"purpose":         map[string]any{"type": "string", "enum": []string{"inspect", "modify", "verify"}},
			},
			"required":             []string{"program", "args", "purpose"},
			"additionalProperties": false,
		},
	}
}

func (tool *ExecCommand) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	var arguments execCommandArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode exec_command arguments: %w", err)
	}
	if arguments.Program == "" {
		return Result{}, errors.New("exec_command program is required")
	}
	if !tool.allowShell && isShellProgram(arguments.Program) {
		return Result{}, errors.New("exec_command shell programs require operator authorization")
	}
	if arguments.Purpose != "inspect" && arguments.Purpose != "modify" && arguments.Purpose != "verify" {
		return Result{}, errors.New("exec_command purpose must be inspect, modify, or verify")
	}
	verifiedCommand := false
	if arguments.Purpose == "verify" {
		if err := validateVerificationCommand(arguments.Program, arguments.Arguments); err != nil {
			return Result{}, fmt.Errorf("exec_command hardened verification rejected the command; use run_command with an allowlisted build/test/static-check command: %w", err)
		}
		verifiedCommand = programProvidesVerification(arguments.Program, arguments.Arguments)
		if !verifiedCommand {
			return Result{}, errors.New("exec_command hardened verification requires a build, test, or static-check command; use run_command")
		}
	}
	workingDirectory, err := commandWorkingDirectory(tool.workspace, arguments.WorkingDir)
	if err != nil {
		return Result{}, fmt.Errorf("exec_command cwd: %w", err)
	}
	timeout, err := commandTimeout(arguments.TimeoutSeconds, tool.maxTimeout)
	if err != nil {
		return Result{}, fmt.Errorf("exec_command: %w", err)
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, arguments.Program, arguments.Arguments...)
	command.Dir = workingDirectory
	configureProcessGroup(command)
	output := newBoundedBuffer(tool.maxOutputBytes)
	command.Stdout = output
	command.Stderr = output
	started := time.Now()
	runErr := command.Run()
	content := commandResult(arguments.Program, arguments.Arguments, runErr, time.Since(started), output)
	failureResult := resultForExecutionFailure(arguments.Purpose, content, command.Process != nil)
	if commandContext.Err() != nil {
		return failureResult, fmt.Errorf("exec_command cancelled or timed out: %w\n%s", commandContext.Err(), content)
	}
	if runErr != nil {
		return failureResult, fmt.Errorf("exec_command failed: %w\n%s", runErr, content)
	}
	if verifiedCommand {
		if err := validateVerificationOutput(arguments.Program, output.String()); err != nil {
			return Result{Content: content, Changed: true}, err
		}
		return Result{Content: content, Verified: true}, nil
	}
	return resultForPurpose(arguments.Purpose, content), nil
}

func isShellProgram(program string) bool {
	switch strings.ToLower(filepath.Base(filepath.Clean(program))) {
	case "bash", "sh", "zsh", "fish", "dash":
		return true
	default:
		return false
	}
}

func commandWorkingDirectory(workspace, requested string) (string, error) {
	if requested == "" {
		requested = "."
	}
	return resolveExistingWorkspacePath(workspace, requested)
}

func commandTimeout(seconds int, maximum time.Duration) (time.Duration, error) {
	if seconds <= 0 {
		return maximum, nil
	}
	requested := time.Duration(seconds) * time.Second
	if requested > maximum {
		return 0, fmt.Errorf("timeout %s exceeds maximum %s", requested, maximum)
	}
	return requested, nil
}

func configureProcessGroup(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.WaitDelay = 2 * time.Second
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
}

func commandResult(program string, arguments []string, runErr error, duration time.Duration, output *boundedBuffer) string {
	return fmt.Sprintf("COMMAND_RESULT\nprogram: %s\nargs: %q\nexit_code: %d\nduration: %s\ntruncated: %t\noutput:\n%s",
		program, arguments, exitCode(runErr), duration.Round(time.Millisecond), output.truncated, strings.TrimSpace(output.String()))
}

func resultForPurpose(purpose, content string) Result {
	return Result{Content: content, Changed: true}
}

func resultForExecutionFailure(purpose, content string, started bool) Result {
	return Result{Content: content, Changed: started}
}
