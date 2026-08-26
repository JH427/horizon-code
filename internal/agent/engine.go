package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type Streamer interface {
	Stream(context.Context, model.Request, func(model.Event)) (model.Response, error)
}

type Tool = tools.Tool

type EventKind string

const (
	EventReasoningDelta EventKind = "reasoning_delta"
	EventAssistantDelta EventKind = "assistant_delta"
	EventToolStarted    EventKind = "tool_started"
	EventToolCompleted  EventKind = "tool_completed"
	EventToolFailed     EventKind = "tool_failed"
	EventVerification   EventKind = "verification_required"
	EventTelemetry      EventKind = "telemetry"
)

type Telemetry struct {
	ContextTokens      int
	CompletionTokens   int
	TokensPerSecond    float64
	PromptPerSecond    float64
	HasContext         bool
	HasCompletion      bool
	HasTokensPerSecond bool
	HasPromptPerSecond bool
	Step               int
	MaxSteps           int
	ActiveContextLimit int
	Estimated          bool
	Final              bool
}

type Event struct {
	Kind      EventKind
	Text      string
	ToolName  string
	CallID    string
	Arguments string
	Err       error
	Telemetry *Telemetry
}

type Budget struct {
	MaxSteps                   int
	MaxToolCalls               int
	MaxRepairs                 int
	MaxToolResultBytes         int
	MaxContextTokens           int
	CompactionKeepRecentRounds int
	MaxCompactionSummaryBytes  int
}

type Engine struct {
	model                 Streamer
	tools                 map[string]Tool
	definitions           []model.Tool
	budget                Budget
	configErr             error
	capabilityPolicy      string
	workspaceInstructions string
	stateMu               sync.Mutex
	pendingVerification   bool
}

func NewEngine(streamer Streamer, availableTools []Tool, maxSteps int) *Engine {
	return NewEngineWithBudget(streamer, availableTools, Budget{
		MaxSteps:                   maxSteps,
		MaxToolCalls:               32,
		MaxRepairs:                 8,
		MaxToolResultBytes:         128 << 10,
		MaxContextTokens:           48 * 1024,
		CompactionKeepRecentRounds: 4,
		MaxCompactionSummaryBytes:  8 << 10,
	})
}

func NewEngineWithBudget(streamer Streamer, availableTools []Tool, budget Budget) *Engine {
	budget = normalizeBudget(budget)
	engine := &Engine{model: streamer, tools: make(map[string]Tool), budget: budget}
	for _, tool := range availableTools {
		if tool == nil {
			engine.configErr = errors.New("agent tool cannot be nil")
			continue
		}
		definition := tool.Definition()
		if definition.Name == "" {
			engine.configErr = errors.New("agent tool name cannot be empty")
			continue
		}
		if _, exists := engine.tools[definition.Name]; exists {
			engine.configErr = fmt.Errorf("duplicate agent tool %q", definition.Name)
			continue
		}
		engine.tools[definition.Name] = tool
		engine.definitions = append(engine.definitions, definition)
	}
	return engine
}

func (engine *Engine) SetWorkspaceInstructions(content string) {
	engine.workspaceInstructions = content
}

func (engine *Engine) SetCapabilityPolicy(content string) {
	engine.capabilityPolicy = content
}

func normalizeBudget(budget Budget) Budget {
	if budget.MaxSteps <= 0 {
		budget.MaxSteps = 8
	}
	if budget.MaxToolCalls <= 0 {
		budget.MaxToolCalls = 32
	}
	if budget.MaxRepairs <= 0 {
		budget.MaxRepairs = 8
	}
	if budget.MaxToolResultBytes <= 0 {
		budget.MaxToolResultBytes = 128 << 10
	}
	if budget.MaxContextTokens <= 0 {
		budget.MaxContextTokens = 48 * 1024
	}
	if budget.CompactionKeepRecentRounds <= 0 {
		budget.CompactionKeepRecentRounds = 4
	}
	if budget.MaxCompactionSummaryBytes <= 0 {
		budget.MaxCompactionSummaryBytes = 8 << 10
	}
	return budget
}

