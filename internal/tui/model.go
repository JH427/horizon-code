package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/JH427/horizon-code/internal/agent"
	"github.com/JH427/horizon-code/internal/resources"
)

type Runner interface {
	Run(context.Context, string, func(agent.Event)) (string, error)
}

type ResourceSampler interface {
	Sample() resources.Snapshot
}

type blockKind int

const (
	blockSystem blockKind = iota
	blockUser
	blockAssistant
	blockReasoning
	blockTool
	blockError
)

type block struct {
	kind      blockKind
	text      string
	callID    string
	toolName  string
	arguments string
}

type Model struct {
	runner          Runner
	modelName       string
	workspace       string
	capability      string
	toolNames       []string
	resources       resources.Snapshot
	resourceSampler ResourceSampler
	// Telemetry stays presentation-only, with no agent event schema dependency.
	contextTokens    int
	contextLimit     int
	servedContext    int
	completionTokens int
	tokensPerSecond  float64
	promptPerSecond  float64
	contextEstimated bool
	contextValid     bool
	rateValid        bool
	completionValid  bool
	telemetryFinal   bool
	step             int
	maxSteps         int
	width            int
	height           int
	viewport         viewport.Model
	composer         textarea.Model
	blocks           []block
	busy             bool
	quitting         bool
	activity         string
	cancel           context.CancelFunc
	runEvents        <-chan runUpdate
}

type agentEventMsg struct{ event agent.Event }
type resourceSampleMsg struct{ snapshot resources.Snapshot }
type resourceTickMsg struct{}
type runStartedMsg struct{ events <-chan runUpdate }
type runUpdateMsg struct{ update runUpdate }

type runUpdate struct {
	event *agent.Event
	err   error
	done  bool
}

var (
	accent       = lipgloss.Color("4")
	accentBright = lipgloss.Color("12")
	cyan         = lipgloss.Color("6")
	muted        = lipgloss.Color("8")
	danger       = lipgloss.Color("1")
)

func NewModel(runner Runner, modelName, workspace string) Model {
	composer := textarea.New()
	composer.Placeholder = ""
	composer.Prompt = "❯ "
	composer.ShowLineNumbers = false
	composer.DynamicHeight = true
	composer.MinHeight = 1
	composer.MaxHeight = 5
	composer.CharLimit = 32 << 10
	composer.SetVirtualCursor(true)
	styles := composer.Styles()
	styles.Focused = textarea.StyleState{
		Base: lipgloss.NewStyle(), Text: lipgloss.NewStyle(), CursorLine: lipgloss.NewStyle(),
		EndOfBuffer: lipgloss.NewStyle(), Placeholder: lipgloss.NewStyle().Foreground(muted),
		Prompt: lipgloss.NewStyle().Foreground(accentBright), Selection: lipgloss.NewStyle().Reverse(true),
	}
	styles.Blurred = textarea.StyleState{
		Base: lipgloss.NewStyle(), Text: lipgloss.NewStyle(), CursorLine: lipgloss.NewStyle(),
		EndOfBuffer: lipgloss.NewStyle(), Placeholder: lipgloss.NewStyle().Foreground(muted),
		Prompt: lipgloss.NewStyle().Foreground(accent), Selection: lipgloss.NewStyle().Reverse(true),
	}
	styles.Cursor.Color = accentBright
	composer.SetStyles(styles)
	_ = composer.Focus()

	view := viewport.New(viewport.WithWidth(76), viewport.WithHeight(12))
	view.SoftWrap = true
	view.FillHeight = true
	return Model{
		runner: runner, modelName: compactOneLine(modelName), workspace: compactOneLine(workspace),
		viewport: view, composer: composer,
		activity: "ready", capability: "safe",
	}
}

func (model *Model) SetServedContext(tokens int) {
	model.servedContext = tokens
}

func (model *Model) SetActiveContextLimit(tokens int) {
	model.contextLimit = tokens
}

func (model *Model) SetCapability(capability string) {
	model.capability = safeText(capability)
}

func (model *Model) SetAvailableTools(names []string) {
	unique := make(map[string]struct{}, len(names))
	model.toolNames = model.toolNames[:0]
	for _, name := range names {
		name = compactOneLine(name)
		if name == "" {
			continue
		}
		if _, exists := unique[name]; exists {
			continue
		}
		unique[name] = struct{}{}
		model.toolNames = append(model.toolNames, name)
	}
	sort.Strings(model.toolNames)
}

func (model *Model) SetResourceSampler(sampler ResourceSampler) {
	model.resourceSampler = sampler
}

