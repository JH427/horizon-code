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

type RunCommand struct {
	workspace      string
	maxOutputBytes int
	maxTimeout     time.Duration
	allowGit       bool
}

type runCommandArguments struct {
	Program        string   `json:"program"`
	Arguments      []string `json:"args,omitempty"`
	WorkingDir     string   `json:"working_dir,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

func NewRunCommand(workspace string, maxOutputBytes int, maxTimeout time.Duration) *RunCommand {
	return NewRunCommandForWorkspace(workspace, maxOutputBytes, maxTimeout, true)
}

func NewRunCommandForWorkspace(workspace string, maxOutputBytes int, maxTimeout time.Duration, allowGit bool) *RunCommand {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 64 << 10
	}
	if maxTimeout <= 0 {
		maxTimeout = 2 * time.Minute
	}
	return &RunCommand{workspace: workspace, maxOutputBytes: maxOutputBytes, maxTimeout: maxTimeout, allowGit: allowGit}
}

func (tool *RunCommand) Definition() model.Tool {
	programs := []string{"go", "cargo", "pytest", "npm"}
	description := "Run one allowlisted build, test, or static-check command without a shell. Successful completion counts as verification."
	if tool.allowGit {
		programs = append([]string{"git"}, programs...)
		description = "Run one allowlisted build, test, static-check, or read-only git command without a shell. Successful completion counts as verification."
	}
	return model.Tool{
		Name:        "run_command",
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"program":         map[string]any{"type": "string", "enum": programs},
				"args":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"working_dir":     map[string]any{"type": "string", "description": "Optional workspace-relative directory"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": int(tool.maxTimeout.Seconds())},
			},
			"required":             []string{"program", "args"},
			"additionalProperties": false,
		},
	}
}

func (tool *RunCommand) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	var arguments runCommandArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode run_command arguments: %w", err)
	}
	if arguments.Program == "git" && !tool.allowGit {
		return Result{}, errors.New("run_command git is not available because the workspace is not a Git worktree")
	}
	if err := validateVerificationCommand(arguments.Program, arguments.Arguments); err != nil {
		return Result{}, err
	}
	workingDirectory := tool.workspace
	if arguments.WorkingDir != "" {
		resolved, err := resolveExistingWorkspacePath(tool.workspace, arguments.WorkingDir)
		if err != nil {
			return Result{}, err
		}
		workingDirectory = resolved
	}
	info, err := filepath.Abs(workingDirectory)
	if err != nil {
		return Result{}, fmt.Errorf("resolve command working directory: %w", err)
	}
	workingDirectory = info

	timeout := tool.maxTimeout
	if arguments.TimeoutSeconds > 0 {
		requested := time.Duration(arguments.TimeoutSeconds) * time.Second
		if requested > tool.maxTimeout {
			return Result{}, fmt.Errorf("run_command timeout %s exceeds maximum %s", requested, tool.maxTimeout)
		}
		timeout = requested
	}
	commandContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(commandContext, arguments.Program, arguments.Arguments...)
	command.Dir = workingDirectory
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
	output := newBoundedBuffer(tool.maxOutputBytes)
	command.Stdout = output
	command.Stderr = output
	started := time.Now()
	err = command.Run()
	duration := time.Since(started)
	content := fmt.Sprintf(
		"COMMAND_RESULT\nprogram: %s\nargs: %q\nexit_code: %d\nduration: %s\ntruncated: %t\noutput:\n%s",
		arguments.Program, arguments.Arguments, exitCode(err), duration.Round(time.Millisecond), output.truncated, strings.TrimSpace(output.String()),
	)
	if commandContext.Err() != nil {
		return Result{}, fmt.Errorf("run_command cancelled or timed out: %w\n%s", commandContext.Err(), content)
	}
	if err != nil {
		return Result{}, fmt.Errorf("run_command failed: %w\n%s", err, content)
	}
	return Result{Content: content, Verified: programProvidesVerification(arguments.Program, arguments.Arguments)}, nil
}

func programProvidesVerification(program string, arguments []string) bool {
	if len(arguments) == 0 || program == "git" {
		return false
	}
	return true
}

func validateVerificationCommand(program string, arguments []string) error {
	if strings.Contains(program, "/") || strings.Contains(program, "\\") {
		return errors.New("run_command program must be an allowlisted executable name, not a path")
	}
	if len(arguments) == 0 {
		return errors.New("run_command requires a subcommand or test arguments")
	}
	if err := validateCommandArgumentsStayInWorkspace(arguments); err != nil {
		return err
	}
	if err := validateCommandProvidesVerification(program, arguments); err != nil {
		return err
	}
	first := arguments[0]
	switch program {
	case "git":
		if first == "status" || first == "diff" || first == "show" || first == "log" {
			return nil
		}
	case "go":
		if first == "test" || first == "vet" || first == "build" {
			return nil
		}
	case "cargo":
		if first == "test" || first == "check" || first == "build" {
			return nil
		}
	case "pytest":
		return nil
	case "npm":
		if first == "test" {
			return nil
		}
		if first == "run" && len(arguments) > 1 {
			script := arguments[1]
			if script == "test" || script == "lint" || script == "check" || script == "build" {
				return nil
			}
		}
	}
	return fmt.Errorf("run_command rejects %q with arguments %q; only build, test, static-check, and read-only git commands are allowed", program, arguments)
}

func validateCommandArgumentsStayInWorkspace(arguments []string) error {
	dangerousOptions := []string{
		"-exec", "-toolexec", "-overlay", "-modfile", "-o",
		"--output", "--target-dir", "--ext-diff", "--textconv", "--git-dir", "--work-tree",
		"--manifest-path", "--config", "--prefix", "--rootdir", "--confcutdir", "--basetemp",
	}
	for _, argument := range arguments {
		if filepath.IsAbs(argument) {
			return fmt.Errorf("run_command rejects absolute-path argument %q", argument)
		}
		if separator := strings.IndexByte(argument, '='); separator >= 0 {
			value := argument[separator+1:]
			cleanedValue := filepath.Clean(value)
			if filepath.IsAbs(value) || cleanedValue == ".." || strings.HasPrefix(cleanedValue, ".."+string(filepath.Separator)) {
				return fmt.Errorf("run_command option value escapes the workspace: %q", argument)
			}
		}
		cleaned := filepath.Clean(argument)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return fmt.Errorf("run_command argument escapes the workspace: %q", argument)
		}
		for _, option := range dangerousOptions {
			if argument == option || strings.HasPrefix(argument, option+"=") {
				return fmt.Errorf("run_command rejects option %q because it can execute or write outside the workspace", argument)
			}
		}
	}
	return nil
}

func validateCommandProvidesVerification(program string, arguments []string) error {
	for _, argument := range arguments {
		switch argument {
		case "-h", "--help", "-V", "--version":
			return fmt.Errorf("run_command rejects informational option %q because it does not verify the workspace", argument)
		}
	}
	if program == "go" {
		for _, argument := range arguments[1:] {
			if argument == "-n" {
				return errors.New("run_command rejects go -n because it prints commands without executing verification")
			}
		}
	}
	if program == "pytest" {
		for _, argument := range arguments {
			switch argument {
			case "--collect-only", "--collectonly", "--co", "--setup-plan", "--fixtures", "--markers":
				return fmt.Errorf("run_command rejects pytest option %q because it does not execute tests", argument)
			}
		}
	}
	return nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		return exitError.ExitCode()
	}
	return -1
}
