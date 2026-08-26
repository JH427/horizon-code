package tools

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func resolveExistingWorkspacePath(workspace, requested string) (string, error) {
	if requested == "" {
		return "", errors.New("workspace-relative path is required")
	}
	if filepath.IsAbs(requested) {
		return "", errors.New("path must be workspace-relative")
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}
	candidate := filepath.Join(resolvedWorkspace, filepath.Clean(requested))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", requested, err)
	}
	relative, err := filepath.Rel(resolvedWorkspace, resolved)
	if err != nil {
		return "", fmt.Errorf("check workspace path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("path resolves outside the workspace")
	}
	return resolved, nil
}
