package tools

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBackgroundProcessPrunesExitedHistoryBeforeStart(t *testing.T) {
	tool := NewBackgroundProcess(t.TempDir(), 1024, time.Second)
	oldDone := make(chan struct{})
	close(oldDone)
	tool.processes["old"] = &managedProcess{id: "old", exited: true, output: newRollingBuffer(16), done: oldDone}
	result, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"true","args":[],"purpose":"inspect"}`))
	if err != nil {
		t.Fatalf("start error = %v", err)
	}
	if !strings.Contains(result.Content, "status: running") {
		t.Fatalf("result = %q", result.Content)
	}
	tool.mu.Lock()
	_, retained := tool.processes["old"]
	tool.mu.Unlock()
	if retained {
		t.Fatal("exited history entry was not pruned")
	}
	if err := tool.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundProcessRejectsStartAtActiveRetentionLimit(t *testing.T) {
	tool := NewBackgroundProcess(t.TempDir(), 1024, time.Second)
	for index := 0; index < maxManagedProcesses; index++ {
		id := fmt.Sprintf("active-%d", index)
		tool.processes[id] = &managedProcess{id: id, output: newRollingBuffer(16), done: make(chan struct{})}
	}
	if _, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"true","args":[],"purpose":"inspect"}`)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("start error = %v, want active process limit", err)
	}
}

func TestBackgroundProcessDeniesShellProgramsUnlessOperatorAuthorized(t *testing.T) {
	workspace := t.TempDir()
	tool := NewBackgroundProcess(workspace, 1024, time.Second)
	t.Cleanup(func() { _ = tool.Close() })
	for _, program := range []string{"bash", "sh", "/bin/bash", "/usr/bin/dash", "zsh", "fish"} {
		raw := []byte(`{"operation":"start","program":"` + program + `","args":["-c","true"],"purpose":"inspect"}`)
		if _, err := tool.Run(context.Background(), raw); err == nil {
			t.Fatalf("default background process accepted shell %q", program)
		}
	}
	operator := NewOperatorBackgroundProcess(workspace, 1024, time.Second)
	t.Cleanup(func() { _ = operator.Close() })
	started, err := operator.Run(context.Background(), []byte(`{"operation":"start","program":"bash","args":["-c","true"],"purpose":"inspect"}`))
	if err != nil {
		t.Fatalf("operator background process rejected shell: %v", err)
	}
	pollUntilExited(t, operator, backgroundIDFromResult(t, started.Content))
}

func TestBackgroundProcessStartPollAndList(t *testing.T) {
	tool := NewOperatorBackgroundProcess(t.TempDir(), 1024, 5*time.Second)
	t.Cleanup(func() { _ = tool.Close() })

	started, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"bash","args":["-c","printf ready"],"purpose":"inspect"}`))
	if err != nil {
		t.Fatalf("start error = %v", err)
	}
	id := backgroundIDFromResult(t, started.Content)
	polled := pollUntilExited(t, tool, id)
	if !strings.Contains(polled.Content, "ready") || polled.Verified || !polled.Changed {
		t.Fatalf("poll result = %#v", polled)
	}
	listed, err := tool.Run(context.Background(), []byte(`{"operation":"list"}`))
	if err != nil || !strings.Contains(listed.Content, id) {
		t.Fatalf("list result = %#v, error = %v", listed, err)
	}
}

func TestBackgroundProcessUsesRollingOutputBound(t *testing.T) {
	tool := NewOperatorBackgroundProcess(t.TempDir(), 32, 5*time.Second)
	t.Cleanup(func() { _ = tool.Close() })
	started, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"bash","args":["-c","printf 'START'; printf '%0100d' 0; printf 'END'"],"purpose":"inspect"}`))
	if err != nil {
		t.Fatal(err)
	}
	result := pollUntilExited(t, tool, backgroundIDFromResult(t, started.Content))
	outputParts := strings.SplitN(result.Content, "output:\n", 2)
	if len(outputParts) != 2 {
		t.Fatalf("missing rolling output in %q", result.Content)
	}
	output := outputParts[1]
	if !strings.Contains(result.Content, "truncated: true") || strings.Contains(output, "START") || !strings.Contains(output, "END") {
		t.Fatalf("rolling output result = %q", result.Content)
	}
	if len(output) > 32 {
		t.Fatalf("rolling output length = %d, want <= 32", len(output))
	}
}

