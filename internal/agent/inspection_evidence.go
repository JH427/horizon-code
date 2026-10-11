package agent

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type inspectionExcerpt struct {
	source    string
	text      string
	task      bool
	truncated bool
}

func selectInspectionExcerpt(arguments, content string) inspectionExcerpt {
	var args struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal([]byte(arguments), &args)
	source := args.Path
	if source == "" {
		source = "read_file"
	}
	base := strings.ToLower(filepath.Base(source))
	task := base == "task.md" || base == "prompt.md" || base == "spec.md" || base == "requirements.md"
	truncated := strings.Contains(content, "truncated: true")
	if _, body, ok := strings.Cut(content, "\ncontent:\n"); ok {
		content = body
	}
	var lines []string
	for _, line := range strings.Split(content, "\n") {
		if prefix, body, ok := strings.Cut(line, "|"); ok {
			if _, err := strconv.Atoi(prefix); err == nil {
				line = body
			}
		}
		// A single fixture/literal must not consume the task evidence allowance.
		// Mark the omission so incomplete evidence can never enable fast routing.
		if !task && len(line) > 240 {
			lines = append(lines, "[long data/code line omitted]")
			truncated = true
			continue
		}
		lines = append(lines, line)
	}
	limit := 256
	if task {
		limit = 1200
	}
	text, cut := truncateDecisionInput(strings.Join(lines, "\n"), limit)
	return inspectionExcerpt{source: source, text: text, task: task, truncated: truncated || cut}
}

func renderInspectionEvidence(excerpts []inspectionExcerpt) (string, bool) {
	ordered := append([]inspectionExcerpt(nil), excerpts...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].task && !ordered[j].task })
	var text strings.Builder
	truncated := false
	for _, excerpt := range ordered {
		truncated = truncated || excerpt.truncated
		source, cut := truncateDecisionInput(excerpt.source, 128)
		truncated = truncated || cut
		text.WriteString("\nFILE " + source + "\n" + excerpt.text + "\n")
	}
	bounded, cut := truncateDecisionInput(text.String(), 1800)
	return bounded, truncated || cut
}
