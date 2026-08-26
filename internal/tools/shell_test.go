package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestShellRunsBashLoginCommandAndMarksPotentialMutation(t *testing.T) {
	tool := NewShell(t.TempDir(), 1024, time.Second)
	result, err := tool.Run(context.Background(), []byte(`{"command":"printf '%s' \"$((2 + 3))\"","purpose":"inspect"}`))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Verified || !result.Changed {
		t.Fatalf("result flags = changed %t, verified %t", result.Changed, result.Verified)
	}
	if !strings.Contains(result.Content, "5") {
		t.Fatalf("result content = %q", result.Content)
	}
}

func TestShellFailurePreservesModifyChangedButNeverVerification(t *testing.T) {
	tool := NewShell(t.TempDir(), 1024, time.Second)
	modified, err := tool.Run(context.Background(), []byte(`{"command":"printf partial; exit 7","purpose":"modify"}`))
	if err == nil || !modified.Changed || modified.Verified || !strings.Contains(modified.Content, "partial") {
		t.Fatalf("modify result = %#v, error = %v", modified, err)
	}
	verified, err := tool.Run(context.Background(), []byte(`{"command":"exit 7","purpose":"verify"}`))
	if err == nil || verified.Changed || verified.Verified {
		t.Fatalf("verify result = %#v, error = %v", verified, err)
	}
}

func TestShellIsExplicitlyOperatorOnlyAndRejectsCWDTraversal(t *testing.T) {
	tool := NewShell(t.TempDir(), 1024, time.Second)
	if !strings.Contains(strings.ToLower(tool.Definition().Description), "operator-only") {
		t.Fatalf("description = %q", tool.Definition().Description)
	}
	if _, err := tool.Run(context.Background(), []byte(`{"command":"pwd","cwd":"../outside","purpose":"inspect"}`)); err == nil {
		t.Fatal("Run() accepted cwd traversal")
	}
}

func TestShellTimeoutKillsChildProcessGroup(t *testing.T) {
	tool := NewShell(t.TempDir(), 1024, 2*time.Second)
	_, err := tool.Run(context.Background(), []byte(`{"command":"sleep 30 & echo $!; wait","timeout_seconds":1,"purpose":"inspect"}`))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("Run() error = %v, want timeout", err)
	}
	waitForProcessGone(t, childPIDFromOutput(t, err.Error()))
}
