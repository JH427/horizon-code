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

type ListFiles struct {
	workspace  string
	maxEntries int
}

type listFilesArguments struct {
	Path string `json:"path,omitempty"`
}

func NewListFiles(workspace string, maxEntries int) *ListFiles {
	if maxEntries <= 0 {
		maxEntries = 200
	}
	return &ListFiles{workspace: workspace, maxEntries: maxEntries}
}

func (tool *ListFiles) Definition() model.Tool {
	return model.Tool{
		Name:        "list_files",
		Description: "List immediate files and directories without reading their contents. Use this before search_code when inspecting a workspace or directory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Optional workspace-relative directory; defaults to ."},
			},
			"additionalProperties": false,
		},
	}
}

func (tool *ListFiles) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var arguments listFilesArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode list_files arguments: %w", err)
	}
	if arguments.Path == "" {
		arguments.Path = "."
	}
	resolved, err := resolveExistingWorkspacePath(tool.workspace, arguments.Path)
	if err != nil {
		return Result{}, fmt.Errorf("list_files: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return Result{}, fmt.Errorf("stat %q: %w", arguments.Path, err)
	}
	if !info.IsDir() {
		return Result{}, errors.New("list_files path is not a directory")
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return Result{}, fmt.Errorf("read directory %q: %w", arguments.Path, err)
	}
	truncated := len(entries) > tool.maxEntries
	if truncated {
		entries = entries[:tool.maxEntries]
	}
	var lines []string
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		kind := "file"
		detail := ""
		if entry.IsDir() {
			kind = "dir"
		} else if entry.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		} else if entryInfo, infoErr := entry.Info(); infoErr == nil {
			detail = fmt.Sprintf(" (%d bytes)", entryInfo.Size())
		}
		lines = append(lines, fmt.Sprintf("[%s] %s%s", kind, filepath.ToSlash(entry.Name()), detail))
	}
	content := fmt.Sprintf("LIST_FILES_OK\npath: %s\nentries: %d\ntruncated: %t\ncontent:\n%s", filepath.ToSlash(arguments.Path), len(entries), truncated, strings.Join(lines, "\n"))
	return Result{Content: content}, nil
}