func (model Model) Init() tea.Cmd {
	if model.resourceSampler == nil {
		return textarea.Blink
	}
	return tea.Batch(textarea.Blink, sampleResources(model.resourceSampler))
}

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		followLatest := model.viewport.AtBottom()
		scrollPercent := model.viewport.ScrollPercent()
		model.width = message.Width
		model.height = message.Height
		model.resize()
		model.refreshTranscriptAfterResize(followLatest, scrollPercent)
		return model, nil
	case tea.KeyPressMsg:
		switch message.String() {
		case "ctrl+q":
			return model.requestQuit()
		case "ctrl+c", "esc":
			if model.busy && model.cancel != nil {
				model.cancel()
				model.activity = "cancelling"
				return model, nil
			}
			if message.String() == "ctrl+c" {
				return model, tea.Quit
			}
		case "enter":
			input := strings.TrimSpace(model.composer.Value())
			if input != "" && (strings.HasPrefix(input, "/") || !model.busy) {
				return model.submit()
			}
			if model.busy {
				return model, nil
			}
		case "pgup":
			model.viewport.PageUp()
			return model, nil
		case "pgdown":
			model.viewport.PageDown()
			return model, nil
		}
		updatedComposer, composerCommand := model.composer.Update(message)
		model.composer = updatedComposer
		return model, composerCommand
	case resourceSampleMsg:
		model.resources = message.snapshot
		if model.resourceSampler == nil {
			return model, nil
		}
		return model, scheduleResourceTick()
	case resourceTickMsg:
		if model.resourceSampler == nil {
			return model, nil
		}
		return model, sampleResources(model.resourceSampler)
	case runStartedMsg:
		model.runEvents = message.events
		return model, waitForRun(message.events)
	case runUpdateMsg:
		if message.update.event != nil {
			model.applyAgentEvent(*message.update.event)
		}
		if message.update.done {
			model.busy = false
			model.cancel = nil
			model.runEvents = nil
			if message.update.err != nil {
				model.blocks = append(model.blocks, block{kind: blockError, text: message.update.err.Error()})
				model.activity = "failed"
			} else {
				model.activity = "ready"
			}
			model.refreshTranscript()
			if model.quitting {
				return model, tea.Quit
			}
			return model, nil
		}
		model.refreshTranscript()
		return model, waitForRun(model.runEvents)
	case agentEventMsg:
		model.applyAgentEvent(message.event)
		model.refreshTranscript()
		return model, nil
	}

	var commands []tea.Cmd
	updatedComposer, composerCommand := model.composer.Update(message)
	model.composer = updatedComposer
	commands = append(commands, composerCommand)
	updatedViewport, viewportCommand := model.viewport.Update(message)
	model.viewport = updatedViewport
	commands = append(commands, viewportCommand)
	return model, tea.Batch(commands...)
}

func (model Model) View() tea.View {
	if model.width == 0 || model.height == 0 {
		view := tea.NewView("")
		view.AltScreen = true
		return view
	}
	innerWidth := max(1, model.width-2)
	header := lipgloss.JoinVertical(lipgloss.Left, model.renderHeader(innerWidth), model.renderResources(innerWidth))
	transcript := lipgloss.NewStyle().
		Width(innerWidth).
		Height(max(3, model.viewport.Height())).
		Render(model.viewport.View())
	composer := lipgloss.NewStyle().
		Width(max(1, innerWidth-2)).
		Padding(0, 1).
		Render(model.composer.View())
	controls := model.renderControls(innerWidth)
	content := lipgloss.NewStyle().Padding(0, 1).Render(
		lipgloss.JoinVertical(lipgloss.Left, header, transcript, composer, controls),
	)
	view := tea.NewView(content)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.WindowTitle = "Horizon Code"
	return view
}

func (model Model) submit() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(model.composer.Value())
	model.composer.Reset()
	if strings.HasPrefix(prompt, "/") {
		return model.submitSlashCommand(prompt)
	}
	model.blocks = append(model.blocks, block{kind: blockUser, text: prompt})
	model.busy = true
	model.activity = "thinking"
	ctx, cancel := context.WithCancel(context.Background())
	model.cancel = cancel
	events := make(chan runUpdate, 64)
	model.refreshTranscript()

	command := func() tea.Msg {
		go func() {
			_, err := model.runner.Run(ctx, prompt, func(event agent.Event) {
				eventCopy := event
				select {
				case events <- runUpdate{event: &eventCopy}:
				case <-ctx.Done():
				}
			})
			events <- runUpdate{err: err, done: true}
			close(events)
		}()
		return runStartedMsg{events: events}
	}
	return model, command
}

