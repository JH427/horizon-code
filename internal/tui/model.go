package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamourstyles "charm.land/glamour/v2/styles"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/JH427/horizon-code/internal/agent"
)

type Runner interface {
	Run(context.Context, string, func(agent.Event)) (string, error)
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
	runner     Runner
	modelName  string
	workspace  string
	capability string
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
	activity         string
	cancel           context.CancelFunc
	runEvents        <-chan runUpdate
}

type agentEventMsg struct{ event agent.Event }
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
	composer.Placeholder = "Ask Horizon Code to inspect the workspace…"
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
		runner: runner, modelName: modelName, workspace: workspace,
		viewport: view, composer: composer,
		activity: "ready", capability: "safe",
	}
}

func (model *Model) SetServedContext(tokens int) {
	model.servedContext = tokens
}

func (model *Model) SetCapability(capability string) {
	model.capability = safeText(capability)
}

func (model Model) Init() tea.Cmd {
	return textarea.Blink
}

func (model Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		model.width = message.Width
		model.height = message.Height
		model.resize()
		model.refreshTranscript()
		return model, nil
	case tea.KeyPressMsg:
		switch message.String() {
		case "ctrl+q":
			if model.cancel != nil {
				model.cancel()
			}
			return model, tea.Quit
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
			if !model.busy && strings.TrimSpace(model.composer.Value()) != "" {
				return model.submit()
			}
		}
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
	header := model.renderHeader(innerWidth)
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
	view.WindowTitle = "Horizon Code"
	return view
}

func (model Model) submit() (tea.Model, tea.Cmd) {
	prompt := strings.TrimSpace(model.composer.Value())
	model.composer.Reset()
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
		if toolName == "read_file" {
			if target := toolPath(arguments); target != "" {
				text = "⚙ " + toolName + "  " + target + "  …"
			}
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
			if toolName == "read_file" {
				target := toolPath(model.blocks[index].arguments)
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
	model.viewport.SetHeight(max(3, model.height-5))
}

func (model *Model) refreshTranscript() {
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
			text := item.text
			if markdown != nil {
				if result, err := markdown.Render(item.text); err == nil {
					text = strings.TrimSpace(result)
				}
			}
			label := lipgloss.NewStyle().Foreground(cyan).Bold(true).Render("◆ HORIZON")
			rendered = append(rendered, label+"\n"+text)
		case blockReasoning:
			label := lipgloss.NewStyle().Foreground(accentBright).Bold(true).Render("┊ THINK")
			text := lipgloss.NewStyle().Foreground(muted).Render(item.text)
			rendered = append(rendered, label+"\n"+text)
		case blockTool:
			rendered = append(rendered, lipgloss.NewStyle().Foreground(cyan).BorderLeft(true).BorderStyle(lipgloss.ThickBorder()).BorderForeground(accent).Padding(0, 1).Render(item.text))
		case blockError:
			rendered = append(rendered, lipgloss.NewStyle().Foreground(danger).Render(item.text))
		}
	}
	model.viewport.SetContent(strings.Join(rendered, "\n\n"))
	model.viewport.GotoBottom()
}

func toolPath(arguments string) string {
	var decoded struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(arguments), &decoded); err != nil {
		return ""
	}
	return safeText(decoded.Path)
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
	return ansi.Strip(value)
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
	controls := "↵ send  ^q quit"
	if width >= 40 {
		controls = "enter send  ·  esc stop  ·  ^q quit"
	}
	if width >= 60 {
		controls = "enter send  ·  alt+enter newline  ·  esc stop  ·  ctrl+q quit"
	}
	return lipgloss.NewStyle().Width(width).Foreground(muted).Render(controls)
}
