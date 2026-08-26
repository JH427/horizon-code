package instructions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type Document struct {
	Content string
	Paths   []string
}

func Load(workspace string, maxBytes int) (Document, error) {
	if maxBytes <= 0 {
		maxBytes = 64 << 10
	}
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return Document{}, fmt.Errorf("resolve workspace for HORIZON.md: %w", err)
	}
	var directories []string
	for directory := resolved; ; directory = filepath.Dir(directory) {
		directories = append(directories, directory)
		parent := filepath.Dir(directory)
		if parent == directory {
			break
		}
	}
	slices.Reverse(directories)

	var document Document
	var sections []string
	total := 0
	for _, directory := range directories {
		path := filepath.Join(directory, "HORIZON.md")
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return Document{}, fmt.Errorf("inspect %s: %w", path, statErr)
		}
		if !info.Mode().IsRegular() {
			return Document{}, fmt.Errorf("%s must be a regular file", path)
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return Document{}, fmt.Errorf("read %s: %w", path, readErr)
		}
		total += len(content)
		if total > maxBytes {
			return Document{}, fmt.Errorf("HORIZON.md instructions exceed %d-byte budget", maxBytes)
		}
		document.Paths = append(document.Paths, path)
		sections = append(sections, fmt.Sprintf("## %s\n%s", path, strings.TrimSpace(string(content))))
	}
	document.Content = strings.Join(sections, "\n\n")
	return document, nil
}
