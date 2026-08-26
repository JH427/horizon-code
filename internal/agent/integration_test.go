package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

func TestEngineAndHTTPClientCompleteDeterministicToolRoundTrip(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		step := requests.Add(1)
		var body struct {
			Messages []model.Message `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		if step == 1 {
			_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"marker.txt\"}"}}]},"finish_reason":"tool_calls"}]}`)
			_, _ = fmt.Fprintln(writer, `data: [DONE]`)
			return
		}
		if len(body.Messages) != 4 || body.Messages[3].Role != "tool" || !strings.Contains(body.Messages[3].Content, "ROUND_TRIP_GREEN") {
			t.Errorf("second request messages = %#v", body.Messages)
		}
		_, _ = fmt.Fprintln(writer, `data: {"choices":[{"delta":{"content":"ROUND_TRIP_GREEN"}}]}`)
		_, _ = fmt.Fprintln(writer, `data: [DONE]`)
	}))
	defer server.Close()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "marker.txt"), []byte("ROUND_TRIP_GREEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	client := model.NewClient(model.Config{BaseURL: server.URL, Model: "qwen38-27b"}, server.Client())
	engine := NewEngine(client, []Tool{tools.NewReadFile(workspace, 100)}, 4)
	answer, err := engine.Run(context.Background(), "Read marker.txt", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if answer != "ROUND_TRIP_GREEN" || requests.Load() != 2 {
		t.Fatalf("answer = %q, requests = %d", answer, requests.Load())
	}
}
