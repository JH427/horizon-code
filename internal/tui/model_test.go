package tui

import (
	"context"
	"image/color"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/JH427/horizon-code/internal/agent"
)

type noopRunner struct{}

func (noopRunner) Run(context.Context, string, func(agent.Event)) (string, error) {
	return "", nil
}

func TestModelRendersToolLifecycleAndAssistantText(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	state := updated.(Model)
	updated, _ = state.Update(agentEventMsg{event: agent.Event{Kind: agent.EventReasoningDelta, Text: "I should inspect the marker.\x1b]0;PWNED\a"}})
	state = updated.(Model)
	updated, _ = state.Update(agentEventMsg{event: agent.Event{Kind: agent.EventToolStarted, ToolName: "read_file", CallID: "call-1", Arguments: `{"path":"marker.txt"}`}})
	state = updated.(Model)
	updated, _ = state.Update(agentEventMsg{event: agent.Event{Kind: agent.EventToolCompleted, ToolName: "read_file", CallID: "call-1", Text: "MARKER_CONTENT"}})
	state = updated.(Model)
	updated, _ = state.Update(agentEventMsg{event: agent.Event{Kind: agent.EventAssistantDelta, Text: "HORIZON_GREEN"}})
	state = updated.(Model)
	updated, _ = state.Update(agentEventMsg{event: agent.Event{Kind: agent.EventVerification, Text: "VERIFICATION_REQUIRED"}})
	state = updated.(Model)

	view := ansi.Strip(state.View().Content)
	for _, want := range []string{"HORIZON CODE", "THINK", "I should inspect the marker.", "read_file", "marker.txt", "MARKER_CONTENT", "HORIZON_GREEN", "verification required", "qwen38-27b"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view does not contain %q:\n%s", want, view)
		}
	}
	if strings.Contains(state.View().Content, "PWNED") {
		t.Fatalf("view contains terminal-control payload:\n%s", state.View().Content)
	}
	if strings.Contains(view, "call-1") || strings.Contains(view, `"path": "marker.txt"`) {
		t.Fatalf("compact successful read leaked call internals:\n%s", view)
	}
	if count := strings.Count(view, "⚙ read_file"); count != 1 {
		t.Fatalf("tool call rendered %d rows, want one updated row:\n%s", count, view)
	}
}

func TestComposerStartsAsBlankWritingSpace(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	if model.composer.Placeholder != "" {
		t.Fatalf("composer placeholder = %q, want blank", model.composer.Placeholder)
	}
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := ansi.Strip(updated.(Model).View().Content)
	if strings.Contains(strings.ToLower(view), "ask horizon") {
		t.Fatalf("blank composer renders instruction copy:\n%s", view)
	}
}

func TestNewModelStartsWithoutTranscriptFluff(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	if len(model.blocks) != 0 {
		t.Fatalf("new model has %d transcript blocks, want none: %#v", len(model.blocks), model.blocks)
	}
}

func TestCapabilityModeIsAlwaysVisibleInWideFlightDeck(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.SetCapability("operator")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	view := ansi.Strip(updated.(Model).View().Content)
	if !strings.Contains(view, "OPERATOR") {
		t.Fatalf("operator capability is not visible in flight deck:\n%s", view)
	}
}

func TestSafeTextRemovesTerminalControlsButPreservesLayout(t *testing.T) {
	got := safeText("left\rright\bX\a\tkeep\nnext\x7f\u0085")
	want := "leftrightX\tkeep\nnext"
	if got != want {
		t.Fatalf("safeText() = %q, want %q", got, want)
	}
}

func TestThemeUsesOmarchyTerminalPalette(t *testing.T) {
	want := []color.Color{
		lipgloss.Color("4"),
		lipgloss.Color("12"),
		lipgloss.Color("6"),
		lipgloss.Color("8"),
		lipgloss.Color("1"),
	}
	got := []color.Color{accent, accentBright, cyan, muted, danger}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("semantic color %d = %q, want terminal palette slot %q", index, got[index], want[index])
		}
	}
}

