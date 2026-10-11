package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func validResponse(choice string) string {
	if choice == "C" {
		return strings.Replace(validResponse("B"), `"choice":"B","probabilities":{"A":0.2,"B":0.7,"C":0.1}`, `"choice":"C","probabilities":{"A":0.2,"B":0.1,"C":0.7}`, 1)
	}
	return fmt.Sprintf(`{"model":"laya-rl-agent","answers":{"inference_mode":{"type":"choice","choice":%q,"probabilities":{"A":0.2,"B":0.7,"C":0.1}}},"usage":{"input_tokens":42,"output_tokens":0,"state_tokens":20,"state_tokens_dropped":0,"truncated":false,"truncated_questions":[]},"routing":{"model":"english","reason":"English Latin text"}}`, choice)
}

func newTestClient(t *testing.T, handler http.Handler, timeout time.Duration, limit int64) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{BaseURL: server.URL, Checkpoint: "english", Timeout: timeout, MaxResponseBytes: limit}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testState() State {
	return State{SchemaVersion: SchemaVersion, UserRequest: "inspect status", CapabilityMode: "safe", FirstModelStep: true}
}

func TestClientDefaultsToTHX01Checkpoint(t *testing.T) {
	var requestBody wireRequest
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		_, _ = fmt.Fprint(writer, validResponse("B"))
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Timeout: time.Second}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Decide(context.Background(), testState())
	if err != nil {
		t.Fatal(err)
	}
	if requestBody.Model != "doofz/THX-01" || result.Checkpoint != "doofz/THX-01" {
		t.Fatalf("request model %q, result checkpoint %q", requestBody.Model, result.Checkpoint)
	}
}

func TestClientReturnsTypedDecisionAndSendsNeutralContract(t *testing.T) {
	var requestBody wireRequest
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/systemone" || request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		writer.Header().Set("X-Inference-Time-Ms", "12.50")
		_, _ = fmt.Fprint(writer, validResponse("B"))
	}), time.Second, 0)

	result, err := client.Decide(context.Background(), testState())
	if err != nil {
		t.Fatal(err)
	}
	if result.Choice != "B" || result.Model != "laya-rl-agent" || result.Checkpoint != "english" {
		t.Fatalf("result = %#v", result)
	}
	if result.EndpointLatency != 12500*time.Microsecond || result.ObservedLatency <= 0 || result.InputTruncated || result.Abstained {
		t.Fatalf("result metadata = %#v", result)
	}
	question := requestBody.Questions["inference_mode"]
	criteria, ok := question.Criteria.(map[string]any)
	if requestBody.Model != "english" || question.Type != "choice" || !ok || len(criteria) != 3 || len(requestBody.Questions) != 3 {
		t.Fatalf("wire request = %#v", requestBody)
	}
	for _, option := range []string{"A", "B", "C"} {
		if criteria[option] == "" {
			t.Fatalf("missing neutral option %s: %#v", option, criteria)
		}
	}
}

func TestClientAcceptsTHX01ResponseMetadata(t *testing.T) {
	response := `{"model":"THX-01","answers":{"inference_mode":{"type":"choice","choice":"B","probabilities":{"A":0.1,"B":0.8,"C":0.1},"confidence":0.9},"task_complexity":{"type":"score","score":0.4,"legend":{"0":"trivial","1":"routine","2":"multi-step","3":"complex"},"probabilities":{"0":0.7,"1":0.2,"2":0.08,"3":0.02},"confidence":0.8},"workspace_mutation":{"type":"noul","noul":0.05,"confidence":0.95}},"usage":{"input_tokens":36,"output_tokens":0},"detected":{"language":"en"},"latency_ms":10.25}`
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(writer, response)
	}), time.Second, 0)
	result, err := client.Decide(context.Background(), testState())
	if err != nil {
		t.Fatal(err)
	}
	if result.Model != "THX-01" || result.EndpointLatency != 10250*time.Microsecond || result.ComplexityScore == nil || *result.ComplexityScore != 0.4 || result.MutationProbability == nil || *result.MutationProbability != 0.05 {
		t.Fatalf("result = %#v", result)
	}
}

func TestClientHonorsLowConfidenceAndQuestionTruncation(t *testing.T) {
	response := strings.Replace(validResponse("B"), `"choice":"B"`, `"choice":"B","low_confidence":true`, 1)
	response = strings.Replace(response, `"truncated_questions":[]`, `"truncated_questions":["workspace_mutation"]`, 1)
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, response) }), time.Second, 0)
	result, err := client.Decide(context.Background(), testState())
	if err != nil || !result.Abstained || !result.InputTruncated {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestObservedLatencyIncludesResponseBody(t *testing.T) {
	client := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(50 * time.Millisecond)
		fmt.Fprint(w, validResponse("B"))
	}), time.Second, 0)
	result, err := client.Decide(context.Background(), testState())
	if err != nil || result.ObservedLatency < 50*time.Millisecond {
		t.Fatalf("latency=%s err=%v", result.ObservedLatency, err)
	}
}

