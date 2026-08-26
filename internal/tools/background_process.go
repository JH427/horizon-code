package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/JH427/horizon-code/internal/model"
)

const maxManagedProcesses = 64

type BackgroundProcess struct {
	workspace      string
	maxOutputBytes int
	maxTimeout     time.Duration
	allowShell     bool

	mu        sync.Mutex
	processes map[string]*managedProcess
	closed    bool
	sequence  atomic.Uint64
}

type backgroundProcessArguments struct {
	Operation      string   `json:"operation"`
	ID             string   `json:"id,omitempty"`
	Program        string   `json:"program,omitempty"`
	Arguments      []string `json:"args,omitempty"`
	WorkingDir     string   `json:"cwd,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	Purpose        string   `json:"purpose,omitempty"`
}

type managedProcess struct {
	id      string
	program string
	args    []string
	purpose string
	command *exec.Cmd
	output  *rollingBuffer
	done    chan struct{}

	mu       sync.Mutex
	waitErr  error
	exited   bool
	timedOut bool
}

type rollingBuffer struct {
	mu        sync.Mutex
	content   []byte
	limit     int
	truncated bool
}

func NewBackgroundProcess(workspace string, maxOutputBytes int, maxTimeout time.Duration) *BackgroundProcess {
	return newBackgroundProcess(workspace, maxOutputBytes, maxTimeout, false)
}

func NewOperatorBackgroundProcess(workspace string, maxOutputBytes int, maxTimeout time.Duration) *BackgroundProcess {
	return newBackgroundProcess(workspace, maxOutputBytes, maxTimeout, true)
}

func newBackgroundProcess(workspace string, maxOutputBytes int, maxTimeout time.Duration, allowShell bool) *BackgroundProcess {
	if maxOutputBytes <= 0 {
		maxOutputBytes = 64 << 10
	}
	if maxTimeout <= 0 {
		maxTimeout = 30 * time.Minute
	}
	return &BackgroundProcess{
		workspace: workspace, maxOutputBytes: maxOutputBytes, maxTimeout: maxTimeout, allowShell: allowShell,
		processes: make(map[string]*managedProcess),
	}
}

func (tool *BackgroundProcess) Definition() model.Tool {
	description := "Start, poll, stop, or list managed non-shell background processes. Shell programs are denied. start requires program, args, and purpose; poll/stop require id; list needs only operation. Start uses a workspace-relative cwd; non-shell executables are not sandboxed. At most 64 entries are retained, and exited history is pruned before start. Poll returns bounded rolling output."
	if tool.allowShell {
		description = "Operator-authorized: start, poll, stop, or list managed background processes, including shell programs. start requires program, args, and purpose; poll/stop require id; list needs only operation. Start uses a workspace-relative cwd; executables are not sandboxed. At most 64 entries are retained, and exited history is pruned before start. Poll returns bounded rolling output."
	}
	return model.Tool{
		Name:        "background_process",
		Description: description,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"operation":       map[string]any{"type": "string", "enum": []string{"start", "poll", "stop", "list"}},
				"id":              map[string]any{"type": "string"},
				"program":         map[string]any{"type": "string"},
				"args":            map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"cwd":             map[string]any{"type": "string", "description": "Optional workspace-relative existing directory"},
				"timeout_seconds": map[string]any{"type": "integer", "minimum": 1, "maximum": int(tool.maxTimeout.Seconds())},
				"purpose":         map[string]any{"type": "string", "enum": []string{"inspect", "modify"}},
			},
			"required":             []string{"operation"},
			"additionalProperties": false,
		},
	}
}

func (tool *BackgroundProcess) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	var arguments backgroundProcessArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode background_process arguments: %w", err)
	}
	switch arguments.Operation {
	case "start":
		return tool.start(ctx, arguments)
	case "poll":
		return tool.poll(arguments.ID)
	case "stop":
		return tool.stop(arguments.ID)
	case "list":
		return tool.list(), nil
	default:
		return Result{}, errors.New("background_process operation must be start, poll, stop, or list")
	}
}

func (tool *BackgroundProcess) start(ctx context.Context, arguments backgroundProcessArguments) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("background_process start cancelled: %w", err)
	}
	if arguments.Program == "" {
		return Result{}, errors.New("background_process start requires program")
	}
	if !tool.allowShell && isShellProgram(arguments.Program) {
		return Result{}, errors.New("background_process shell programs require operator authorization")
	}
	if arguments.Purpose == "verify" {
		return Result{}, errors.New("background_process cannot provide hardened verification; wait for it to exit, then use run_command")
	}
	if arguments.Purpose != "inspect" && arguments.Purpose != "modify" {
		return Result{}, errors.New("background_process start purpose must be inspect or modify")
	}
	workingDirectory, err := commandWorkingDirectory(tool.workspace, arguments.WorkingDir)
	if err != nil {
		return Result{}, fmt.Errorf("background_process cwd: %w", err)
	}
	timeout, err := commandTimeout(arguments.TimeoutSeconds, tool.maxTimeout)
	if err != nil {
		return Result{}, fmt.Errorf("background_process: %w", err)
	}

	tool.mu.Lock()
	if tool.closed {
		tool.mu.Unlock()
		return Result{}, errors.New("background_process is closed")
	}
	tool.pruneExitedLocked()
	if len(tool.processes) >= maxManagedProcesses {
		tool.mu.Unlock()
		return Result{}, fmt.Errorf("background_process active process limit is %d", maxManagedProcesses)
	}
	id := fmt.Sprintf("bg-%d-%d", time.Now().UnixNano(), tool.sequence.Add(1))
	command := exec.Command(arguments.Program, arguments.Arguments...)
	command.Dir = workingDirectory
	configureProcessGroup(command)
	command.Cancel = nil // managed explicitly by stop, timeout, and Close
	output := newRollingBuffer(tool.maxOutputBytes)
	command.Stdout = output
	command.Stderr = output
	process := &managedProcess{
		id: id, program: arguments.Program, args: append([]string(nil), arguments.Arguments...),
		purpose: arguments.Purpose, command: command, output: output, done: make(chan struct{}),
	}
	if err := command.Start(); err != nil {
		tool.mu.Unlock()
		return Result{}, fmt.Errorf("background_process start %q: %w", arguments.Program, err)
	}
	tool.processes[id] = process
	tool.mu.Unlock()

	go process.wait()
	go process.enforceTimeout(timeout)
	content := fmt.Sprintf("BACKGROUND_PROCESS\nid: %s\nstatus: running\nprogram: %s\nargs: %q", id, arguments.Program, arguments.Arguments)
	return Result{Content: content, Changed: true}, nil
}

func (tool *BackgroundProcess) pruneExitedLocked() {
	for id, process := range tool.processes {
		process.mu.Lock()
		exited := process.exited
		process.mu.Unlock()
		if exited {
			delete(tool.processes, id)
		}
	}
}

func (process *managedProcess) wait() {
	err := process.command.Wait()
	process.mu.Lock()
	process.waitErr = err
	process.exited = true
	process.mu.Unlock()
	close(process.done)
}

func (process *managedProcess) enforceTimeout(timeout time.Duration) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-process.done:
		return
	case <-timer.C:
		process.mu.Lock()
		process.timedOut = true
		process.mu.Unlock()
		_ = killProcessGroup(process.command.Process.Pid, syscall.SIGKILL)
	}
}

func (tool *BackgroundProcess) poll(id string) (Result, error) {
	process, err := tool.process(id)
	if err != nil {
		return Result{}, err
	}
	content, _, _ := process.snapshot()
	return Result{
		Content:  content,
		Changed:  true,
		Verified: false,
	}, nil
}

func (tool *BackgroundProcess) stop(id string) (Result, error) {
	process, err := tool.process(id)
	if err != nil {
		return Result{}, err
	}
	process.mu.Lock()
	exited := process.exited
	process.mu.Unlock()
	if !exited {
		if err := killProcessGroup(process.command.Process.Pid, syscall.SIGKILL); err != nil {
			return Result{}, fmt.Errorf("stop background process %s: %w", id, err)
		}
		<-process.done
	}
	content, _, _ := process.snapshot()
	return Result{Content: content, Changed: true}, nil
}

func (tool *BackgroundProcess) list() Result {
	tool.mu.Lock()
	ids := make([]string, 0, len(tool.processes))
	for id := range tool.processes {
		ids = append(ids, id)
	}
	tool.mu.Unlock()
	sort.Strings(ids)
	var content strings.Builder
	content.WriteString("BACKGROUND_PROCESSES")
	for _, id := range ids {
		process, err := tool.process(id)
		if err != nil {
			continue
		}
		process.mu.Lock()
		status := "running"
		if process.exited {
			status = "exited"
		}
		process.mu.Unlock()
		fmt.Fprintf(&content, "\nid: %s status: %s program: %s", id, status, process.program)
	}
	return Result{Content: content.String()}
}

func (tool *BackgroundProcess) process(id string) (*managedProcess, error) {
	if id == "" {
		return nil, errors.New("background_process operation requires id")
	}
	tool.mu.Lock()
	process := tool.processes[id]
	tool.mu.Unlock()
	if process == nil {
		return nil, fmt.Errorf("background process %q not found", id)
	}
	return process, nil
}

func (process *managedProcess) snapshot() (string, bool, bool) {
	process.mu.Lock()
	exited := process.exited
	waitErr := process.waitErr
	timedOut := process.timedOut
	process.mu.Unlock()
	output, truncated := process.output.snapshot()
	status := "running"
	code := -1
	if exited {
		status = "exited"
		code = exitCode(waitErr)
	}
	content := fmt.Sprintf("BACKGROUND_PROCESS\nid: %s\nstatus: %s\nprogram: %s\nargs: %q\nexit_code: %d\ntimed_out: %t\ntruncated: %t\noutput:\n%s",
		process.id, status, process.program, process.args, code, timedOut, truncated, strings.TrimSpace(output))
	return content, exited && waitErr == nil && !timedOut, exited
}

func (tool *BackgroundProcess) Close() error {
	tool.mu.Lock()
	if tool.closed {
		tool.mu.Unlock()
		return nil
	}
	tool.closed = true
	processes := make([]*managedProcess, 0, len(tool.processes))
	for _, process := range tool.processes {
		processes = append(processes, process)
	}
	tool.mu.Unlock()

	var closeErrors []error
	for _, process := range processes {
		process.mu.Lock()
		exited := process.exited
		process.mu.Unlock()
		if !exited {
			if err := killProcessGroup(process.command.Process.Pid, syscall.SIGKILL); err != nil {
				closeErrors = append(closeErrors, fmt.Errorf("stop %s: %w", process.id, err))
				continue
			}
			<-process.done
		}
	}
	return errors.Join(closeErrors...)
}

func killProcessGroup(pid int, signal syscall.Signal) error {
	err := syscall.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

func newRollingBuffer(limit int) *rollingBuffer {
	return &rollingBuffer{limit: limit}
}

func (buffer *rollingBuffer) Write(content []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	length := len(content)
	if length >= buffer.limit {
		buffer.content = append(buffer.content[:0], content[length-buffer.limit:]...)
		buffer.truncated = true
		return length, nil
	}
	if overflow := len(buffer.content) + length - buffer.limit; overflow > 0 {
		copy(buffer.content, buffer.content[overflow:])
		buffer.content = buffer.content[:len(buffer.content)-overflow]
		buffer.truncated = true
	}
	buffer.content = append(buffer.content, content...)
	return length, nil
}

func (buffer *rollingBuffer) snapshot() (string, bool) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.content), buffer.truncated
}
