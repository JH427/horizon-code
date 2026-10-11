package model

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExplicitZeroThinkingBudgetIsSentWithoutChangingDefaults(t *testing.T) {
	var bodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		bodies = append(bodies, body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintln(w, `data: {"choices":[{"finish_reason":"stop","delta":{"content":"done"}}]}`)
		fmt.Fprintln(w, "data: [DONE]")
	}))
	defer server.Close()
	client := NewClient(Config{BaseURL: server.URL, ReasoningBudgetTokens: 2048, MaxTokens: 4096}, server.Client())
	zero := 0
	for _, options := range []InferenceOptions{{ReasoningBudgetTokens: &zero, MaxTokens: &zero}, {}} {
		if _, err := client.Stream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}, InferenceOptions: options}, nil); err != nil {
			t.Fatal(err)
		}
	}
	if bodies[0]["thinking_budget_tokens"] != float64(0) || bodies[1]["thinking_budget_tokens"] != float64(2048) || bodies[0]["max_tokens"] != float64(0) || bodies[1]["max_tokens"] != float64(4096) {
		t.Fatalf("requests=%v", bodies)
	}
}
