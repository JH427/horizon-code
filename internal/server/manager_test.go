package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestManagerStartsWaitsAndStopsOwnedServer(t *testing.T) {
	port := freePort(t)
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", port)
	manager := NewManager(Config{
		Command:         os.Args[0],
		Args:            []string{"-test.run=TestHelperInferenceServer", "--"},
		Environment:     []string{"GO_WANT_HELPER_INFERENCE=1", "HELPER_PORT=" + strconv.Itoa(port)},
		HealthURL:       endpoint + "/health",
		StartupTimeout:  3 * time.Second,
		ShutdownTimeout: 2 * time.Second,
	}, http.DefaultClient)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	response, err := http.Get(endpoint + "/health")
	if err != nil {
		t.Fatalf("health request after Start() error = %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", response.StatusCode)
	}

	if err := manager.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		connection, dialErr := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 25*time.Millisecond)
		if dialErr != nil {
			return
		}
		_ = connection.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("owned inference server still accepts connections after Close()")
}

func TestManagerRefusesToOwnAnAlreadyRunningEndpoint(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })

	manager := NewManager(Config{
		Command:        os.Args[0],
		HealthURL:      "http://" + listener.Addr().String(),
		StartupTimeout: time.Second,
	}, http.DefaultClient)

	err = manager.Start(context.Background())
	if err == nil {
		t.Fatal("Start() error = nil, want refusal for an existing endpoint")
	}
	if manager.Owned() {
		t.Fatal("Owned() = true for a server the manager did not start")
	}
}

func TestHelperInferenceServer(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_INFERENCE") != "1" {
		return
	}
	port, err := strconv.Atoi(os.Getenv("HELPER_PORT"))
	if err != nil {
		os.Exit(2)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	if err := http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux); err != nil {
		os.Exit(3)
	}
	os.Exit(0)
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}
