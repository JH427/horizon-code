package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func resolveWorkspacePath(workspace, requested string) (string, error) {
	if requested == "" {
		return "", errors.New("workspace-relative path is required")
	}
	if filepath.IsAbs(requested) {
		return "", errors.New("path must be workspace-relative")
	}
	cleaned := filepath.Clean(requested)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("path escapes the workspace")
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolve workspace: %w", err)
	}

	parts := strings.Split(cleaned, string(filepath.Separator))
	existing := resolvedWorkspace
	missingAt := len(parts)
	for index, part := range parts {
		candidate := filepath.Join(existing, part)
		if _, err := os.Lstat(candidate); err != nil {
			if !os.IsNotExist(err) {
				return "", fmt.Errorf("inspect %q: %w", requested, err)
			}
			missingAt = index
			break
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve %q: %w", requested, err)
		}
		if err := ensureWithinWorkspace(resolvedWorkspace, resolved); err != nil {
			return "", err
		}
		existing = resolved
	}

	if missingAt == len(parts) {
		return existing, nil
	}
	return filepath.Join(append([]string{existing}, parts[missingAt:]...)...), nil
}

func resolveWorkspaceEntry(workspace, requested string) (string, os.FileInfo, error) {
	if requested == "" {
		return "", nil, errors.New("workspace-relative path is required")
	}
	if filepath.IsAbs(requested) {
		return "", nil, errors.New("path must be workspace-relative")
	}
	cleaned := filepath.Clean(requested)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", nil, errors.New("path escapes the workspace")
	}
	parent, err := resolveExistingWorkspacePath(workspace, filepath.Dir(cleaned))
	if err != nil {
		return "", nil, err
	}
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return "", nil, fmt.Errorf("stat path parent: %w", err)
	}
	if !parentInfo.IsDir() {
		return "", nil, errors.New("path parent is not a directory")
	}
	entry := filepath.Join(parent, filepath.Base(cleaned))
	info, err := os.Lstat(entry)
	if err != nil {
		return "", nil, fmt.Errorf("inspect %q: %w", requested, err)
	}
	return entry, info, nil
}

func ensureWithinWorkspace(workspace, candidate string) error {
	relative, err := filepath.Rel(workspace, candidate)
	if err != nil {
		return fmt.Errorf("check workspace path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("path resolves outside the workspace")
	}
	return nil
}
