package tools

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/JH427/horizon-code/internal/model"
)

type MakeDirectory struct {
	workspace string
}

type makeDirectoryArguments struct {
	Path string `json:"path"`
}

func NewMakeDirectory(workspace string) *MakeDirectory {
	return &MakeDirectory{workspace: workspace}
}

func (tool *MakeDirectory) Definition() model.Tool {
	return model.Tool{
		Name:        "make_directory",
		Description: "Create a workspace directory and any missing parent directories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Workspace-relative directory path", "maxLength": 4096},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

func (tool *MakeDirectory) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var arguments makeDirectoryArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode make_directory arguments: %w", err)
	}
	resolved, err := resolveWorkspacePath(tool.workspace, arguments.Path)
	if err != nil {
		return Result{}, fmt.Errorf("make_directory: %w", err)
	}
	if info, err := os.Stat(resolved); err == nil {
		if !info.IsDir() {
			return Result{}, errors.New("make_directory path already exists and is not a directory")
		}
		return Result{Content: fmt.Sprintf("MAKE_DIRECTORY_OK\npath: %s\ncreated: false", arguments.Path)}, nil
	} else if !os.IsNotExist(err) {
		return Result{}, fmt.Errorf("stat make_directory path: %w", err)
	}
	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return Result{Content: "MAKE_DIRECTORY_ERROR\npath: " + arguments.Path, Changed: true}, fmt.Errorf("create directory %q: %w", arguments.Path, err)
	}
	return Result{
		Content: fmt.Sprintf("MAKE_DIRECTORY_OK\npath: %s\ncreated: true", arguments.Path),
		Changed: true,
	}, nil
}