func (model Model) submitSlashCommand(input string) (tea.Model, tea.Cmd) {
	fields := strings.Fields(input)
	name := ""
	if len(fields) > 0 {
		name = strings.ToLower(fields[0])
	}
	if len(fields) > 1 && knownSlashCommand(name) {
		model.blocks = append(model.blocks, block{kind: blockError, text: "Usage: " + name + "\nThis command does not accept arguments."})
		model.refreshTranscript()
		return model, nil
	}
	switch name {
	case "/quit", "/exit":
		return model.requestQuit()
	case "/clear":
		if model.busy {
			model.blocks = append(model.blocks, block{kind: blockError, text: "Busy: /clear is unavailable while a turn is running. Press Esc to stop it."})
			break
		}
		model.blocks = nil
	case "/status":
		model.blocks = append(model.blocks, block{kind: blockSystem, text: model.renderStatus()})
	case "/model":
		nativeContext := "unavailable"
		if model.servedContext > 0 {
			nativeContext = formatTokens(model.servedContext) + " tokens"
		}
		activeContext := "unavailable"
		if model.contextLimit > 0 {
			activeContext = formatTokens(model.contextLimit) + " tokens"
		}
		model.blocks = append(model.blocks, block{kind: blockSystem, text: fmt.Sprintf("Model: %s\nActive context: %s\nNative context: %s\nWorkspace: %s", model.modelName, activeContext, nativeContext, model.workspace)})
	case "/tools":
		tools := "none"
		if len(model.toolNames) > 0 {
			tools = strings.Join(model.toolNames, ", ")
		}
		model.blocks = append(model.blocks, block{kind: blockSystem, text: fmt.Sprintf("Tools available in %s capability:\n%s", model.capability, tools)})
	case "/capability":
		boundary := "Read/write actions are limited to the safe tool set."
		if model.capability == "workspace" {
			boundary = "Structured workspace execution is enabled. This is not an OS sandbox."
		} else if model.capability == "operator" {
			boundary = "Explicit shell-capable operator execution is enabled. This is not an OS sandbox."
		}
		model.blocks = append(model.blocks, block{kind: blockSystem, text: fmt.Sprintf("Capability: %s\n%s\nCapability changes require restart with --capability; slash commands cannot elevate access.", model.capability, boundary)})
	case "/help", "/?":
		model.blocks = append(model.blocks, block{kind: blockSystem, text: strings.Join([]string{
			"Commands",
			"/help        command and key reference",
			"/status      runtime and resource snapshot",
			"/model       current model and context",
			"/tools       tools available in this capability",
			"/capability  active capability and safety boundary",
			"/clear       clear the visible transcript",
			"/quit        stop work and exit",
			"",
			"Keys",
			"Enter send · Alt+Enter newline · PgUp/PgDn scroll",
			"Esc stop active run · Ctrl+Q quit",
			"",
			"Commands run locally and are never sent to the model.",
		}, "\n")})
	default:
		model.blocks = append(model.blocks, block{kind: blockError, text: "Unknown command: " + safeText(input) + "\nUse /help to list commands."})
	}
	model.refreshTranscript()
	return model, nil
}

func (model Model) requestQuit() (tea.Model, tea.Cmd) {
	if !model.busy {
		return model, tea.Quit
	}
	model.quitting = true
	model.activity = "cancelling"
	if model.cancel != nil {
		model.cancel()
	}
	return model, nil
}

func knownSlashCommand(name string) bool {
	switch name {
	case "/help", "/?", "/clear", "/status", "/model", "/tools", "/capability", "/quit", "/exit":
		return true
	default:
		return false
	}
}

func sampleResources(sampler ResourceSampler) tea.Cmd {
	return func() tea.Msg {
		return resourceSampleMsg{snapshot: sampler.Sample()}
	}
}

func scheduleResourceTick() tea.Cmd {
	return tea.Tick(2*time.Second, func(time.Time) tea.Msg { return resourceTickMsg{} })
}

func waitForRun(events <-chan runUpdate) tea.Cmd {
	return func() tea.Msg {
		update, ok := <-events
		if !ok {
			return runUpdateMsg{update: runUpdate{done: true}}
		}
		return runUpdateMsg{update: update}
	}
}