func TestThemeDoesNotPaintCustomBackgrounds(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolStarted, ToolName: "read_file", CallID: "call-1", Arguments: `{"path":"gallery.html"}`})
	model.applyAgentEvent(agent.Event{Kind: agent.EventAssistantDelta, Text: "# Heading\n\n`inline`\n\n```go\nfmt.Println(1)\n```"})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	raw := updated.(Model).View().Content
	backgroundSGR := regexp.MustCompile(`\x1b\[(?:4[0-7]|10[0-7])m|\x1b\[48[;:]`)
	if backgroundSGR.MatchString(raw) || strings.Contains(raw, "\x1b[38;2;") || strings.Contains(raw, "\x1b[38;5;") {
		t.Fatalf("TUI bypasses the Omarchy terminal palette/background: %q", raw)
	}
}

func TestViewBeforeFirstResizeDoesNotShowSplashCopy(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	view := ansi.Strip(model.View().Content)
	if strings.TrimSpace(view) != "" {
		t.Fatalf("pre-resize view contains splash copy: %q", view)
	}
}

func TestViewEnablesMouseScrollingForTranscript(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := updated.(Model).View()
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("mouse mode = %v, want cell motion for wheel/touch scrolling", view.MouseMode)
	}
}

func TestStreamingEventsPreserveManualTranscriptScroll(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 60
	model.height = 14
	model.resize()
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: strings.Repeat("older transcript line\n", 40)})
	model.refreshTranscript()
	model.viewport.PageUp()
	before := model.viewport.YOffset()
	if model.viewport.AtBottom() {
		t.Fatal("test setup did not scroll away from bottom")
	}

	model.applyAgentEvent(agent.Event{Kind: agent.EventAssistantDelta, Text: "new streamed text"})
	model.refreshTranscript()
	if got := model.viewport.YOffset(); got != before {
		t.Fatalf("streaming event moved manual scroll from %d to %d", before, got)
	}
}

func TestResizePreservesFollowLatestWhenTerminalShrinks(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	model = updated.(Model)
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: strings.Repeat("transcript line\n", 80)})
	model.refreshTranscript()
	if !model.viewport.AtBottom() {
		t.Fatal("test setup is not following latest")
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	model = updated.(Model)
	if !model.viewport.AtBottom() {
		t.Fatalf("terminal shrink stopped following latest: offset=%d lines=%d height=%d", model.viewport.YOffset(), model.viewport.TotalLineCount(), model.viewport.Height())
	}
}

func TestResizePreservesManualScrollAcrossReflow(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	model = updated.(Model)
	line := strings.Repeat("x", 65) + "\n"
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: strings.Repeat(line, 200)})
	model.refreshTranscript()
	model.viewport.PageUp()
	if model.viewport.AtBottom() || model.viewport.AtTop() {
		t.Fatal("test setup lacks a manual middle scroll position")
	}

	updated, _ = model.Update(tea.WindowSizeMsg{Width: 76, Height: 26})
	model = updated.(Model)
	if model.viewport.AtBottom() {
		t.Fatalf("resize snapped manual scroll to bottom: offset=%d lines=%d height=%d", model.viewport.YOffset(), model.viewport.TotalLineCount(), model.viewport.Height())
	}
	if model.viewport.AtTop() {
		t.Fatalf("resize snapped manual scroll to top: offset=%d", model.viewport.YOffset())
	}
}

func TestTypingInComposerDoesNotScrollTranscript(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 60
	model.height = 14
	model.resize()
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: strings.Repeat("transcript line\n", 40)})
	model.refreshTranscript()
	before := model.viewport.YOffset()

	updated, _ := model.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	state := updated.(Model)
	if got := state.viewport.YOffset(); got != before {
		t.Fatalf("typing b moved transcript from %d to %d", before, got)
	}
	if state.composer.Value() != "b" {
		t.Fatalf("composer value = %q, want b", state.composer.Value())
	}
}

func TestPageUpNavigatesTranscriptWithoutEditingComposer(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 60
	model.height = 14
	model.resize()
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: strings.Repeat("transcript line\n", 40)})
	model.refreshTranscript()
	before := model.viewport.YOffset()

	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	state := updated.(Model)
	if got := state.viewport.YOffset(); got >= before {
		t.Fatalf("page up left transcript at %d, started at %d", got, before)
	}
	if state.composer.Value() != "" {
		t.Fatalf("page up changed composer to %q", state.composer.Value())
	}
}

