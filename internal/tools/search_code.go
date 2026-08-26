package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JH427/horizon-code/internal/model"
)

type SearchCode struct {
	workspace  string
	maxResults int
	maxBytes   int
}

type searchCodeArguments struct {
	Query    string `json:"query"`
	Path     string `json:"path,omitempty"`
	FileGlob string `json:"file_glob,omitempty"`
}

func NewSearchCode(workspace string, maxResults, maxBytes int) *SearchCode {
	if maxResults <= 0 {
		maxResults = 100
	}
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	return &SearchCode{workspace: workspace, maxResults: maxResults, maxBytes: maxBytes}
}

func (tool *SearchCode) Definition() model.Tool {
	return model.Tool{
		Name:        "search_code",
		Description: "Search workspace text with a regular expression and return bounded file, line, column, and matching-line results.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":     map[string]any{"type": "string", "description": "Regular expression to search for"},
				"path":      map[string]any{"type": "string", "description": "Optional workspace-relative file or directory"},
				"file_glob": map[string]any{"type": "string", "description": "Optional glob such as *.go"},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}
}

func (tool *SearchCode) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	var arguments searchCodeArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode search_code arguments: %w", err)
	}
	if arguments.Query == "" {
		return Result{}, errors.New("search_code query is required")
	}
	searchPath := arguments.Path
	if searchPath == "" {
		searchPath = "."
	}
	resolved, err := resolveExistingWorkspacePath(tool.workspace, searchPath)
	if err != nil {
		return Result{}, err
	}
	relative, err := filepath.Rel(tool.workspace, resolved)
	if err != nil {
		return Result{}, fmt.Errorf("make search path relative: %w", err)
	}
	if relative == "" {
		relative = "."
	}

	commandArguments := []string{"--line-number", "--column", "--no-heading", "--color=never"}
	if arguments.FileGlob != "" {
		commandArguments = append(commandArguments, "--glob", arguments.FileGlob)
	}
	commandArguments = append(commandArguments, "--", arguments.Query, relative)
	command := exec.CommandContext(ctx, "rg", commandArguments...)
	command.Dir = tool.workspace
	stdout := newBoundedBuffer(tool.maxBytes)
	stderr := newBoundedBuffer(16 << 10)
	command.Stdout = stdout
	command.Stderr = stderr
	err = command.Run()
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) && exitError.ExitCode() == 1 {
			return Result{Content: "SEARCH_CODE_OK\nmatches: 0\ntruncated: false"}, nil
		}
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("search_code failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	truncated := stdout.truncated
	if len(lines) > tool.maxResults {
		lines = lines[:tool.maxResults]
		truncated = true
	}
	content := fmt.Sprintf("SEARCH_CODE_OK\nmatches: %d\ntruncated: %t\ncontent:\n%s", len(lines), truncated, strings.Join(lines, "\n"))
	return Result{Content: content}, nil
}