func TestClientMarksUncertainAndTruncatedDecision(t *testing.T) {
	response := strings.Replace(validResponse("C"), `"state_tokens_dropped":0,"truncated":false`, `"state_tokens_dropped":7,"truncated":true`, 1)
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(writer, response)
	}), time.Second, 0)
	result, err := client.Decide(context.Background(), testState())
	if err != nil {
		t.Fatal(err)
	}
	if result.Abstained || !result.InputTruncated || result.StateTokensDropped != 7 {
		t.Fatalf("result = %#v", result)
	}
}

func TestClientSurfacesEndpointAbstention(t *testing.T) {
	response := strings.Replace(validResponse("B"), `"probabilities":`, `"abstention":"abstained","probabilities":`, 1)
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(writer, response)
	}), time.Second, 0)
	result, err := client.Decide(context.Background(), testState())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Abstained {
		t.Fatalf("result = %#v, want abstained", result)
	}
}

func TestClientRejectsBadHTTPAndResponseBodies(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		limit  int64
		want   string
	}{
		{name: "non-2xx", status: http.StatusServiceUnavailable, body: `{"detail":"loading"}`, want: "503"},
		{name: "malformed JSON", status: http.StatusOK, body: `{`, want: "decode decision response"},
		{name: "unknown field", status: http.StatusOK, body: strings.Replace(validResponse("B"), `"model":`, `"extra":true,"model":`, 1), want: "unknown field"},
		{name: "oversized", status: http.StatusOK, body: validResponse("B"), limit: 32, want: "32-byte limit"},
		{name: "missing answer", status: http.StatusOK, body: `{"model":"laya","answers":{},"usage":{}}`, want: "missing inference_mode"},
		{name: "unexpected choice", status: http.StatusOK, body: validResponse("fast"), want: "unexpected option"},
		{name: "unexpected probability", status: http.StatusOK, body: strings.Replace(validResponse("B"), `"C":0.1`, `"D":0.1`, 1), want: "unexpected option"},
		{name: "missing probability", status: http.StatusOK, body: strings.Replace(validResponse("B"), `,"C":0.1`, ``, 1), want: "probabilities for A, B, and C"},
		{name: "invalid probability total", status: http.StatusOK, body: strings.Replace(validResponse("B"), `"B":0.7`, `"B":0.2`, 1), want: "sum to"},
		{name: "inconsistent choice", status: http.StatusOK, body: strings.Replace(validResponse("B"), `"choice":"B"`, `"choice":"A"`, 1), want: "highest probability"},
		{name: "missing model", status: http.StatusOK, body: strings.Replace(validResponse("B"), `"laya-rl-agent"`, `""`, 1), want: "model identity"},
		{name: "bad latency", status: http.StatusOK, body: validResponse("B"), want: "invalid X-Inference-Time-Ms"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				if test.name == "bad latency" {
					writer.Header().Set("X-Inference-Time-Ms", "NaN")
				}
				writer.WriteHeader(test.status)
				_, _ = fmt.Fprint(writer, test.body)
			}), time.Second, test.limit)
			_, err := client.Decide(context.Background(), testState())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decide() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestClientRejectsRedirects(t *testing.T) {
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected = true
	}))
	defer target.Close()
	client := newTestClient(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}), time.Second, 0)
	_, err := client.Decide(context.Background(), testState())
	if err == nil || !strings.Contains(err.Error(), "redirects are disabled") {
		t.Fatalf("Decide() error = %v, want disabled redirect", err)
	}
	if redirected {
		t.Fatal("decision request followed redirect")
	}
}

func TestClientHonorsTimeoutAndCancellation(t *testing.T) {
	handler := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = fmt.Fprint(writer, validResponse("B"))
	})

	t.Run("timeout", func(t *testing.T) {
		client := newTestClient(t, handler, 20*time.Millisecond, 0)
		_, err := client.Decide(context.Background(), testState())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Decide() error = %v, want deadline exceeded", err)
		}
	})
	t.Run("parent cancellation", func(t *testing.T) {
		client := newTestClient(t, handler, time.Second, 0)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.Decide(ctx, testState())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Decide() error = %v, want context canceled", err)
		}
	})
}

func TestClientValidatesConfigurationAndStateVersion(t *testing.T) {
	for _, config := range []Config{
		{BaseURL: "file:///tmp/laya", Timeout: time.Second},
		{BaseURL: "https://127.0.0.1:8001", Timeout: time.Second},
		{BaseURL: "http://192.0.2.1:8001", Timeout: time.Second},
		{BaseURL: "http://user@127.0.0.1:8001", Timeout: time.Second},
		{BaseURL: "http://127.0.0.1:8001?query=1", Timeout: time.Second},
		{BaseURL: "http://127.0.0.1:8001", Timeout: 0},
	} {
		if _, err := NewClient(config, nil); err == nil {
			t.Fatalf("NewClient(%#v) succeeded", config)
		}
	}
	client, err := NewClient(Config{BaseURL: "http://127.0.0.1:8001", Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Decide(context.Background(), State{SchemaVersion: 99})
	if err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("Decide() error = %v", err)
	}
}
