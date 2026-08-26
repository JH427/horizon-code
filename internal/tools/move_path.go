package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JH427/horizon-code/internal/model"
)

type MovePath struct {
	workspace string
}

type movePathArguments struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func NewMovePath(workspace string) *MovePath {
	return &MovePath{workspace: workspace}
}

func (tool *MovePath) Definition() model.Tool {
	return model.Tool{
		Name:        "move_path",
		Description: "Move or rename one workspace file or directory. Refuses to overwrite an existing destination.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"source":      map[string]any{"type": "string", "description": "Workspace-relative existing source path", "maxLength": 4096},
				"destination": map[string]any{"type": "string", "description": "Workspace-relative new destination path", "maxLength": 4096},
			},
			"required":             []string{"source", "destination"},
			"additionalProperties": false,
		},
	}
}

func (tool *MovePath) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var arguments movePathArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode move_path arguments: %w", err)
	}
	source, _, err := resolveWorkspaceEntry(tool.workspace, arguments.Source)
	if err != nil {
		return Result{}, fmt.Errorf("move_path source: %w", err)
	}
	destination, err := resolveWorkspacePath(tool.workspace, arguments.Destination)
	if err != nil {
		return Result{}, fmt.Errorf("move_path destination: %w", err)
	}
	if _, err := os.Lstat(destination); err == nil {
		return Result{}, errors.New("move_path destination already exists")
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("inspect move_path destination: %w", err)
	}
	parentInfo, err := os.Stat(filepath.Dir(destination))
	if err != nil {
		return Result{}, fmt.Errorf("stat move_path destination parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return Result{}, errors.New("move_path destination parent is not a directory")
	}
	if err := os.Rename(source, destination); err != nil {
		return Result{}, fmt.Errorf("move %q to %q: %w", arguments.Source, arguments.Destination, err)
	}
	return Result{
		Content: fmt.Sprintf("MOVE_PATH_OK\nsource: %s\ndestination: %s", arguments.Source, arguments.Destination),
		Changed: true,
	}, nil
}
