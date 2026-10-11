package agent

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTaskEvidenceSurvivesLargeFixtureReadFirst(t *testing.T) {
	fixture := selectInspectionExcerpt(`{"path":"test_solution.py"}`, "READ_FILE_OK\ntruncated: false\ncontent:\n1|import subprocess\n2|expected='"+strings.Repeat("123456", 500)+"'\n3|assert normalize(actual)==normalize(expected)")
	task := selectInspectionExcerpt(`{"path":"TASK.md"}`, "READ_FILE_OK\ntruncated: false\ncontent:\n1|# Divisors\n2|Print the positive divisors of each number from 1 to 100.")
	text, truncated := renderInspectionEvidence([]inspectionExcerpt{fixture, task})
	if !strings.HasPrefix(text, "\nFILE TASK.md\n") || !strings.Contains(text, "positive divisors") || !strings.Contains(text, "assert normalize") || strings.Contains(text, "123456") || !truncated {
		t.Fatalf("text=%q truncated=%t", text, truncated)
	}
	if strings.Contains(text, "READ_FILE_OK") || strings.Contains(text, "1|") {
		t.Fatal("protocol decoration consumed evidence allowance")
	}
}

func TestInspectionEvidenceBoundsAndUTF8(t *testing.T) {
	var excerpts []inspectionExcerpt
	for i := 0; i < 8; i++ {
		excerpts = append(excerpts, selectInspectionExcerpt(`{"path":"TASK.md"}`, strings.Repeat("界", 1000)))
	}
	text, truncated := renderInspectionEvidence(excerpts)
	if len(text) > 1800 || !utf8.ValidString(text) || !truncated {
		t.Fatalf("bytes=%d truncated=%t", len(text), truncated)
	}
}

func TestCompleteShortTaskEvidenceDoesNotClaimTruncation(t *testing.T) {
	text, truncated := renderInspectionEvidence([]inspectionExcerpt{selectInspectionExcerpt(`{"path":"TASK.md"}`, "READ_FILE_OK\ntruncated: false\ncontent:\n1|Print divisors.")})
	if truncated || !strings.Contains(text, "Print divisors.") {
		t.Fatalf("text=%q truncated=%t", text, truncated)
	}
}
