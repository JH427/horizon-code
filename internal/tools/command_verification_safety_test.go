package tools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestExecCommandTreatsArbitraryInspectionAsPotentialMutation(t *testing.T) {
	result, err := NewExecCommand(t.TempDir(), 1024, time.Second).Run(context.Background(), []byte(`{"program":"true","args":[],"purpose":"inspect"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Verified {
		t.Fatalf("result = %#v, want conservative Changed", result)
	}
}

func TestExecCommandRejectsSelfAttestedVerification(t *testing.T) {
	result, err := NewExecCommand(t.TempDir(), 1024, time.Second).Run(context.Background(), []byte(`{"program":"true","args":[],"purpose":"verify"}`))
	if err == nil || !strings.Contains(err.Error(), "hardened verification") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if result.Verified {
		t.Fatalf("result = %#v, must not verify", result)
	}
}

func TestShellCannotSelfAttestVerification(t *testing.T) {
	result, err := NewShell(t.TempDir(), 1024, time.Second).Run(context.Background(), []byte(`{"command":"true","purpose":"verify"}`))
	if err == nil || !strings.Contains(err.Error(), "run_command") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
	if result.Verified {
		t.Fatalf("result = %#v, must not verify", result)
	}
}

func TestShellTreatsInspectionAsPotentialMutation(t *testing.T) {
	result, err := NewShell(t.TempDir(), 1024, time.Second).Run(context.Background(), []byte(`{"command":"true","purpose":"inspect"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Verified {
		t.Fatalf("result = %#v, want conservative Changed", result)
	}
}

func TestBackgroundProcessCannotSelfAttestVerification(t *testing.T) {
	tool := NewBackgroundProcess(t.TempDir(), 1024, time.Second)
	result, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"true","args":[],"purpose":"verify"}`))
	if err == nil || !strings.Contains(err.Error(), "run_command") {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestBackgroundProcessTreatsInspectionAsPotentialMutation(t *testing.T) {
	tool := NewBackgroundProcess(t.TempDir(), 1024, time.Second)
	t.Cleanup(func() { _ = tool.Close() })
	result, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"true","args":[],"purpose":"inspect"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.Verified {
		t.Fatalf("result = %#v, want conservative Changed", result)
	}
}
