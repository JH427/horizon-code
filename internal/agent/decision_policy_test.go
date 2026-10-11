package agent

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/JH427/horizon-code/internal/decision"
	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type policyModel struct {
	requests []model.Request
}

func (fake *policyModel) Stream(_ context.Context, request model.Request, _ func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	return model.Response{Text: "done"}, nil
}

type policyTool struct{}

func (policyTool) Definition() model.Tool {
	return model.Tool{Name: "read_file", Description: "read", Parameters: map[string]any{"type": "object"}}
}
func (policyTool) Run(context.Context, []byte) (tools.Result, error) { return tools.Result{}, nil }

type recordingDecider struct {
	states []decision.State
	result decision.Result
	err    error
}

func (fake *recordingDecider) Decide(_ context.Context, state decision.State) (decision.Result, error) {
	fake.states = append(fake.states, state)
	return fake.result, fake.err
}

func TestDecisionPolicyOffPerformsNoCall(t *testing.T) {
	streamer := &policyModel{}
	decider := &recordingDecider{}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionOff, Decider: decider, CapabilityMode: "safe"}); err != nil {
		t.Fatal(err)
	}
	answer, err := engine.Run(context.Background(), "inspect", nil)
	if err != nil || answer != "done" {
		t.Fatalf("Run() = %q, %v", answer, err)
	}
	if len(decider.states) != 0 {
		t.Fatalf("decision calls = %d, want 0", len(decider.states))
	}
}

func TestDecisionPolicyShadowRecordsButDoesNotApply(t *testing.T) {
	streamer := &policyModel{}
	decider := &recordingDecider{result: decision.Result{
		Choice: "B", Probabilities: map[string]float64{"A": 0.1, "B": 0.8, "C": 0.1},
		Model: "laya-head", Checkpoint: "english", EndpointLatency: 5 * time.Millisecond,
		ObservedLatency: 8 * time.Millisecond, InputTokens: 32, StateTokens: 20,
	}}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider, CapabilityMode: "workspace"}); err != nil {
		t.Fatal(err)
	}
	var events []Event
	answer, err := engine.Run(context.Background(), "show repository status", func(event Event) { events = append(events, event) })
	if err != nil || answer != "done" {
		t.Fatalf("Run() = %q, %v", answer, err)
	}
	if len(decider.states) != 1 {
		t.Fatalf("decision calls = %d, want 1", len(decider.states))
	}
	state := decider.states[0]
	if state.UserRequest != "show repository status" || state.CapabilityMode != "workspace" || !state.FirstModelStep || state.SchemaVersion != decision.SchemaVersion {
		t.Fatalf("decision state = %#v", state)
	}
	if len(streamer.requests) != 1 || len(streamer.requests[0].Tools) != 1 || streamer.requests[0].Tools[0].Name != "read_file" {
		t.Fatalf("model request tools = %#v", streamer.requests)
	}
	options := streamer.requests[0].InferenceOptions
	if options.EnableThinking != nil || options.ReasoningEffort != nil || options.ReasoningBudgetTokens != nil || options.MaxTokens != nil {
		t.Fatalf("shadow mode changed inference options: %#v", options)
	}
	if len(events) == 0 || events[0].Kind != EventDecision || events[0].Decision == nil {
		t.Fatalf("first event = %#v, want decision", events)
	}
	evidence := events[0].Decision
	if evidence.Applied || evidence.Choice != "B" || evidence.Mode != DecisionShadow || evidence.EndpointLatencyNS != int64(5*time.Millisecond) {
		t.Fatalf("decision evidence = %#v", evidence)
	}
	decider.result.Probabilities["B"] = 0
	if evidence.Probabilities["B"] != 0.8 {
		t.Fatal("recorded probabilities alias mutable decider result")
	}
}

type priorToolModel struct {
	step     int
	toolName string
}

func (fake *priorToolModel) Stream(_ context.Context, _ model.Request, _ func(model.Event)) (model.Response, error) {
	fake.step++
	if fake.step == 1 {
		name := fake.toolName
		if name == "" {
			name = "read_file"
		}
		return model.Response{ToolCalls: []model.ToolCall{{ID: "tool-1", Name: name, Arguments: `{}`}}}, nil
	}
	return model.Response{Text: "done"}, nil
}