func TestPageDownNavigatesTranscriptWithoutEditingComposer(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 60
	model.height = 14
	model.resize()
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: strings.Repeat("transcript line\n", 40)})
	model.refreshTranscript()
	model.viewport.PageUp()
	before := model.viewport.YOffset()

	updated, _ := model.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	state := updated.(Model)
	if got := state.viewport.YOffset(); got <= before {
		t.Fatalf("page down left transcript at %d, started at %d", got, before)
	}
	if state.composer.Value() != "" {
		t.Fatalf("page down changed composer to %q", state.composer.Value())
	}
}

func TestLeanShellIsDenseAndResponsive(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	wide := ansi.Strip(updated.(Model).View().Content)
	for _, want := range []string{"HORIZON", "/workspace", "ready", "qwen38-27b", "ctx —", "tok/s —", "enter send", "pgup/dn scroll"} {
		if !strings.Contains(wide, want) {
			t.Fatalf("wide shell does not contain %q:\n%s", want, wide)
		}
	}
	for _, unwanted := range []string{"QWEN-NATIVE GO HARNESS", "╭", "╮", "╰", "╯"} {
		if strings.Contains(wide, unwanted) {
			t.Fatalf("wide shell contains obsolete chrome %q:\n%s", unwanted, wide)
		}
	}

	state := updated.(Model)
	for _, terminalWidth := range []int{42, 18} {
		resized, _ := state.Update(tea.WindowSizeMsg{Width: terminalWidth, Height: 16})
		state = resized.(Model)
		narrow := ansi.Strip(state.View().Content)
		for _, line := range strings.Split(narrow, "\n") {
			if ansi.StringWidth(line) > terminalWidth {
				t.Fatalf("%d-column shell line is %d cells wide: %q", terminalWidth, ansi.StringWidth(line), line)
			}
		}
	}
}

func TestTranscriptUsesCompactSemanticHierarchy(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 100
	model.height = 30
	model.resize()
	model.blocks = append(model.blocks, block{kind: blockUser, text: "inspect marker.txt"})
	model.applyAgentEvent(agent.Event{Kind: agent.EventReasoningDelta, Text: "Need the file."})
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolStarted, ToolName: "read_file", CallID: "call-1", Arguments: `{"path":"marker.txt"}`})
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolCompleted, ToolName: "read_file", CallID: "call-1", Text: "MARKER"})
	model.applyAgentEvent(agent.Event{Kind: agent.EventAssistantDelta, Text: "Found it."})
	model.refreshTranscript()
	view := ansi.Strip(model.viewport.View())

	labels := []string{"YOU ›", "THINK", "⚙ read_file", "◆ HORIZON"}
	last := -1
	for _, label := range labels {
		position := strings.Index(view, label)
		if position < 0 {
			t.Fatalf("transcript does not contain hierarchy label %q:\n%s", label, view)
		}
		if position <= last {
			t.Fatalf("hierarchy label %q is out of order:\n%s", label, view)
		}
		last = position
	}
	for _, detail := range []string{"marker.txt", "MARKER"} {
		if !strings.Contains(view, detail) {
			t.Fatalf("compact hierarchy dropped detail %q:\n%s", detail, view)
		}
	}
}

func TestNarrowToolCardRepeatsRailOnWrappedLines(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 42
	model.height = 18
	model.resize()
	arguments := `{"program":"go","args":["test","./internal/parser","-count=1"],"purpose":"inspect"}`
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolStarted, ToolName: "exec_command", CallID: "call-2", Arguments: arguments})
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolCompleted, ToolName: "exec_command", CallID: "call-2", Text: "COMMAND_RESULT\nexit_code: 0"})
	model.refreshTranscript()
	view := ansi.Strip(model.viewport.View())
	if rails := strings.Count(view, "┃"); rails < 2 {
		t.Fatalf("wrapped tool card has %d rail lines, want at least 2:\n%s", rails, view)
	}
	if !strings.Contains(view, "./internal/parser") {
		t.Fatalf("tool card split a path that fits on its own line:\n%s", view)
	}
}