func (model *Model) applyAgentEvent(event agent.Event) {
	if event.Telemetry != nil {
		telemetry := event.Telemetry
		if telemetry.Estimated {
			model.contextValid = false
			model.rateValid = false
			model.completionValid = false
			model.telemetryFinal = false
		}
		if telemetry.HasContext {
			model.contextTokens = telemetry.ContextTokens
			model.contextValid = true
		}
		if telemetry.ActiveContextLimit > 0 {
			model.contextLimit = telemetry.ActiveContextLimit
		}
		if telemetry.HasCompletion {
			model.completionTokens = telemetry.CompletionTokens
			model.completionValid = true
		}
		if telemetry.HasTokensPerSecond {
			model.tokensPerSecond = telemetry.TokensPerSecond
			model.rateValid = true
		}
		if telemetry.HasPromptPerSecond {
			model.promptPerSecond = telemetry.PromptPerSecond
		}
		model.contextEstimated = telemetry.Estimated
		model.telemetryFinal = telemetry.Final
		model.step = telemetry.Step
		model.maxSteps = telemetry.MaxSteps
	}
	switch event.Kind {
	case agent.EventReasoningDelta:
		model.activity = "thinking"
		if len(model.blocks) == 0 || model.blocks[len(model.blocks)-1].kind != blockReasoning {
			model.blocks = append(model.blocks, block{kind: blockReasoning})
		}
		model.blocks[len(model.blocks)-1].text += safeText(event.Text)
	case agent.EventAssistantDelta:
		model.activity = "responding"
		if len(model.blocks) == 0 || model.blocks[len(model.blocks)-1].kind != blockAssistant {
			model.blocks = append(model.blocks, block{kind: blockAssistant})
		}
		model.blocks[len(model.blocks)-1].text += safeText(event.Text)
	case agent.EventVerification:
		model.activity = "verification required"
		model.blocks = append(model.blocks, block{kind: blockSystem, text: "Verification required after workspace changes."})
	case agent.EventToolStarted:
		toolName := safeText(event.ToolName)
		callID := safeText(event.CallID)
		arguments := safeText(event.Arguments)
		model.activity = "running " + toolName
		text := "⚙ " + toolName + "  running"
		if target, compact := compactToolTarget(toolName, arguments); compact {
			text = "⚙ " + toolName
			if target != "" {
				text += "  " + target
			}
			text += "  …"
		} else {
			if callID != "" {
				text += "  [" + callID + "]"
			}
			if arguments != "" {
				text += "\nargs\n" + prettyJSON(arguments)
			}
		}
		model.blocks = append(model.blocks, block{kind: blockTool, text: text, callID: callID, toolName: toolName, arguments: arguments})
	case agent.EventToolCompleted:
		model.activity = "thinking"
		toolName := safeText(event.ToolName)
		callID := safeText(event.CallID)
		updated := false
		for index := len(model.blocks) - 1; index >= 0; index-- {
			if model.blocks[index].kind != blockTool || callID == "" || model.blocks[index].callID != callID {
				continue
			}
			if target, compact := compactToolTarget(toolName, model.blocks[index].arguments); compact {
				status := firstResultLine(event.Text)
				model.blocks[index].text = "⚙ " + toolName
				if target != "" {
					model.blocks[index].text += "  " + target
				}
				model.blocks[index].text += "  ✓ " + status
			} else {
				model.blocks[index].text = strings.Replace(model.blocks[index].text, "⚙ "+toolName+"  running", "⚙ "+toolName+"  done", 1)
				if event.Text != "" {
					model.blocks[index].text += "\nresult\n" + toolResultPreview(event.Text)
				}
			}
			updated = true
			break
		}
		if !updated {
			text := "⚙ " + toolName + "  done"
			if callID != "" {
				text += "  [" + callID + "]"
			}
			if event.Text != "" {
				text += "\nresult\n" + toolResultPreview(event.Text)
			}
			model.blocks = append(model.blocks, block{kind: blockTool, text: text})
		}
	case agent.EventToolFailed:
		model.activity = "tool failed"
		model.blocks = append(model.blocks, block{kind: blockError, text: "✕ " + safeText(event.ToolName) + " failed\n" + toolResultPreview(event.Err.Error())})
	}
}

func (model *Model) resize() {
	innerWidth := max(1, model.width-2)
	model.composer.SetWidth(max(1, innerWidth-4))
	model.viewport.SetWidth(innerWidth)
	model.viewport.SetHeight(max(3, model.height-model.chromeHeight()))
}

func (model Model) chromeHeight() int {
	if model.width-2 >= 80 {
		return 7
	}
	return 6
}

func (model *Model) refreshTranscript() {
	model.refreshTranscriptAt(model.viewport.AtBottom(), model.viewport.YOffset(), -1)
}

func (model *Model) refreshTranscriptAfterResize(followLatest bool, scrollPercent float64) {
	model.refreshTranscriptAt(followLatest, 0, scrollPercent)
}

