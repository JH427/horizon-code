package tools

import (
	"context"
	"testing"
	"time"
)

func TestBackgroundActivityIncludesInspectJobsAndClearsAfterStop(t *testing.T) {
	for _, purpose := range []string{"inspect", "modify"} {
		t.Run(purpose, func(t *testing.T) {
			tool := NewBackgroundProcess(t.TempDir(), 1024, time.Minute)
			t.Cleanup(func() { _ = tool.Close() })
			if tool.HasRunningProcesses() {
				t.Fatal("empty manager reports activity")
			}
			result, err := tool.Run(context.Background(), []byte(`{"operation":"start","program":"sleep","args":["30"],"purpose":"`+purpose+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			if !tool.HasRunningProcesses() {
				t.Fatal("running job was not detected")
			}
			id := backgroundIDFromResult(t, result.Content)
			if _, err := tool.Run(context.Background(), []byte(`{"operation":"stop","id":"`+id+`"}`)); err != nil {
				t.Fatal(err)
			}
			if tool.HasRunningProcesses() {
				t.Fatal("stopped job still blocks verification")
			}
		})
	}
}