func TestFencedCodeBlocksRenderWithLanguageAndRail(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 48
	model.height = 18
	model.resize()
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: "Before.\n\n```go\nfmt.Println(\"hello\")\n```\n\nAfter."})
	model.refreshTranscript()
	view := ansi.Strip(model.viewport.GetContent())
	for _, want := range []string{"Before.", "┌─ go", "│ fmt.Println(\"hello\")", "└─", "After."} {
		if !strings.Contains(view, want) {
			t.Fatalf("rendered markdown lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "```") {
		t.Fatalf("rendered markdown leaked fence markers:\n%s", view)
	}
	for _, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) > model.viewport.Width() {
			t.Fatalf("code block line is %d cells wide, viewport is %d: %q", ansi.StringWidth(line), model.viewport.Width(), line)
		}
	}
}

func TestLongerMarkdownFenceCanContainShorterFence(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.width = 48
	model.height = 18
	model.resize()
	model.blocks = append(model.blocks, block{kind: blockAssistant, text: "````go\n```\ninside\n````"})
	model.refreshTranscript()
	view := ansi.Strip(model.viewport.GetContent())
	for _, want := range []string{"┌─ go", "│ ```", "│ inside", "└─"} {
		if !strings.Contains(view, want) {
			t.Fatalf("long fenced block lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "````") {
		t.Fatalf("long fence marker leaked into output:\n%s", view)
	}
}

func TestTelemetryRailConsumesAgentEventsAndShowsOperationalAndNativeContext(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.SetServedContext(262144)
	model.applyAgentEvent(agent.Event{Kind: agent.EventTelemetry, Telemetry: &agent.Telemetry{
		ContextTokens: 12288, CompletionTokens: 1024, TokensPerSecond: 42.75,
		HasContext: true, HasCompletion: true, HasTokensPerSecond: true,
		Step: 2, MaxSteps: 16, ActiveContextLimit: 48 * 1024,
	}})

	telemetry := model.renderTelemetry()
	for _, want := range []string{"ctx 12.3k/49.2k", "native 262k", "42.8 tok/s avg", "out 1.0k", "step 2/16"} {
		if !strings.Contains(telemetry, want) {
			t.Fatalf("telemetry does not contain %q: %q", want, telemetry)
		}
	}
}

func TestTelemetryRailMarksClientContextEstimate(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.applyAgentEvent(agent.Event{Kind: agent.EventTelemetry, Telemetry: &agent.Telemetry{
		ContextTokens: 8192, HasContext: true, ActiveContextLimit: 48 * 1024, Estimated: true,
	}})
	if telemetry := model.renderTelemetry(); !strings.Contains(telemetry, "ctx ~8.2k/49.2k") {
		t.Fatalf("estimated telemetry is not marked: %q", telemetry)
	}
}

func TestTelemetryRailPreservesReportedZeroAndMergesFinalUsage(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.applyAgentEvent(agent.Event{Kind: agent.EventTelemetry, Telemetry: &agent.Telemetry{
		HasContext: true, ContextTokens: 0, HasTokensPerSecond: true, TokensPerSecond: 0,
		Step: 1, MaxSteps: 16, ActiveContextLimit: 48 * 1024,
	}})
	model.applyAgentEvent(agent.Event{Kind: agent.EventTelemetry, Telemetry: &agent.Telemetry{
		HasContext: true, ContextTokens: 12, HasCompletion: true, CompletionTokens: 2, Final: true,
		Step: 1, MaxSteps: 16, ActiveContextLimit: 48 * 1024,
	}})
	telemetry := model.renderTelemetry()
	for _, want := range []string{"ctx 12/49.2k", "0.0 tok/s avg", "out 2"} {
		if !strings.Contains(telemetry, want) {
			t.Fatalf("merged telemetry does not contain %q: %q", want, telemetry)
		}
	}
}

func TestTelemetryRailNeverOverflowsTerminalWidth(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/a/very/long/workspace/path/that/must/not-break-the-layout")
	model.SetServedContext(262144)
	model.applyAgentEvent(agent.Event{Kind: agent.EventTelemetry, Telemetry: &agent.Telemetry{
		ContextTokens: 49151, HasContext: true, CompletionTokens: 16384, HasCompletion: true,
		TokensPerSecond: 123.45, HasTokensPerSecond: true, Step: 16, MaxSteps: 16, ActiveContextLimit: 49152,
	}})
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, line := range strings.Split(ansi.Strip(updated.(Model).View().Content), "\n") {
		if ansi.StringWidth(line) > 80 {
			t.Fatalf("line is %d cells wide, want <= 80: %q", ansi.StringWidth(line), line)
		}
	}
}

func TestSuccessfulReadFileRendersOneCompactOperatorRow(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolStarted, ToolName: "read_file", CallID: "call-1", Arguments: `{"path":"gallery.html"}`})
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolCompleted, ToolName: "read_file", CallID: "call-1", Text: "READ_FILE_OK\npath: gallery.html\ncontent:\n<html>large content</html>"})

	if len(model.blocks) != 1 {
		t.Fatalf("blocks = %#v", model.blocks)
	}
	want := "⚙ read_file  gallery.html  ✓ READ_FILE_OK"
	if model.blocks[0].text != want {
		t.Fatalf("tool row = %q, want %q", model.blocks[0].text, want)
	}
	if strings.Contains(model.blocks[0].text, "call-1") || strings.Contains(model.blocks[0].text, "<html>") || strings.Contains(model.blocks[0].text, "\n") {
		t.Fatalf("compact row leaked implementation detail: %q", model.blocks[0].text)
	}
}

