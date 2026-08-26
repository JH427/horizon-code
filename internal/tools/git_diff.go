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

type GitDiff struct {
	workspace string
	maxBytes  int
}

type gitDiffArguments struct {
	Path   string `json:"path,omitempty"`
	Staged bool   `json:"staged,omitempty"`
}

func NewGitDiff(workspace string, maxBytes int) *GitDiff {
	if maxBytes <= 0 {
		maxBytes = 128 << 10
	}
	return &GitDiff{workspace: workspace, maxBytes: maxBytes}
}

func (tool *GitDiff) Definition() model.Tool {
	return model.Tool{
		Name:        "git_diff",
		Description: "Inspect the bounded Git diff for the whole workspace or one workspace-relative path.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":   map[string]any{"type": "string", "description": "Optional workspace-relative path"},
				"staged": map[string]any{"type": "boolean", "description": "Show staged changes instead of unstaged changes"},
			},
			"additionalProperties": false,
		},
	}
}

func (tool *GitDiff) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	var arguments gitDiffArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode git_diff arguments: %w", err)
	}
	repositoryCheck := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	repositoryCheck.Dir = tool.workspace
	insideWorkTree, err := repositoryCheck.Output()
	if err != nil || strings.TrimSpace(string(insideWorkTree)) != "true" {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, errors.New("git_diff workspace is not a Git repository")
	}
	path := ""
	if arguments.Path != "" {
		if filepath.IsAbs(arguments.Path) {
			return Result{}, errors.New("git_diff path must be workspace-relative")
		}
		cleaned := filepath.Clean(arguments.Path)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			return Result{}, errors.New("git_diff path escapes the workspace")
		}
		path = cleaned
	}
	commandArguments := []string{"diff", "--no-ext-diff", "--no-color"}
	if arguments.Staged {
		commandArguments = append(commandArguments, "--cached")
	}
	if path != "" {
		commandArguments = append(commandArguments, "--", path)
	}
	command := exec.CommandContext(ctx, "git", commandArguments...)
	command.Dir = tool.workspace
	stdout := newBoundedBuffer(tool.maxBytes)
	stderr := newBoundedBuffer(16 << 10)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("git_diff failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	content := strings.TrimSpace(stdout.String())
	if content == "" {
		content = "(no diff)"
	}
	return Result{Content: fmt.Sprintf("GIT_DIFF_OK\ntruncated: %t\ncontent:\n%s", stdout.truncated, content)}, nil
}
