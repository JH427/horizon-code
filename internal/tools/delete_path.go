package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/JH427/horizon-code/internal/model"
)

type DeletePath struct {
	workspace string
	remove    func(string) error
	removeAll func(string) error
}

type deletePathArguments struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive,omitempty"`
}

func NewDeletePath(workspace string) *DeletePath {
	return &DeletePath{workspace: workspace, remove: os.Remove, removeAll: os.RemoveAll}
}

func (tool *DeletePath) Definition() model.Tool {
	return model.Tool{
		Name:        "delete_path",
		Description: "Delete one workspace file, symlink, or directory. Non-empty directories require recursive=true. The workspace root cannot be deleted.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":      map[string]any{"type": "string", "description": "Workspace-relative path to delete", "maxLength": 4096},
				"recursive": map[string]any{"type": "boolean", "description": "Required to recursively delete a non-empty directory"},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

func (tool *DeletePath) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var arguments deletePathArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode delete_path arguments: %w", err)
	}
	resolved, info, err := resolveWorkspaceEntry(tool.workspace, arguments.Path)
	if err != nil {
		return Result{}, fmt.Errorf("delete_path: %w", err)
	}
	if info.IsDir() && arguments.Recursive {
		err = tool.removeAll(resolved)
	} else {
		err = tool.remove(resolved)
	}
	if err != nil {
		return Result{Content: "DELETE_PATH_ERROR\npath: " + arguments.Path, Changed: true}, fmt.Errorf("delete %q: %w", arguments.Path, err)
	}
	return Result{
		Content: fmt.Sprintf("DELETE_PATH_OK\npath: %s\nrecursive: %t", arguments.Path, arguments.Recursive),
		Changed: true,
	}, nil
}