func TestDecisionPolicyIncludesPreviousToolOutcomeOnLaterTurn(t *testing.T) {
	decider := &recordingDecider{result: decision.Result{Choice: "A"}}
	engine := NewEngine(&priorToolModel{}, []Tool{policyTool{}}, 3)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "first turn", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "follow-up turn", nil); err != nil {
		t.Fatal(err)
	}
	if len(decider.states) != 2 {
		t.Fatalf("decision calls = %d, want 2", len(decider.states))
	}
	if decider.states[0].LastToolName != "" || decider.states[0].LastToolSucceeded != nil {
		t.Fatalf("first turn previous-tool state = %#v", decider.states[0])
	}
	previous := decider.states[1]
	if previous.LastToolName != "read_file" || previous.LastToolSucceeded == nil || !*previous.LastToolSucceeded {
		t.Fatalf("follow-up previous-tool state = %#v", previous)
	}
}

func TestDecisionPolicyIncludesPreviousToolFailureOnLaterTurn(t *testing.T) {
	decider := &recordingDecider{result: decision.Result{Choice: "A"}}
	engine := NewEngine(&priorToolModel{toolName: "missing_tool"}, []Tool{policyTool{}}, 3)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "first turn", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "follow-up turn", nil); err != nil {
		t.Fatal(err)
	}
	previous := decider.states[1]
	if previous.LastToolName != "missing_tool" || previous.LastToolSucceeded == nil || *previous.LastToolSucceeded {
		t.Fatalf("follow-up previous-tool state = %#v", previous)
	}
}

func TestDecisionPolicyActiveAppliesFastAndDeliberateChoices(t *testing.T) {
	for _, test := range []struct {
		name    string
		choice  string
		applied bool
		lane    string
	}{
		{name: "fast", choice: "B", applied: true, lane: "fast"},
		{name: "deliberate", choice: "A", applied: true, lane: "deliberate"},
		{name: "uncertain", choice: "C", applied: false, lane: "normal"},
	} {
		t.Run(test.name, func(t *testing.T) {
			streamer := &policyModel{}
			decider := &recordingDecider{result: decision.Result{
				Choice: test.choice, Probabilities: map[string]float64{test.choice: 0.8},
				ComplexityScore: floatPointer(1), MutationProbability: floatPointer(0.1),
			}}
			engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
			if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
				t.Fatal(err)
			}
			var evidence *DecisionEvidence
			if _, err := engine.Run(context.Background(), "task", func(event Event) {
				if event.Kind == EventDecision {
					evidence = event.Decision
				}
			}); err != nil {
				t.Fatal(err)
			}
			options := streamer.requests[0].InferenceOptions
			switch test.choice {
			case "B":
				if options.EnableThinking == nil || *options.EnableThinking {
					t.Fatalf("thinking override = %v, want false", options.EnableThinking)
				}
			case "A":
				if options.EnableThinking != nil || options.ReasoningEffort == nil || *options.ReasoningEffort != "medium" || options.ReasoningBudgetTokens == nil || *options.ReasoningBudgetTokens != 4096 || options.MaxTokens == nil || *options.MaxTokens != 8192 {
					t.Fatalf("deliberate options = %#v", options)
				}
			case "C":
				if options.EnableThinking != nil || options.ReasoningEffort != nil || options.ReasoningBudgetTokens != nil || options.MaxTokens != nil {
					t.Fatalf("uncertain options = %#v, want defaults", options)
				}
			}
			if evidence == nil || evidence.Mode != DecisionActive || evidence.Applied != test.applied || evidence.InferenceLane != test.lane {
				t.Fatalf("decision evidence = %#v", evidence)
			}
		})
	}
}

type activeContinuationModel struct {
	requests []model.Request
}

func (fake *activeContinuationModel) Stream(_ context.Context, request model.Request, _ func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	if len(fake.requests) == 1 {
		return model.Response{ToolCalls: []model.ToolCall{{ID: "read-1", Name: "read_file", Arguments: `{}`}}}, nil
	}
	return model.Response{Text: "done"}, nil
}

func TestDecisionPolicyActiveKeepsDeliberateBudgetAcrossSteps(t *testing.T) {
	streamer := &activeContinuationModel{}
	decider := &recordingDecider{result: decision.Result{
		Choice: "A", Probabilities: map[string]float64{"A": 0.8, "B": 0.1, "C": 0.1},
	}}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 3)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{
		Mode: DecisionActive, Decider: decider, DeliberateReasoningEffort: "xhigh",
		DeliberateReasoningBudgetTokens: 2048, DeliberateMaxTokens: 4096,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "debug then answer", nil); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(streamer.requests))
	}
	for index, request := range streamer.requests {
		options := request.InferenceOptions
		if options.ReasoningEffort == nil || *options.ReasoningEffort != "xhigh" || options.ReasoningBudgetTokens == nil || *options.ReasoningBudgetTokens != 2048 || options.MaxTokens == nil || *options.MaxTokens != 4096 {
			t.Fatalf("request %d options = %#v", index, options)
		}
	}
}