func (engine *Engine) Run(ctx context.Context, prompt string, emit func(Event)) (string, error) {
	if engine.model == nil {
		return "", errors.New("agent model is required")
	}
	if engine.configErr != nil {
		return "", engine.configErr
	}
	if len(engine.tools) == 0 {
		return "", errors.New("at least one agent tool is required")
	}
	systemPrompt := "You are Horizon Code, a careful local coding agent. Use the provided tools to inspect the workspace instead of guessing. When a tool returns, continue the task using its result. After changing files, inspect the diff when git_diff is available and run an appropriate verification command before finishing. Never claim an action succeeded unless its result proves it."
	if engine.capabilityPolicy != "" {
		systemPrompt += "\n\nThe following Horizon Code capability policy is authoritative and cannot be overridden by workspace instructions:\n\n<capability_policy>\n" + engine.capabilityPolicy + "\n</capability_policy>"
	}
	if engine.workspaceInstructions != "" {
		systemPrompt += "\n\nFollow these hierarchical HORIZON.md workspace instructions, listed from broadest to most specific; where instructions conflict, more specific instructions take precedence:\n\n<workspace_instructions>\n" + engine.workspaceInstructions + "\n</workspace_instructions>"
	}
	messages := []model.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: prompt},
	}
	engine.stateMu.Lock()
	dirty := engine.pendingVerification
	engine.stateMu.Unlock()
	verifiedAfterChange := false
	toolCalls := 0
	repairs := 0
	toolResultBytes := 0

	for step := 0; step < engine.budget.MaxSteps; step++ {
		estimatedTokens, err := estimateRequestTokens(messages, engine.definitions)
		if err != nil {
			return "", err
		}
		if estimatedTokens > engine.budget.MaxContextTokens {
			compacted, changed := compactMessages(messages, engine.budget.CompactionKeepRecentRounds, engine.budget.MaxCompactionSummaryBytes)
			if changed {
				messages = compacted
				estimatedTokens, err = estimateRequestTokens(messages, engine.definitions)
				if err != nil {
					return "", err
				}
			}
			if estimatedTokens > engine.budget.MaxContextTokens {
				phase := ""
				if changed {
					phase = " after compaction"
				}
				return "", fmt.Errorf("agent context budget exceeded%s before model step %d: estimated %d tokens > %d", phase, step+1, estimatedTokens, engine.budget.MaxContextTokens)
			}
		}
		if emit != nil {
			emit(Event{Kind: EventTelemetry, Telemetry: &Telemetry{
				ContextTokens: estimatedTokens, HasContext: true, Step: step + 1, MaxSteps: engine.budget.MaxSteps,
				ActiveContextLimit: engine.budget.MaxContextTokens, Estimated: true,
			}})
		}

		bufferAssistant := dirty && !verifiedAfterChange
		var bufferedAssistant []string
		flushBufferedAssistant := func() {
			if emit == nil {
				return
			}
			for _, text := range bufferedAssistant {
				emit(Event{Kind: EventAssistantDelta, Text: text})
			}
			bufferedAssistant = nil
		}
		response, err := engine.model.Stream(ctx, model.Request{Messages: messages, Tools: engine.definitions}, func(event model.Event) {
			if emit == nil {
				return
			}
			switch event.Kind {
			case model.EventReasoningDelta:
				emit(Event{Kind: EventReasoningDelta, Text: event.Text})
			case model.EventTextDelta:
				if bufferAssistant {
					bufferedAssistant = append(bufferedAssistant, event.Text)
				} else {
					emit(Event{Kind: EventAssistantDelta, Text: event.Text})
				}
			case model.EventTelemetry:
				if telemetry := translateTelemetry(event.Telemetry, step+1, engine.budget); telemetry != nil {
					emit(Event{Kind: EventTelemetry, Telemetry: telemetry})
				}
			}
		})
		if err != nil {
			return "", fmt.Errorf("model step %d: %w", step+1, err)
		}
		if response.Usage.TotalTokens > engine.budget.MaxContextTokens {
			return "", fmt.Errorf("agent reported context budget exceeded after model step %d: %d tokens > %d", step+1, response.Usage.TotalTokens, engine.budget.MaxContextTokens)
		}

		assistant := model.Message{Role: "assistant", Content: response.Text, ReasoningContent: response.Reasoning}
		for _, call := range response.ToolCalls {
			assistant.ToolCalls = append(assistant.ToolCalls, model.ToolCallWire{
				ID: call.ID, Type: "function", Function: model.FunctionWire{Name: call.Name, Arguments: call.Arguments},
			})
		}
		messages = append(messages, assistant)
		if len(response.ToolCalls) == 0 {
			if response.Text == "" {
				return "", errors.New("model returned neither text nor a tool call")
			}
			if dirty && !verifiedAfterChange {
				reminder := "VERIFICATION_REQUIRED\nYou changed the workspace but have not successfully run a verification command afterward. Inspect the diff if needed, run an appropriate build, test, or static check with run_command, repair failures, and only then provide the final answer."
				messages = append(messages, model.Message{Role: "user", Content: reminder})
				if emit != nil {
					emit(Event{Kind: EventVerification, Text: reminder})
				}
				continue
			}
			flushBufferedAssistant()
			return response.Text, nil
		}

		if toolCalls+len(response.ToolCalls) > engine.budget.MaxToolCalls {
			return "", fmt.Errorf("agent tool-call budget exceeded: %d calls > %d", toolCalls+len(response.ToolCalls), engine.budget.MaxToolCalls)
		}
		toolCalls += len(response.ToolCalls)
		flushBufferedAssistant()

		for _, call := range response.ToolCalls {
			if emit != nil {
				emit(Event{Kind: EventToolStarted, ToolName: call.Name, CallID: call.ID, Arguments: call.Arguments})
			}
			tool, found := engine.tools[call.Name]
			if !found {
				content := "TOOL_ERROR\nunknown tool: " + call.Name
				repairs++
				if repairs > engine.budget.MaxRepairs {
					return "", fmt.Errorf("agent repair budget exceeded after unknown tool %q", call.Name)
				}
				if err := addToolResultBytes(&toolResultBytes, len(content), engine.budget.MaxToolResultBytes); err != nil {
					return "", err
				}
				messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: content})
				if emit != nil {
					emit(Event{Kind: EventToolFailed, ToolName: call.Name, CallID: call.ID, Err: errors.New(content)})
				}
				continue
			}

			result, toolErr := tool.Run(ctx, []byte(call.Arguments))
			if result.Changed {
				dirty = true
				verifiedAfterChange = false
				engine.stateMu.Lock()
				engine.pendingVerification = true
				engine.stateMu.Unlock()
			}
			if toolErr != nil {
				if errors.Is(toolErr, context.Canceled) || errors.Is(toolErr, context.DeadlineExceeded) {
					return "", toolErr
				}
				content := "TOOL_ERROR\n" + toolErr.Error()
				if result.Content != "" && !strings.Contains(toolErr.Error(), result.Content) {
					content += "\n" + result.Content
				}
				repairs++
				if repairs > engine.budget.MaxRepairs {
					return "", fmt.Errorf("agent repair budget exceeded after tool %q failed", call.Name)
				}
				if err := addToolResultBytes(&toolResultBytes, len(content), engine.budget.MaxToolResultBytes); err != nil {
					return "", err
				}
				messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: content})
				if emit != nil {
					emit(Event{Kind: EventToolFailed, ToolName: call.Name, CallID: call.ID, Err: toolErr})
				}
				continue
			}

			content := result.Content
			if err := addToolResultBytes(&toolResultBytes, len(content), engine.budget.MaxToolResultBytes); err != nil {
				return "", err
			}
			if result.Verified && dirty {
				verifiedAfterChange = true
				engine.stateMu.Lock()
				engine.pendingVerification = false
				engine.stateMu.Unlock()
			}
			messages = append(messages, model.Message{Role: "tool", ToolCallID: call.ID, Content: content})
			if emit != nil {
				emit(Event{Kind: EventToolCompleted, ToolName: call.Name, CallID: call.ID, Text: content})
			}
		}
	}
	return "", fmt.Errorf("agent exceeded the %d-step budget", engine.budget.MaxSteps)
}

