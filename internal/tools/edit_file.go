package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JH427/horizon-code/internal/model"
)

type EditFile struct {
	workspace string
	maxBytes  int64
}

type editFileArguments struct {
	Path       string `json:"path"`
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func NewEditFile(workspace string, maxBytes int64) *EditFile {
	if maxBytes <= 0 {
		maxBytes = 4 << 20
	}
	return &EditFile{workspace: workspace, maxBytes: maxBytes}
}

func (tool *EditFile) Definition() model.Tool {
	return model.Tool{
		Name:        "edit_file",
		Description: "Replace an exact text match in an existing workspace file. By default the match must occur exactly once.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":        map[string]any{"type": "string", "description": "Workspace-relative file path"},
				"old_text":    map[string]any{"type": "string", "description": "Exact text to replace"},
				"new_text":    map[string]any{"type": "string", "description": "Replacement text"},
				"replace_all": map[string]any{"type": "boolean", "description": "Replace every exact match instead of requiring uniqueness"},
			},
			"required":             []string{"path", "old_text", "new_text"},
			"additionalProperties": false,
		},
	}
}

func (tool *EditFile) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var arguments editFileArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode edit_file arguments: %w", err)
	}
	if arguments.Path == "" {
		return Result{}, errors.New("edit_file path is required")
	}
	if arguments.OldText == "" {
		return Result{}, errors.New("edit_file old_text cannot be empty")
	}
	if arguments.OldText == arguments.NewText {
		return Result{}, errors.New("edit_file old_text and new_text are identical")
	}
	resolved, err := resolveExistingWorkspacePath(tool.workspace, arguments.Path)
	if err != nil {
		return Result{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Result{}, fmt.Errorf("stat edit target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return Result{}, errors.New("edit_file path is not a regular file")
	}
	if info.Size() > tool.maxBytes {
		return Result{}, fmt.Errorf("edit_file target is %d bytes; limit is %d", info.Size(), tool.maxBytes)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return Result{}, fmt.Errorf("read edit target: %w", err)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return Result{}, errors.New("edit_file supports text files only")
	}
	matches := strings.Count(string(content), arguments.OldText)
	if matches == 0 {
		return Result{}, errors.New("edit_file old_text was not found")
	}
	if matches > 1 && !arguments.ReplaceAll {
		return Result{}, fmt.Errorf("edit_file old_text matched %d times; provide a unique match or set replace_all", matches)
	}
	replacements := 1
	if arguments.ReplaceAll {
		replacements = matches
	}
	updated := strings.Replace(string(content), arguments.OldText, arguments.NewText, replacements)
	if int64(len(updated)) > tool.maxBytes {
		return Result{}, fmt.Errorf("edited content would be %d bytes; limit is %d", len(updated), tool.maxBytes)
	}
	if err := writeAtomic(resolved, []byte(updated), info.Mode().Perm()); err != nil {
		return Result{}, err
	}
	return Result{
		Content: fmt.Sprintf("EDIT_FILE_OK\npath: %s\nreplacements: %d\nbytes_before: %d\nbytes_after: %d", arguments.Path, replacements, len(content), len(updated)),
		Changed: true,
	}, nil
}

func writeAtomic(path string, content []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".horizon-code-edit-*")
	if err != nil {
		return fmt.Errorf("create temporary edit file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("preserve edit target mode: %w", err)
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary edit file: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary edit file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary edit file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace edit target: %w", err)
	}
	return nil
}