func (model *Model) refreshTranscriptAt(followLatest bool, previousOffset int, scrollPercent float64) {
	var rendered []string
	width := max(8, model.viewport.Width()-4)
	markdown, _ := glamour.NewTermRenderer(
		glamour.WithStyles(glamourstyles.ASCIIStyleConfig),
		glamour.WithWordWrap(width),
		glamour.WithPreservedNewLines(),
	)
	for _, item := range model.blocks {
		switch item.kind {
		case blockSystem:
			rendered = append(rendered, lipgloss.NewStyle().Foreground(muted).Render("◇ NOTE  "+item.text))
		case blockUser:
			label := lipgloss.NewStyle().Foreground(accentBright).Bold(true).Render("YOU › ")
			rendered = append(rendered, label+item.text)
		case blockAssistant:
			text := renderAssistantMarkdown(item.text, width, markdown)
			label := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("◆ HORIZON")
			rendered = append(rendered, label+"\n"+text)
		case blockReasoning:
			label := lipgloss.NewStyle().Foreground(accentBright).Bold(true).Render("┊ THINK")
			text := lipgloss.NewStyle().Foreground(muted).Render(item.text)
			rendered = append(rendered, label+"\n"+text)
		case blockTool:
			toolText := ansi.Wordwrap(item.text, max(1, width-4), " ")
			rendered = append(rendered, lipgloss.NewStyle().Foreground(cyan).BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(accent).Padding(0, 1).Render(toolText))
		case blockError:
			rendered = append(rendered, lipgloss.NewStyle().Foreground(danger).Render(item.text))
		}
	}
	model.viewport.SetContent(strings.Join(rendered, "\n\n"))
	if followLatest {
		model.viewport.GotoBottom()
	} else if scrollPercent >= 0 {
		model.viewport.GotoBottom()
		maximumOffset := model.viewport.YOffset()
		targetOffset := int(scrollPercent*float64(maximumOffset) + 0.5)
		if targetOffset >= maximumOffset && maximumOffset > 0 {
			targetOffset = maximumOffset - 1
		}
		model.viewport.SetYOffset(targetOffset)
	} else {
		model.viewport.SetYOffset(previousOffset)
	}
}

func renderAssistantMarkdown(value string, width int, markdown *glamour.TermRenderer) string {
	lines := strings.Split(safeText(value), "\n")
	var rendered []string
	var prose []string
	flushProse := func() {
		if len(prose) == 0 {
			return
		}
		text := strings.Join(prose, "\n")
		if markdown != nil {
			if result, err := markdown.Render(text); err == nil {
				text = strings.TrimSpace(result)
			}
		}
		if strings.TrimSpace(text) != "" {
			rendered = append(rendered, text)
		}
		prose = nil
	}

	for index := 0; index < len(lines); index++ {
		marker, markerLength, language, fenced := markdownFence(lines[index])
		if !fenced {
			prose = append(prose, lines[index])
			continue
		}
		closing := -1
		for candidate := index + 1; candidate < len(lines); candidate++ {
			if closesMarkdownFence(lines[candidate], marker, markerLength) {
				closing = candidate
				break
			}
		}
		if closing < 0 {
			prose = append(prose, lines[index])
			continue
		}
		flushProse()
		rendered = append(rendered, renderCodeBlock(language, lines[index+1:closing], width))
		index = closing
	}
	flushProse()
	return strings.Join(rendered, "\n\n")
}

func markdownFence(line string) (marker rune, length int, info string, ok bool) {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 || trimmed == "" {
		return 0, 0, "", false
	}
	marker = rune(trimmed[0])
	if marker != '`' && marker != '~' {
		return 0, 0, "", false
	}
	for length < len(trimmed) && rune(trimmed[length]) == marker {
		length++
	}
	if length < 3 {
		return 0, 0, "", false
	}
	info = strings.TrimSpace(trimmed[length:])
	if marker == '`' && strings.ContainsRune(info, '`') {
		return 0, 0, "", false
	}
	return marker, length, info, true
}

func closesMarkdownFence(line string, marker rune, minimumLength int) bool {
	trimmed := strings.TrimLeft(line, " ")
	if len(line)-len(trimmed) > 3 {
		return false
	}
	length := 0
	for length < len(trimmed) && rune(trimmed[length]) == marker {
		length++
	}
	return length >= minimumLength && strings.TrimSpace(trimmed[length:]) == ""
}