func TestBackgroundProcessRejectsCWDTraversal(t *testing.T) {
	tool := NewOperatorBackgroundProcess(t.TempDir(), 1024, time.Second)
	t.Cleanup(func() { _ = tool.Close() })
	_, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"pwd","args":[],"cwd":"..","purpose":"inspect"}`))
	if err == nil {
		t.Fatal("start accepted cwd traversal")
	}
}

func TestBackgroundProcessStopKillsEntireProcessGroup(t *testing.T) {
	tool := NewOperatorBackgroundProcess(t.TempDir(), 1024, 5*time.Second)
	t.Cleanup(func() { _ = tool.Close() })
	started, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"bash","args":["-c","sleep 30 & echo $!; wait"],"purpose":"modify"}`))
	if err != nil {
		t.Fatal(err)
	}
	id := backgroundIDFromResult(t, started.Content)
	pid := waitForBackgroundChildPID(t, tool, id)
	stopped, err := tool.Run(context.Background(), []byte(`{"operation":"stop","id":"`+id+`"}`))
	if err != nil || !strings.Contains(stopped.Content, "status: exited") || !stopped.Changed {
		t.Fatalf("stop result = %#v, error = %v", stopped, err)
	}
	waitForProcessGone(t, pid)
}

func TestBackgroundProcessTimeoutKillsEntireProcessGroup(t *testing.T) {
	tool := NewOperatorBackgroundProcess(t.TempDir(), 1024, 2*time.Second)
	t.Cleanup(func() { _ = tool.Close() })
	started, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"bash","args":["-c","sleep 30 & echo $!; wait"],"timeout_seconds":1,"purpose":"inspect"}`))
	if err != nil {
		t.Fatal(err)
	}
	id := backgroundIDFromResult(t, started.Content)
	pid := waitForBackgroundChildPID(t, tool, id)
	result := pollUntilExited(t, tool, id)
	if !strings.Contains(result.Content, "timed_out: true") || result.Verified {
		t.Fatalf("timeout result = %#v", result)
	}
	waitForProcessGone(t, pid)
}

func TestBackgroundProcessCloseStopsAllAndRejectsNewStarts(t *testing.T) {
	tool := NewOperatorBackgroundProcess(t.TempDir(), 1024, 5*time.Second)
	var childPIDs []int
	var ids []string
	for range 2 {
		started, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"bash","args":["-c","sleep 30 & echo $!; wait"],"purpose":"inspect"}`))
		if err != nil {
			t.Fatal(err)
		}
		id := backgroundIDFromResult(t, started.Content)
		ids = append(ids, id)
		childPIDs = append(childPIDs, waitForBackgroundChildPID(t, tool, id))
	}
	if ids[0] == ids[1] {
		t.Fatalf("background IDs are not unique: %q", ids[0])
	}
	if err := tool.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := tool.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	for _, pid := range childPIDs {
		waitForProcessGone(t, pid)
	}
	if _, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"true","args":[],"purpose":"inspect"}`)); err == nil {
		t.Fatal("start succeeded after Close")
	}
}

func backgroundIDFromResult(t *testing.T, content string) string {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "id: ") {
			return strings.TrimPrefix(line, "id: ")
		}
	}
	t.Fatalf("no process id in %q", content)
	return ""
}

func pollUntilExited(t *testing.T, tool *BackgroundProcess, id string) Result {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		result, err := tool.Run(context.Background(), []byte(fmt.Sprintf(`{"operation":"poll","id":%q}`, id)))
		if err != nil {
			t.Fatalf("poll error = %v", err)
		}
		if strings.Contains(result.Content, "status: exited") {
			return result
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %s did not exit", id)
	return Result{}
}

func waitForBackgroundChildPID(t *testing.T, tool *BackgroundProcess, id string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := tool.Run(context.Background(), []byte(fmt.Sprintf(`{"operation":"poll","id":%q}`, id)))
		if err != nil {
			t.Fatal(err)
		}
		if output := strings.SplitN(result.Content, "output:\n", 2); len(output) == 2 && len(strings.Fields(output[1])) > 0 {
			return childPIDFromOutput(t, result.Content)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %s did not report child pid", id)
	return 0
}