func TestDecisionPolicyActiveKeepsInspectionContinuationFast(t *testing.T) {
	streamer := &activeContinuationModel{}
	decider := &recordingDecider{result: decision.Result{
		Choice: "B", Probabilities: map[string]float64{"A": 0.1, "B": 0.8, "C": 0.1},
		ComplexityScore: floatPointer(1), MutationProbability: floatPointer(0.1),
	}}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 3)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "read then answer", nil); err != nil {
		t.Fatal(err)
	}
	if len(streamer.requests) != 2 {
		t.Fatalf("model requests = %d, want 2", len(streamer.requests))
	}
	first := streamer.requests[0].InferenceOptions.EnableThinking
	second := streamer.requests[1].InferenceOptions.EnableThinking
	if first == nil || *first || second == nil || *second {
		t.Fatalf("thinking overrides = first %v second %v, want false then false", first, second)
	}
}

type changingPolicyTool struct{}

func (changingPolicyTool) Definition() model.Tool {
	return model.Tool{Name: "read_file", Description: "change", Parameters: map[string]any{"type": "object"}}
}
func (changingPolicyTool) Run(context.Context, []byte) (tools.Result, error) {
	return tools.Result{Changed: true}, nil
}

func TestDecisionPolicyActiveRestoresThinkingAfterMutation(t *testing.T) {
	streamer := &activeContinuationModel{}
	decider := &recordingDecider{result: decision.Result{
		Choice: "B", Probabilities: map[string]float64{"A": 0.1, "B": 0.8, "C": 0.1},
		ComplexityScore: floatPointer(1), MutationProbability: floatPointer(0.1),
	}}
	engine := NewEngine(streamer, []Tool{changingPolicyTool{}}, 3)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	// The fake completes without verification; the engine therefore inserts a
	// reminder and reaches its step limit, but the second request still proves
	// that mutation restored configured deliberate reasoning.
	_, _ = engine.Run(context.Background(), "change then answer", nil)
	if len(streamer.requests) < 2 {
		t.Fatalf("model requests = %d, want at least 2", len(streamer.requests))
	}
	first := streamer.requests[0].InferenceOptions.EnableThinking
	second := streamer.requests[1].InferenceOptions.EnableThinking
	if first == nil || *first || second != nil {
		t.Fatalf("thinking overrides = first %v second %v, want false then nil", first, second)
	}
}

func TestDecisionPolicyActiveRequiresConfiguredConfidence(t *testing.T) {
	streamer := &policyModel{}
	decider := &recordingDecider{result: decision.Result{
		Choice: "B", Probabilities: map[string]float64{"A": 0.2, "B": 0.7, "C": 0.1},
	}}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider, MinConfidence: 0.8}); err != nil {
		t.Fatal(err)
	}
	var evidence *DecisionEvidence
	if _, err := engine.Run(context.Background(), "task", func(event Event) {
		if event.Kind == EventDecision {
			evidence = event.Decision
		}
	}); err != nil {
		t.Fatal(err)
	}
	if streamer.requests[0].InferenceOptions.EnableThinking != nil || evidence == nil || evidence.Applied || evidence.MinConfidence != 0.8 {
		t.Fatalf("request=%#v evidence=%#v", streamer.requests[0], evidence)
	}
}

func TestDecisionPolicyActiveUsesAuxiliarySafetySignals(t *testing.T) {
	for _, test := range []struct {
		name       string
		result     decision.Result
		deliberate bool
	}{
		{
			name:   "mutation blocks fast",
			result: decision.Result{Choice: "B", Probabilities: map[string]float64{"A": 0.1, "B": 0.8, "C": 0.1}, MutationProbability: floatPointer(0.9)},
		},
		{
			name:   "complex uncertainty escalates",
			result: decision.Result{Choice: "C", Probabilities: map[string]float64{"A": 0.3, "B": 0.2, "C": 0.5}, ComplexityScore: floatPointer(2.8)}, deliberate: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			streamer := &policyModel{}
			engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
			if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: &recordingDecider{result: test.result}}); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Run(context.Background(), "task", nil); err != nil {
				t.Fatal(err)
			}
			options := streamer.requests[0].InferenceOptions
			if test.deliberate {
				if options.ReasoningBudgetTokens == nil || *options.ReasoningBudgetTokens != 4096 {
					t.Fatalf("options = %#v, want deliberate", options)
				}
			} else if options.EnableThinking != nil || options.ReasoningBudgetTokens != nil {
				t.Fatalf("options = %#v, want normal defaults", options)
			}
		})
	}
}

