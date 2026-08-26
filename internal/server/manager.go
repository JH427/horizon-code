package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const healthPollInterval = 100 * time.Millisecond

type Config struct {
	Command         string
	Args            []string
	Environment     []string
	WorkingDir      string
	HealthURL       string
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
	Stdout          io.Writer
	Stderr          io.Writer
}

type Manager struct {
	config Config
	client *http.Client

	mu      sync.Mutex
	command *exec.Cmd
	done    chan error
	owned   bool
	stderr  bytes.Buffer
}

func NewManager(config Config, client *http.Client) *Manager {
	if client == nil {
		client = http.DefaultClient
	}
	if config.StartupTimeout <= 0 {
		config.StartupTimeout = 2 * time.Minute
	}
	if config.ShutdownTimeout <= 0 {
		config.ShutdownTimeout = 10 * time.Second
	}
	return &Manager{config: config, client: client}
}

func (manager *Manager) Start(ctx context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.command != nil {
		return errors.New("inference server already started by this manager")
	}
	if manager.config.Command == "" {
		return errors.New("inference server command is required")
	}
	if manager.config.HealthURL == "" {
		return errors.New("inference server health URL is required")
	}
	if manager.healthy(ctx) {
		return fmt.Errorf("inference endpoint %s is already running; refusing to claim ownership", manager.config.HealthURL)
	}

	command := exec.Command(manager.config.Command, manager.config.Args...)
	command.Dir = manager.config.WorkingDir
	command.Env = append(os.Environ(), manager.config.Environment...)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if manager.config.Stdout != nil {
		command.Stdout = manager.config.Stdout
	}
	if manager.config.Stderr != nil {
		command.Stderr = io.MultiWriter(manager.config.Stderr, &manager.stderr)
	} else {
		command.Stderr = &manager.stderr
	}
	if err := command.Start(); err != nil {
		return fmt.Errorf("start inference server: %w", err)
	}

	manager.command = command
	manager.done = make(chan error, 1)
	manager.owned = true
	go func() { manager.done <- command.Wait() }()

	startupContext, cancel := context.WithTimeout(ctx, manager.config.StartupTimeout)
	defer cancel()
	if err := manager.waitUntilHealthy(startupContext); err != nil {
		stopErr := manager.stopLocked()
		if stopErr != nil {
			return fmt.Errorf("wait for inference server: %w; shutdown after failed startup: %v; stderr: %s", err, stopErr, manager.stderr.String())
		}
		return fmt.Errorf("wait for inference server: %w; stderr: %s", err, manager.stderr.String())
	}
	return nil
}

func (manager *Manager) Close() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.stopLocked()
}

func (manager *Manager) Owned() bool {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.owned
}

func (manager *Manager) waitUntilHealthy(ctx context.Context) error {
	ticker := time.NewTicker(healthPollInterval)
	defer ticker.Stop()
	for {
		if manager.healthy(ctx) {
			return nil
		}
		select {
		case err := <-manager.done:
			manager.owned = false
			if err == nil {
				return errors.New("inference server exited before becoming healthy")
			}
			return fmt.Errorf("inference server exited before becoming healthy: %w", err)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (manager *Manager) healthy(ctx context.Context) bool {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manager.config.HealthURL, nil)
	if err != nil {
		return false
	}
	response, err := manager.client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func (manager *Manager) stopLocked() error {
	if manager.command == nil || !manager.owned {
		return nil
	}
	manager.owned = false
	process := manager.command.Process
	if process == nil {
		return nil
	}
	if err := syscall.Kill(-process.Pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate inference server process group: %w", err)
	}

	timer := time.NewTimer(manager.config.ShutdownTimeout)
	defer timer.Stop()
	select {
	case err := <-manager.done:
		if err != nil {
			var exitError *exec.ExitError
			if !errors.As(err, &exitError) {
				return fmt.Errorf("wait for inference server shutdown: %w", err)
			}
		}
		return nil
	case <-timer.C:
		if err := syscall.Kill(-process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("kill inference server process group: %w", err)
		}
		<-manager.done
		return nil
	}
}