func renderCodeBlock(language string, lines []string, width int) string {
	contentWidth := max(1, width-2)
	railStyle := lipgloss.NewStyle().Foreground(accent)
	metadataStyle := lipgloss.NewStyle().Foreground(muted)
	language = ansi.Truncate(compactOneLine(language), max(1, width-4), "…")
	header := railStyle.Render("┌─")
	if language != "" {
		header += metadataStyle.Render(" " + language)
	}
	rendered := []string{header}
	if len(lines) == 0 {
		lines = []string{""}
	}
	for _, line := range lines {
		wrapped := strings.Split(ansi.Hardwrap(safeText(line), contentWidth, true), "\n")
		for _, segment := range wrapped {
			rendered = append(rendered, railStyle.Render("│ ")+segment)
		}
	}
	rendered = append(rendered, railStyle.Render("└─"))
	return strings.Join(rendered, "\n")
}

func compactToolTarget(toolName, arguments string) (string, bool) {
	pathTarget := func(defaultPath string) (string, bool) {
		var decoded struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
			return "", false
		}
		if decoded.Path == "" {
			decoded.Path = defaultPath
		}
		if decoded.Path == "" {
			return "", false
		}
		return compactOneLine(decoded.Path), true
	}
	commandTarget := func() (string, bool) {
		var decoded struct {
			Program string   `json:"program"`
			Args    []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil || decoded.Program == "" {
			return "", false
		}
		return compactOneLine(strings.Join(append([]string{decoded.Program}, decoded.Args...), " ")), true
	}

	switch toolName {
	case "read_file", "create_file", "edit_file", "make_directory", "delete_path":
		return pathTarget("")
	case "list_files":
		return pathTarget(".")
	case "search_code":
		var decoded struct {
			Query string `json:"query"`
			Path  string `json:"path"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil || decoded.Query == "" {
			return "", false
		}
		if decoded.Path == "" {
			decoded.Path = "."
		}
		return compactOneLine(decoded.Query + " · " + decoded.Path), true
	case "apply_patch":
		var decoded struct {
			Patches []struct {
				Path string `json:"path"`
			} `json:"patches"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil || len(decoded.Patches) == 0 || decoded.Patches[0].Path == "" {
			return "", false
		}
		target := decoded.Patches[0].Path
		if len(decoded.Patches) > 1 {
			target += fmt.Sprintf(" +%d", len(decoded.Patches)-1)
		}
		return compactOneLine(target), true
	case "move_path":
		var decoded struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil || decoded.Source == "" || decoded.Destination == "" {
			return "", false
		}
		return compactOneLine(decoded.Source + " → " + decoded.Destination), true
	case "run_command", "exec_command":
		return commandTarget()
	case "shell":
		var decoded struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil || decoded.Command == "" {
			return "", false
		}
		return compactOneLine(decoded.Command), true
	case "background_process":
		var decoded struct {
			Operation string   `json:"operation"`
			ID        string   `json:"id"`
			Program   string   `json:"program"`
			Args      []string `json:"args"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil || decoded.Operation == "" {
			return "", false
		}
		target := decoded.ID
		if decoded.Operation == "start" && decoded.Program != "" {
			target = strings.Join(append([]string{decoded.Program}, decoded.Args...), " ")
		}
		if target == "" {
			return compactOneLine(decoded.Operation), true
		}
		return compactOneLine(decoded.Operation + " · " + target), true
	case "git_diff":
		var decoded struct {
			Path   string `json:"path"`
			Staged bool   `json:"staged"`
		}
		if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
			return "", false
		}
		mode := "working"
		if decoded.Staged {
			mode = "staged"
		}
		if decoded.Path == "" {
			return mode, true
		}
		return compactOneLine(mode + " · " + decoded.Path), true
	default:
		return "", false
	}
}

func compactOneLine(value string) string {
	const maxRunes = 120
	value = strings.Join(strings.Fields(safeText(value)), " ")
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes-1]) + "…"
	}
	return value
}

func firstResultLine(result string) string {
	for _, line := range strings.Split(safeText(result), "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return "done"
}

func prettyJSON(value string) string {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return safeText(value)
	}
	formatted, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return safeText(value)
	}
	return string(formatted)
}

func safeText(value string) string {
	value = ansi.Strip(value)
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '	' {
			return character
		}
		if character < 0x20 || character == 0x7f || (character >= 0x80 && character <= 0x9f) {
			return -1
		}
		return character
	}, value)
}

func toolResultPreview(value string) string {
	const maxLines = 24
	const maxRunesPerLine = 500

	lines := strings.Split(strings.TrimSpace(safeText(value)), "\n")
	clipped := len(lines) > maxLines
	if clipped {
		lines = lines[:maxLines]
	}
	for index, line := range lines {
		runes := []rune(line)
		if len(runes) > maxRunesPerLine {
			lines[index] = string(runes[:maxRunesPerLine]) + "…"
			clipped = true
		}
	}
	preview := strings.Join(lines, "\n")
	if clipped {
		preview += "\n… inline preview clipped; full result is in the session log."
	}
	return preview
}

func (model Model) renderHeader(width int) string {
	brandText := "◆ HORIZON CODE"
	if width < 28 {
		brandText = "◆ HC"
	}
	brand := lipgloss.NewStyle().Foreground(accentBright).Bold(true).Render(brandText)
	capabilityText := strings.ToUpper(model.capability)
	if width < 40 {
		switch model.capability {
		case "operator":
			capabilityText = "OP"
		case "workspace":
			capabilityText = "WS"
		default:
			capabilityText = "S"
		}
	}
	capabilityColor := muted
	if model.capability == "workspace" {
		capabilityColor = accentBright
	} else if model.capability == "operator" {
		capabilityColor = danger
	}
	capability := lipgloss.NewStyle().Foreground(capabilityColor).Bold(true).Render(capabilityText)
	stateColor := cyan
	if model.busy {
		stateColor = accentBright
	}
	if model.activity == "failed" || model.activity == "tool failed" {
		stateColor = danger
	}
	activity := lipgloss.NewStyle().Foreground(stateColor).Render("● " + model.activity)

	left := brand + " " + capability + "  " + activity
	right := ""
	if width >= 80 {
		left = brand + " " + capability + lipgloss.NewStyle().Foreground(muted).Render("  "+model.workspace) + "  " + activity
		right = lipgloss.NewStyle().Foreground(muted).Render(model.modelName + "  " + model.renderTelemetry())
	} else if width >= 56 {
		right = lipgloss.NewStyle().Foreground(muted).Render(model.modelName)
	}
	gap := strings.Repeat(" ", max(1, width-lipgloss.Width(left)-lipgloss.Width(right)))
	return lipgloss.NewStyle().Width(width).Render(left + gap + right)
}

func (model Model) renderStatus() string {
	lines := []string{
		"Runtime",
		"Model: " + model.modelName,
		"Workspace: " + model.workspace,
		"Activity: " + model.activity,
		"Capability: " + model.capability,
		"Active context limit: " + formatOptionalTokens(model.contextLimit),
		"Model telemetry: " + model.renderTelemetry(),
	}
	if model.resources.GPU.Valid {
		lines = append(lines, "GPU allocations (system-wide): "+safeText(model.resources.GPU.Card))
	} else {
		lines = append(lines, "GPU allocations (system-wide): unavailable")
	}
	lines = append(lines,
		"VRAM: "+formatGPUHeap(model.resources.GPU.VRAM),
		"GTT: "+formatGTT(model.resources.GPU.GTT),
	)
	if model.resources.RAM.Valid {
		ram := model.resources.RAM
		lines = append(lines, "RAM: "+formatGiB(ram.UsedBytes)+"/"+formatGiB(ram.TotalBytes)+" GiB · available "+formatGiB(ram.AvailableBytes)+" GiB")
	} else {
		lines = append(lines, "RAM: unavailable")
	}
	return strings.Join(lines, "\n")
}

func formatGTT(heap resources.GPUHeap) string {
	if !heap.Valid {
		return "unavailable"
	}
	return formatGiB(heap.UsedBytes) + " GiB allocated (" + formatGiB(heap.TotalBytes) + " GiB limit)"
}

func formatGPUHeap(heap resources.GPUHeap) string {
	if !heap.Valid {
		return "unavailable"
	}
	return formatGiB(heap.UsedBytes) + "/" + formatGiB(heap.TotalBytes) + " GiB"
}

func (model Model) renderResources(width int) string {
	vram := formatGPUHeap(model.resources.GPU.VRAM)
	gtt := formatGTT(model.resources.GPU.GTT)
	ram := model.resources.RAM
	if width >= 80 {
		gpuLine := "GPU system · VRAM " + vram + " · GTT " + gtt
		ramLine := "RAM —"
		if ram.Valid {
			ramLine = "RAM " + formatGiB(ram.UsedBytes) + "/" + formatGiB(ram.TotalBytes) + " GiB · available " + formatGiB(ram.AvailableBytes) + " GiB"
		}
		return renderResourceLine(gpuLine, width) + "\n" + renderResourceLine(ramLine, width)
	}
	text := "V —  G —  R —"
	if width >= 52 {
		vramText := "VRAM —"
		if model.resources.GPU.VRAM.Valid {
			vramText = "VRAM " + formatGiB(model.resources.GPU.VRAM.UsedBytes) + "/" + formatGiB(model.resources.GPU.VRAM.TotalBytes) + "G"
		}
		gttText := "GTT —"
		if model.resources.GPU.GTT.Valid {
			gttText = "GTT " + formatGiB(model.resources.GPU.GTT.UsedBytes) + "/" + formatGiB(model.resources.GPU.GTT.TotalBytes) + "G limit"
		}
		ramText := "RAM —"
		if ram.Valid {
			ramText = "RAM " + formatGiB(ram.UsedBytes) + "/" + formatGiB(ram.TotalBytes) + "G"
		}
		text = vramText + "  " + gttText + "  " + ramText
	} else if width >= 28 {
		vramText := "V —"
		if model.resources.GPU.VRAM.Valid {
			vramText = "V " + formatGiB(model.resources.GPU.VRAM.UsedBytes) + "/" + formatGiB(model.resources.GPU.VRAM.TotalBytes)
		}
		gttText := "G —"
		if model.resources.GPU.GTT.Valid {
			gttText = "G " + formatGiB(model.resources.GPU.GTT.UsedBytes) + "/" + formatGiB(model.resources.GPU.GTT.TotalBytes) + "L"
		}
		ramText := "R —"
		if ram.Valid {
			ramText = "R " + formatGiB(ram.UsedBytes) + "/" + formatGiB(ram.TotalBytes)
		}
		text = vramText + " " + gttText + " " + ramText
	} else {
		if model.resources.GPU.VRAM.Valid {
			text = "V" + formatGiB(model.resources.GPU.VRAM.UsedBytes)
		}
		if model.resources.GPU.GTT.Valid {
			text += " G" + formatGiB(model.resources.GPU.GTT.UsedBytes)
		}
		if ram.Valid {
			text += " R" + formatGiB(ram.UsedBytes)
		}
	}
	return renderResourceLine(text, width)
}

func renderResourceLine(text string, width int) string {
	text = ansi.Truncate(text, width, "…")
	return lipgloss.NewStyle().Width(width).Foreground(muted).Render(text)
}

func formatGiB(value uint64) string {
	gib := float64(value) / float64(uint64(1)<<30)
	if gib >= 10 && gib == float64(uint64(gib)) {
		return fmt.Sprintf("%.0f", gib)
	}
	return strings.TrimSuffix(fmt.Sprintf("%.1f", gib), ".0")
}

func (model Model) renderTelemetry() string {
	context := "ctx —"
	if model.contextValid {
		prefix := ""
		if model.contextEstimated {
			prefix = "~"
		}
		context = "ctx " + prefix + formatTokens(model.contextTokens)
		if model.contextLimit > 0 {
			context += "/" + formatTokens(model.contextLimit)
		}
	}
	parts := []string{context}
	if model.servedContext > 0 {
		parts = append(parts, "native "+formatTokens(model.servedContext))
	}
	if model.rateValid {
		parts = append(parts, fmt.Sprintf("%.1f tok/s avg", model.tokensPerSecond))
	} else {
		parts = append(parts, "tok/s —")
	}
	if model.completionValid {
		parts = append(parts, "out "+formatTokens(model.completionTokens))
	}
	if model.step > 0 && model.maxSteps > 0 {
		parts = append(parts, fmt.Sprintf("step %d/%d", model.step, model.maxSteps))
	}
	return strings.Join(parts, "  ")
}

func formatOptionalTokens(tokens int) string {
	if tokens <= 0 {
		return "unavailable"
	}
	return formatTokens(tokens) + " tokens"
}

func formatTokens(tokens int) string {
	if tokens < 1000 {
		return fmt.Sprintf("%d", tokens)
	}
	if tokens >= 100000 {
		return fmt.Sprintf("%.0fk", float64(tokens)/1000)
	}
	return fmt.Sprintf("%.1fk", float64(tokens)/1000)
}

func (model Model) renderControls(width int) string {
	controls := "↵ send  /help  ^q quit"
	if width >= 40 {
		controls = "enter send  ·  /help  ·  esc stop  ·  ^q quit"
	}
	if width >= 60 {
		controls = "enter send · alt+enter newline · /help · esc stop · ctrl+q quit"
	}
	if width >= 80 {
		controls = "enter send · alt+enter newline · pgup/dn scroll · /help · esc stop · ^q quit"
	}
	return lipgloss.NewStyle().Width(width).Foreground(muted).Render(controls)
}