func floatPointer(value float64) *float64 { return &value }

func TestDecisionPolicyActiveDoesNotApplyTruncatedFastChoice(t *testing.T) {
	streamer := &policyModel{}
	decider := &recordingDecider{result: decision.Result{Choice: "B", InputTruncated: true}}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	var evidence *DecisionEvidence
	if _, err := engine.Run(context.Background(), "task", func(event Event) {
		if event.Kind == EventDecision {
			evidence = event.Decision
		}
	}); err != nil {
		t.Fatal(err)
	}
	if streamer.requests[0].InferenceOptions.EnableThinking != nil || evidence == nil || evidence.Applied || !evidence.InputTruncated {
		t.Fatalf("request=%#v evidence=%#v", streamer.requests[0], evidence)
	}
}

func TestDecisionPolicyFailureFallsBackToExistingModelPath(t *testing.T) {
	streamer := &policyModel{}
	decider := &recordingDecider{err: errors.New("sidecar unavailable")}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	var evidence *DecisionEvidence
	answer, err := engine.Run(context.Background(), "inspect", func(event Event) {
		if event.Kind == EventDecision {
			evidence = event.Decision
		}
	})
	if err != nil || answer != "done" || len(streamer.requests) != 1 {
		t.Fatalf("Run() = %q, %v; model requests=%d", answer, err, len(streamer.requests))
	}
	if evidence == nil || evidence.Error != "sidecar unavailable" || evidence.Applied {
		t.Fatalf("decision evidence = %#v", evidence)
	}
}

type blockingDecider struct{ started chan struct{} }

func (fake blockingDecider) Decide(ctx context.Context, _ decision.State) (decision.Result, error) {
	close(fake.started)
	<-ctx.Done()
	return decision.Result{}, ctx.Err()
}

func TestDecisionPolicyParentCancellationStopsTurn(t *testing.T) {
	streamer := &policyModel{}
	decider := blockingDecider{started: make(chan struct{})}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := engine.Run(ctx, "inspect", nil)
		done <- err
	}()
	<-decider.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	if len(streamer.requests) != 0 {
		t.Fatalf("model requests = %d, want 0", len(streamer.requests))
	}
}

func TestDecisionPolicyCapsUserRequestAtUTF8Boundary(t *testing.T) {
	streamer := &policyModel{}
	decider := &recordingDecider{result: decision.Result{Choice: "C"}}
	engine := NewEngine(streamer, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	prompt := strings.Repeat("a", decisionUserRequestLimit-1) + "界tail"
	var evidence *DecisionEvidence
	if _, err := engine.Run(context.Background(), prompt, func(event Event) {
		if event.Kind == EventDecision {
			evidence = event.Decision
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(decider.states) != 1 || len(decider.states[0].UserRequest) > decisionUserRequestLimit || !evidence.InputTruncated {
		t.Fatalf("state bytes=%d evidence=%#v", len(decider.states[0].UserRequest), evidence)
	}
}

func TestConfigureDecisionPolicyRejectsInvalidConfiguration(t *testing.T) {
	for _, config := range []DecisionPolicyConfig{
		{Mode: DecisionOff, MinConfidence: math.NaN()},
		{Mode: DecisionOff, DeliberateReasoningEffort: "high"},
		{Mode: DecisionOff, DeliberateReasoningBudgetTokens: 4096, DeliberateMaxTokens: 4096},
	} {
		if err := NewEngine(&policyModel{}, []Tool{policyTool{}}, 2).ConfigureDecisionPolicy(config); err == nil {
			t.Fatalf("invalid config accepted: %#v", config)
		}
	}
	engine := NewEngine(&policyModel{}, []Tool{policyTool{}}, 2)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow}); err == nil {
		t.Fatal("shadow mode without decider succeeded")
	}
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive}); err == nil {
		t.Fatal("active mode without decider succeeded")
	}
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: "invalid", Decider: &recordingDecider{}}); err == nil {
		t.Fatal("invalid mode unexpectedly succeeded")
	}
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: &recordingDecider{}, MinConfidence: 1.1}); err == nil {
		t.Fatal("invalid minimum confidence unexpectedly succeeded")
	}
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{
		Mode: DecisionActive, Decider: &recordingDecider{},
		DeliberateReasoningBudgetTokens: 4096, DeliberateMaxTokens: 1024,
	}); err == nil {
		t.Fatal("deliberate max below reasoning budget unexpectedly succeeded")
	}
}