func TestSuccessfulExecCommandRendersOneCompactOperatorRow(t *testing.T) {
	model := NewModel(noopRunner{}, "qwen38-27b", "/workspace")
	arguments := `{"program":"go","args":["test","./..."],"purpose":"inspect"}`
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolStarted, ToolName: "exec_command", CallID: "call-2", Arguments: arguments})
	model.applyAgentEvent(agent.Event{Kind: agent.EventToolCompleted, ToolName: "exec_command", CallID: "call-2", Text: "COMMAND_RESULT\nexit_code: 0\noutput:\nok"})

	if len(model.blocks) != 1 {
		t.Fatalf("blocks = %#v", model.blocks)
	}
	want := "⚙ exec_command  go test ./...  ✓ COMMAND_RESULT"
	if model.blocks[0].text != want {
		t.Fatalf("tool row = %q, want %q", model.blocks[0].text, want)
	}
	for _, leaked := range []string{"call-2", `"program"`, "exit_code:", "\n"} {
		if strings.Contains(model.blocks[0].text, leaked) {
			t.Fatalf("compact row leaked %q: %q", leaked, model.blocks[0].text)
		}
	}
}

func TestCompactToolTargetSummarizesKnownTools(t *testing.T) {
	tests := []struct {
		name      string
		arguments string
		want      string
	}{
		{name: "list_files", arguments: `{}`, want: "."},
		{name: "search_code", arguments: `{"query":"TODO","path":"internal"}`, want: "TODO · internal"},
		{name: "create_file", arguments: `{"path":"new.go","content":"package main"}`, want: "new.go"},
		{name: "apply_patch", arguments: `{"patches":[{"path":"a.go"},{"path":"b.go"}]}`, want: "a.go +1"},
		{name: "move_path", arguments: `{"source":"old.go","destination":"new.go"}`, want: "old.go → new.go"},
		{name: "run_command", arguments: `{"program":"go","args":["test","./..."]}`, want: "go test ./..."},
		{name: "shell", arguments: `{"command":"git status --short","purpose":"inspect"}`, want: "git status --short"},
		{name: "background_process", arguments: `{"operation":"start","program":"npm","args":["run","dev"]}`, want: "start · npm run dev"},
		{name: "background_process", arguments: `{"operation":"poll","id":"bg-1"}`, want: "poll · bg-1"},
		{name: "git_diff", arguments: `{"path":"README.md","staged":true}`, want: "staged · README.md"},
	}
	for _, test := range tests {
		t.Run(test.name+"/"+test.want, func(t *testing.T) {
			got, compact := compactToolTarget(test.name, test.arguments)
			if !compact || got != test.want {
				t.Fatalf("compactToolTarget(%q, %s) = %q, %v; want %q, true", test.name, test.arguments, got, compact, test.want)
			}
		})
	}
	if _, compact := compactToolTarget("third_party_tool", `{"value":1}`); compact {
		t.Fatal("unknown tool should retain detailed fallback")
	}
}

func TestToolResultPreviewClipsHugeLinesAndPreservesFullLogHint(t *testing.T) {
	result := "header\n" + strings.Repeat("x", 2000) + "\nfooter"
	preview := toolResultPreview(result)
	if len(preview) >= len(result) {
		t.Fatalf("preview was not clipped: %d >= %d", len(preview), len(result))
	}
	if !strings.Contains(preview, "full result is in the session log") {
		t.Fatalf("preview lacks full-log hint: %q", preview)
	}
}
