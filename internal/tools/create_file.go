package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JH427/horizon-code/internal/model"
)

type CreateFile struct {
	workspace string
	maxBytes  int
}

type createFileArguments struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func NewCreateFile(workspace string, maxBytes int) *CreateFile {
	if maxBytes <= 0 {
		maxBytes = 4 << 20
	}
	return &CreateFile{workspace: workspace, maxBytes: maxBytes}
}

func (tool *CreateFile) Definition() model.Tool {
	return model.Tool{
		Name:        "create_file",
		Description: "Create one new UTF-8 text file inside an existing workspace directory. Refuses to overwrite existing files.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Workspace-relative path for the new file"},
				"content": map[string]any{"type": "string", "description": "Complete initial file content"},
			},
			"required":             []string{"path", "content"},
			"additionalProperties": false,
		},
	}
}

func (tool *CreateFile) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var arguments createFileArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode create_file arguments: %w", err)
	}
	if arguments.Path == "" {
		return Result{}, errors.New("create_file path is required")
	}
	if filepath.IsAbs(arguments.Path) {
		return Result{}, errors.New("create_file path must be workspace-relative")
	}
	cleaned := filepath.Clean(arguments.Path)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return Result{}, errors.New("create_file path escapes the workspace")
	}
	if len(arguments.Content) > tool.maxBytes {
		return Result{}, fmt.Errorf("create_file content is %d bytes; limit is %d", len(arguments.Content), tool.maxBytes)
	}
	if strings.IndexByte(arguments.Content, 0) >= 0 {
		return Result{}, errors.New("create_file supports text files only")
	}
	parent, err := resolveExistingWorkspacePath(tool.workspace, filepath.Dir(cleaned))
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(parent)
	if err != nil {
		return Result{}, fmt.Errorf("stat create_file parent: %w", err)
	}
	if !info.IsDir() {
		return Result{}, errors.New("create_file parent is not a directory")
	}
	target := filepath.Join(parent, filepath.Base(cleaned))
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("create %q: %w", arguments.Path, err)
	}
	complete := false
	defer func() {
		_ = file.Close()
		if !complete {
			_ = os.Remove(target)
		}
	}()
	if _, err := file.WriteString(arguments.Content); err != nil {
		return Result{}, fmt.Errorf("write %q: %w", arguments.Path, err)
	}
	if err := file.Sync(); err != nil {
		return Result{}, fmt.Errorf("sync %q: %w", arguments.Path, err)
	}
	if err := file.Close(); err != nil {
		return Result{}, fmt.Errorf("close %q: %w", arguments.Path, err)
	}
	complete = true
	return Result{
		Content: fmt.Sprintf("CREATE_FILE_OK\npath: %s\nbytes: %d", arguments.Path, len(arguments.Content)),
		Changed: true,
	}, nil
}
