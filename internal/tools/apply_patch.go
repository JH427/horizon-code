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

const maxPatchOperations = 64

type ApplyPatch struct {
	workspace string
	maxBytes  int64
	rename    func(string, string) error
	remove    func(string) error
}

type applyPatchArguments struct {
	Patches []patchOperation `json:"patches"`
}

type patchOperation struct {
	Path       string `json:"path"`
	OldText    string `json:"old_text"`
	NewText    string `json:"new_text"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

type preparedPatch struct {
	requestedPath string
	resolvedPath  string
	content       []byte
	original      []byte
	mode          os.FileMode
	replacements  int
}

func NewApplyPatch(workspace string, maxBytes int64) *ApplyPatch {
	if maxBytes <= 0 {
		maxBytes = 4 << 20
	}
	return &ApplyPatch{workspace: workspace, maxBytes: maxBytes, rename: os.Rename, remove: os.Remove}
}

func (tool *ApplyPatch) Definition() model.Tool {
	return model.Tool{
		Name:        "apply_patch",
		Description: "Atomically apply one or more exact text replacements to existing workspace files. All replacements are validated before any file changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"patches": map[string]any{
					"type":     "array",
					"minItems": 1,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path":        map[string]any{"type": "string", "description": "Workspace-relative existing file path; runtime validation enforces a 4096-byte bound"},
							"old_text":    map[string]any{"type": "string", "description": "Exact text to replace; runtime validation enforces the file-size bound"},
							"new_text":    map[string]any{"type": "string", "description": "Replacement text; runtime validation enforces the file-size bound"},
							"replace_all": map[string]any{"type": "boolean", "description": "Replace every exact match instead of requiring uniqueness"},
						},
						"required":             []string{"path", "old_text", "new_text"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []string{"patches"},
			"additionalProperties": false,
		},
	}
}

func (tool *ApplyPatch) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	var arguments applyPatchArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return Result{}, fmt.Errorf("decode apply_patch arguments: %w", err)
	}
	if len(arguments.Patches) == 0 {
		return Result{}, errors.New("apply_patch requires at least one patch")
	}
	if len(arguments.Patches) > maxPatchOperations {
		return Result{}, fmt.Errorf("apply_patch has %d patches; limit is %d", len(arguments.Patches), maxPatchOperations)
	}

	prepared := make([]preparedPatch, 0, len(arguments.Patches))
	seen := make(map[string]struct{}, len(arguments.Patches))
	for index, patch := range arguments.Patches {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		item, err := tool.preparePatch(patch)
		if err != nil {
			return Result{}, fmt.Errorf("apply_patch patch %d: %w", index+1, err)
		}
		if _, duplicate := seen[item.resolvedPath]; duplicate {
			return Result{}, fmt.Errorf("apply_patch patch %d targets a file more than once", index+1)
		}
		seen[item.resolvedPath] = struct{}{}
		prepared = append(prepared, item)
	}

	changed, err := tool.commitPatches(ctx, prepared)
	if err != nil {
		return Result{Content: "APPLY_PATCH_ERROR\n" + err.Error(), Changed: changed}, err
	}

	return Result{
		Content: fmt.Sprintf("APPLY_PATCH_OK\nfiles: %d\nreplacements: %d", len(prepared), totalReplacements(prepared)),
		Changed: true,
	}, nil
}

func (tool *ApplyPatch) preparePatch(patch patchOperation) (preparedPatch, error) {
	if patch.Path == "" {
		return preparedPatch{}, errors.New("path is required")
	}
	if len(patch.Path) > 4096 {
		return preparedPatch{}, errors.New("path exceeds the 4096-byte limit")
	}
	if patch.OldText == "" {
		return preparedPatch{}, errors.New("old_text cannot be empty")
	}
	if patch.OldText == patch.NewText {
		return preparedPatch{}, errors.New("old_text and new_text are identical")
	}
	resolved, err := resolveExistingWorkspacePath(tool.workspace, patch.Path)
	if err != nil {
		return preparedPatch{}, err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return preparedPatch{}, fmt.Errorf("stat target: %w", err)
	}
	if !info.Mode().IsRegular() {
		return preparedPatch{}, errors.New("path is not a regular file")
	}
	if info.Size() > tool.maxBytes {
		return preparedPatch{}, fmt.Errorf("target is %d bytes; limit is %d", info.Size(), tool.maxBytes)
	}
	content, err := os.ReadFile(resolved)
	if err != nil {
		return preparedPatch{}, fmt.Errorf("read target: %w", err)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return preparedPatch{}, errors.New("apply_patch supports text files only")
	}
	matches := strings.Count(string(content), patch.OldText)
	if matches == 0 {
		return preparedPatch{}, errors.New("old_text was not found")
	}
	if matches > 1 && !patch.ReplaceAll {
		return preparedPatch{}, fmt.Errorf("old_text matched %d times; provide a unique match or set replace_all", matches)
	}
	replacements := 1
	if patch.ReplaceAll {
		replacements = matches
	}
	updated := []byte(strings.Replace(string(content), patch.OldText, patch.NewText, replacements))
	if int64(len(updated)) > tool.maxBytes {
		return preparedPatch{}, fmt.Errorf("patched content would be %d bytes; limit is %d", len(updated), tool.maxBytes)
	}
	return preparedPatch{
		requestedPath: patch.Path,
		resolvedPath:  resolved,
		content:       updated,
		original:      append([]byte(nil), content...),
		mode:          info.Mode().Perm(),
		replacements:  replacements,
	}, nil
}

type stagedPatch struct {
	preparedPatch
	stagedPath    string
	backupPath    string
	originalMoved bool
	promoted      bool
}

func (tool *ApplyPatch) commitPatches(ctx context.Context, prepared []preparedPatch) (bool, error) {
	staged := make([]stagedPatch, 0, len(prepared))
	defer func() {
		for _, patch := range staged {
			_ = os.Remove(patch.stagedPath)
		}
	}()

	for _, patch := range prepared {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		stagedPath, err := stagePatchFile(patch)
		if err != nil {
			return false, fmt.Errorf("stage patch for %q: %w", patch.requestedPath, err)
		}
		backupPath, err := reservePatchPath(filepath.Dir(patch.resolvedPath), ".horizon-code-patch-old-*")
		if err != nil {
			_ = os.Remove(stagedPath)
			return false, fmt.Errorf("reserve rollback path for %q: %w", patch.requestedPath, err)
		}
		staged = append(staged, stagedPatch{preparedPatch: patch, stagedPath: stagedPath, backupPath: backupPath})
	}

	mutationStarted := false
	for index := range staged {
		patch := &staged[index]
		if err := ctx.Err(); err != nil {
			return mutationStarted, tool.rollbackPatches(staged, index-1, err)
		}
		if err := tool.rename(patch.resolvedPath, patch.backupPath); err != nil {
			return mutationStarted, tool.rollbackPatches(staged, index-1, fmt.Errorf("prepare promotion for %q: %w", patch.requestedPath, err))
		}
		patch.originalMoved = true
		mutationStarted = true
		captured, err := os.ReadFile(patch.backupPath)
		if err != nil {
			return true, tool.rollbackPatches(staged, index, fmt.Errorf("verify captured original for %q: %w", patch.requestedPath, err))
		}
		if !bytes.Equal(captured, patch.original) {
			return true, tool.rollbackPatches(staged, index, fmt.Errorf("concurrent change detected for %q", patch.requestedPath))
		}
		if err := tool.rename(patch.stagedPath, patch.resolvedPath); err != nil {
			return true, tool.rollbackPatches(staged, index, fmt.Errorf("promote patch for %q: %w", patch.requestedPath, err))
		}
		patch.promoted = true
	}

	for index := range staged {
		if err := tool.remove(staged[index].backupPath); err != nil {
			return true, fmt.Errorf("remove patch rollback file for %q: %w", staged[index].requestedPath, err)
		}
		staged[index].backupPath = ""
	}
	return true, nil
}

func stagePatchFile(patch preparedPatch) (string, error) {
	temporary, err := os.CreateTemp(filepath.Dir(patch.resolvedPath), ".horizon-code-patch-new-*")
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	complete := false
	defer func() {
		_ = temporary.Close()
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if err := temporary.Chmod(patch.mode); err != nil {
		return "", err
	}
	if _, err := temporary.Write(patch.content); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	complete = true
	return path, nil
}

func reservePatchPath(directory, pattern string) (string, error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func (tool *ApplyPatch) rollbackPatches(staged []stagedPatch, last int, cause error) error {
	rollbackErrors := []error{cause}
	for index := last; index >= 0; index-- {
		patch := &staged[index]
		if patch.promoted {
			if err := os.Remove(patch.resolvedPath); err != nil && !os.IsNotExist(err) {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("remove promoted patch for %q: %w", patch.requestedPath, err))
				continue
			}
		}
		if patch.originalMoved {
			if err := tool.rename(patch.backupPath, patch.resolvedPath); err != nil {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("restore %q: %w", patch.requestedPath, err))
			} else {
				patch.backupPath = ""
			}
		}
	}
	return fmt.Errorf("apply_patch transaction failed: %w", errors.Join(rollbackErrors...))
}

func totalReplacements(patches []preparedPatch) int {
	total := 0
	for _, patch := range patches {
		total += patch.replacements
	}
	return total
}
