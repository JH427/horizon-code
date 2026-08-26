package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExecCommandRunsProgramWithoutShellAndMarksPurpose(t *testing.T) {
	tool := NewExecCommand(t.TempDir(), 1024, time.Second)
	result, err := tool.Run(context.Background(), []byte(`{"program":"printf","args":["hello"],"purpose":"modify"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.Changed || result.Verified {
		t.Fatalf("result flags = changed %t, verified %t", result.Changed, result.Verified)
	}
	if !strings.Contains(result.Content, "hello") || !strings.Contains(result.Content, "exit_code: 0") {
		t.Fatalf("result content = %q", result.Content)
	}
}

func TestExecCommandUsesWorkspaceRelativeCWDAndRejectsTraversal(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	tool := NewExecCommand(workspace, 1024, time.Second)
	result, err := tool.Run(context.Background(), []byte(`{"program":"pwd","args":[],"cwd":"sub","purpose":"inspect"}`))
	if err != nil || !strings.Contains(result.Content, filepath.Join(workspace, "sub")) {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if _, err := tool.Run(context.Background(), []byte(`{"program":"pwd","args":[],"cwd":"..","purpose":"inspect"}`)); err == nil {
		t.Fatal("Run() accepted cwd traversal")
	}
}

func TestExecCommandBoundsOutput(t *testing.T) {
	tool := NewExecCommand(t.TempDir(), 32, time.Second)
	result, err := tool.Run(context.Background(), []byte(`{"program":"printf","args":["abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"],"purpose":"inspect"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "truncated: true") {
		t.Fatalf("result content = %q", result.Content)
	}
	output := strings.SplitN(result.Content, "output:\n", 2)[1]
	if len(output) > 32 {
		t.Fatalf("captured output length = %d, want <= 32", len(output))
	}
}

func TestExecCommandFailurePreservesModifyChangedButNeverVerification(t *testing.T) {
	tool := NewExecCommand(t.TempDir(), 1024, time.Second)
	modified, err := tool.Run(context.Background(), []byte(`{"program":"false","args":[],"purpose":"modify"}`))
	if err == nil || !modified.Changed || modified.Verified || !strings.Contains(modified.Content, "exit_code: 1") {
		t.Fatalf("modify result = %#v, error = %v", modified, err)
	}
	verified, err := tool.Run(context.Background(), []byte(`{"program":"false","args":[],"purpose":"verify"}`))
	if err == nil || verified.Changed || verified.Verified {
		t.Fatalf("verify result = %#v, error = %v", verified, err)
	}
}

func TestExecCommandDeniesShellProgramsUnlessOperatorAuthorized(t *testing.T) {
	workspace := t.TempDir()
	for _, program := range []string{"bash", "sh", "/bin/bash", "/usr/bin/dash", "zsh", "fish"} {
		tool := NewExecCommand(workspace, 1024, time.Second)
		raw := []byte(`{"program":"` + program + `","args":["-c","true"],"purpose":"inspect"}`)
		if _, err := tool.Run(context.Background(), raw); err == nil {
			t.Fatalf("default exec accepted shell program %q", program)
		}
	}
	operator := NewOperatorExecCommand(workspace, 1024, time.Second)
	if _, err := operator.Run(context.Background(), []byte(`{"program":"bash","args":["-c","true"],"purpose":"inspect"}`)); err != nil {
		t.Fatalf("operator exec rejected shell: %v", err)
	}
}

func TestExecCommandCancellationKillsChildProcessGroupAndPreservesChanged(t *testing.T) {
	tool := NewOperatorExecCommand(t.TempDir(), 1024, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	result, err := tool.Run(ctx, []byte(`{"program":"bash","args":["-c","sleep 30 & echo $!; wait"],"purpose":"modify"}`))
	if err == nil || !strings.Contains(err.Error(), "canceled") || !result.Changed || result.Verified {
		t.Fatalf("Run() result = %#v, error = %v", result, err)
	}
	waitForProcessGone(t, childPIDFromOutput(t, result.Content))
}

func TestExecCommandTimeoutKillsChildProcessGroup(t *testing.T) {
	tool := NewOperatorExecCommand(t.TempDir(), 1024, 2*time.Second)
	_, err := tool.Run(context.Background(), []byte(`{"program":"bash","args":["-c","sleep 30 & echo $!; wait"],"timeout_seconds":1,"purpose":"inspect"}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Run() error = %v, want timeout", err)
	}
	pid := childPIDFromOutput(t, err.Error())
	waitForProcessGone(t, pid)
}

func childPIDFromOutput(t *testing.T, content string) int {
	t.Helper()
	output := strings.SplitN(content, "output:\n", 2)
	if len(output) != 2 {
		t.Fatalf("no output in %q", content)
	}
	fields := strings.Fields(output[1])
	if len(fields) == 0 {
		t.Fatalf("empty output in %q", content)
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		t.Fatalf("parse child pid %q: %v", fields[0], err)
	}
	return pid
}

func waitForProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d remains after process-group cleanup", pid)
}
