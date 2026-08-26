package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientVerifyCapturesModelContextSize(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"id":"qwen38-27b","meta":{"n_ctx":262144}}]}`))
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	if err := client.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if got := client.ContextSize(); got != 262144 {
		t.Fatalf("ContextSize() = %d, want 262144", got)
	}
}

func TestClientVerifyRequiresConfiguredModelID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"id":"qwen38-27b","object":"model"}]}`))
	}))
	defer server.Close()

	client := NewClient(Config{BaseURL: server.URL + "/v1", Model: "qwen38-27b"}, server.Client())
	if err := client.Verify(context.Background()); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	missing := NewClient(Config{BaseURL: server.URL + "/v1", Model: "not-qwen"}, server.Client())
	if err := missing.Verify(context.Background()); err == nil {
		t.Fatal("Verify() error = nil for missing configured model")
	}
}
