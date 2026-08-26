package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JH427/horizon-code/internal/model"
)

type ReadFile struct {
	workspace string
	maxLines  int
}

type ReadFileResult struct {
	Content    string
	TotalLines int
	ShownLines int
	Truncated  bool
}

type readFileArguments struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
}

func NewReadFile(workspace string, maxLines int) *ReadFile {
	if maxLines <= 0 {
		maxLines = 500
	}
	return &ReadFile{workspace: workspace, maxLines: maxLines}
}

func (tool *ReadFile) Definition() model.Tool {
	return model.Tool{
		Name:        "read_file",
		Description: "Read numbered lines from a UTF-8 text file inside the current workspace.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":       map[string]any{"type": "string", "description": "Workspace-relative file path"},
				"start_line": map[string]any{"type": "integer", "minimum": 1},
				"end_line":   map[string]any{"type": "integer", "minimum": 1},
			},
			"required":             []string{"path"},
			"additionalProperties": false,
		},
	}
}

func (tool *ReadFile) Run(ctx context.Context, rawArguments []byte) (Result, error) {
	result, err := tool.Execute(ctx, rawArguments)
	if err != nil {
		return Result{}, err
	}
	content := fmt.Sprintf(
		"READ_FILE_OK\ntotal_lines: %d\nshown_lines: %d\ntruncated: %t\ncontent:\n%s",
		result.TotalLines, result.ShownLines, result.Truncated, result.Content,
	)
	return Result{Content: content}, nil
}

func (tool *ReadFile) Execute(ctx context.Context, rawArguments []byte) (ReadFileResult, error) {
	if err := ctx.Err(); err != nil {
		return ReadFileResult{}, err
	}
	var arguments readFileArguments
	if err := decodeStrictArguments(rawArguments, &arguments); err != nil {
		return ReadFileResult{}, fmt.Errorf("decode read_file arguments: %w", err)
	}
	if arguments.Path == "" {
		return ReadFileResult{}, errors.New("read_file path is required")
	}
	if filepath.IsAbs(arguments.Path) {
		return ReadFileResult{}, errors.New("read_file path must be workspace-relative")
	}
	workspace, err := filepath.EvalSymlinks(tool.workspace)
	if err != nil {
		return ReadFileResult{}, fmt.Errorf("resolve workspace: %w", err)
	}
	candidate := filepath.Join(workspace, filepath.Clean(arguments.Path))
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return ReadFileResult{}, fmt.Errorf("resolve %q: %w", arguments.Path, err)
	}
	relative, err := filepath.Rel(workspace, resolved)
	if err != nil {
		return ReadFileResult{}, fmt.Errorf("check workspace path: %w", err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ReadFileResult{}, errors.New("read_file path resolves outside the workspace")
	}

	file, err := os.Open(resolved)
	if err != nil {
		return ReadFileResult{}, fmt.Errorf("open %q: %w", arguments.Path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ReadFileResult{}, fmt.Errorf("stat %q: %w", arguments.Path, err)
	}
	if !info.Mode().IsRegular() {
		return ReadFileResult{}, errors.New("read_file path is not a regular file")
	}

	startLine := arguments.StartLine
	if startLine == 0 {
		startLine = 1
	}
	if startLine < 1 || arguments.EndLine < 0 || (arguments.EndLine != 0 && arguments.EndLine < startLine) {
		return ReadFileResult{}, errors.New("read_file line range is invalid")
	}

	var output strings.Builder
	totalLines := 0
	shownLines := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return ReadFileResult{}, err
		}
		totalLines++
		line := scanner.Text()
		if strings.IndexByte(line, 0) >= 0 {
			return ReadFileResult{}, errors.New("read_file supports text files only")
		}
		if totalLines < startLine || (arguments.EndLine != 0 && totalLines > arguments.EndLine) {
			continue
		}
		if shownLines >= tool.maxLines {
			continue
		}
		fmt.Fprintf(&output, "%d|%s\n", totalLines, line)
		shownLines++
	}
	if err := scanner.Err(); err != nil {
		return ReadFileResult{}, fmt.Errorf("read %q: %w", arguments.Path, err)
	}
	requestedLines := totalLines - startLine + 1
	if arguments.EndLine != 0 && requestedLines > arguments.EndLine-startLine+1 {
		requestedLines = arguments.EndLine - startLine + 1
	}
	if requestedLines < 0 {
		requestedLines = 0
	}
	return ReadFileResult{
		Content:    strings.TrimSuffix(output.String(), "\n"),
		TotalLines: totalLines,
		ShownLines: shownLines,
		Truncated:  shownLines < requestedLines,
	}, nil
}