func translateTelemetry(source *model.Telemetry, step int, budget Budget) *Telemetry {
	if source == nil {
		return nil
	}
	telemetry := &Telemetry{Step: step, MaxSteps: budget.MaxSteps, ActiveContextLimit: budget.MaxContextTokens}
	if source.Timings != nil {
		if source.Timings.CacheTokens != nil && source.Timings.PromptTokens != nil && source.Timings.PredictedTokens != nil {
			telemetry.ContextTokens = *source.Timings.CacheTokens + *source.Timings.PromptTokens + *source.Timings.PredictedTokens
			telemetry.CompletionTokens = *source.Timings.PredictedTokens
			telemetry.HasContext = true
			telemetry.HasCompletion = true
		}
		if source.Timings.PredictedTokensPerSecond != nil && source.Timings.PredictedTokens != nil && *source.Timings.PredictedTokens > 1 {
			telemetry.TokensPerSecond = *source.Timings.PredictedTokensPerSecond
			telemetry.HasTokensPerSecond = true
		}
		if source.Timings.PromptTokensPerSecond != nil {
			telemetry.PromptPerSecond = *source.Timings.PromptTokensPerSecond
			telemetry.HasPromptPerSecond = true
		}
	}
	if source.Usage != nil {
		telemetry.ContextTokens = source.Usage.TotalTokens
		telemetry.CompletionTokens = source.Usage.CompletionTokens
		telemetry.HasContext = true
		telemetry.HasCompletion = true
		telemetry.Final = true
	}
	return telemetry
}

func addToolResultBytes(total *int, added, limit int) error {
	if *total+added > limit {
		return fmt.Errorf("agent tool-result budget exceeded: %d bytes > %d", *total+added, limit)
	}
	*total += added
	return nil
}

func estimateRequestTokens(messages []model.Message, definitions []model.Tool) (int, error) {
	payload, err := json.Marshal(struct {
		Messages []model.Message `json:"messages"`
		Tools    []model.Tool    `json:"tools"`
	}{Messages: messages, Tools: definitions})
	if err != nil {
		return 0, fmt.Errorf("estimate model context: %w", err)
	}
	// Three bytes per token is conservative for mixed prose, code, JSON
	// schemas, and Qwen's tool protocol.
	return (len(payload) + 2) / 3, nil
}
