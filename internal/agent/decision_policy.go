package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"

	"github.com/JH427/horizon-code/internal/decision"
	"github.com/JH427/horizon-code/internal/model"
)

const (
	decisionUserRequestLimit   = 8 << 10
	defaultActiveMinConfidence = 0.55
)

type DecisionMode string

const (
	DecisionOff    DecisionMode = "off"
	DecisionShadow DecisionMode = "shadow"
	DecisionActive DecisionMode = "active"
)

// Decider is deliberately narrower than the HTTP implementation. A decision
// receives compact state only and has no access to tools or model settings.
type Decider interface {
	Decide(context.Context, decision.State) (decision.Result, error)
}

type DecisionPolicyConfig struct {
	Mode                            DecisionMode
	Decider                         Decider
	CapabilityMode                  string
	MinConfidence                   float64
	DeliberateReasoningEffort       string
	DeliberateReasoningBudgetTokens int
	DeliberateMaxTokens             int
}

type DecisionEvidence struct {
	SchemaVersion       int                `json:"schema_version"`
	Mode                DecisionMode       `json:"mode"`
	Choice              string             `json:"choice,omitempty"`
	Probabilities       map[string]float64 `json:"probabilities,omitempty"`
	Model               string             `json:"model,omitempty"`
	Checkpoint          string             `json:"checkpoint,omitempty"`
	EndpointLatencyNS   int64              `json:"endpoint_latency_ns,omitempty"`
	ObservedLatencyNS   int64              `json:"observed_latency_ns,omitempty"`
	InputTruncated      bool               `json:"input_truncated"`
	Abstained           bool               `json:"abstained"`
	Error               string             `json:"error,omitempty"`
	Applied             bool               `json:"applied"`
	InputTokens         int                `json:"input_tokens,omitempty"`
	StateTokens         int                `json:"state_tokens,omitempty"`
	StateTokensDropped  int                `json:"state_tokens_dropped,omitempty"`
	MinConfidence       float64            `json:"min_confidence,omitempty"`
	ComplexityScore     *float64           `json:"complexity_score,omitempty"`
	MutationProbability *float64           `json:"mutation_probability,omitempty"`
	InferenceLane       string             `json:"inference_lane,omitempty"`
	Phase               string             `json:"phase"`
	InspectionTruncated bool               `json:"inspection_truncated,omitempty"`
}

func (engine *Engine) ConfigureDecisionPolicy(config DecisionPolicyConfig) error {
	if math.IsNaN(config.MinConfidence) || config.MinConfidence < 0 || config.MinConfidence > 1 {
		return errors.New("decision minimum confidence must be between 0 and 1")
	}
	if config.MinConfidence == 0 {
		config.MinConfidence = defaultActiveMinConfidence
	}
	if config.DeliberateReasoningEffort == "" {
		config.DeliberateReasoningEffort = "medium"
	}
	switch config.DeliberateReasoningEffort {
	case "low", "medium", "xhigh":
	default:
		return errors.New("deliberate reasoning effort must be low, medium, or xhigh")
	}
	if config.DeliberateReasoningBudgetTokens == 0 {
		config.DeliberateReasoningBudgetTokens = 4096
	}
	if config.DeliberateMaxTokens == 0 {
		config.DeliberateMaxTokens = 8192
	}
	if config.DeliberateReasoningBudgetTokens < 0 || config.DeliberateMaxTokens <= config.DeliberateReasoningBudgetTokens {
		return errors.New("deliberate token limits must be positive and max tokens must leave room after the reasoning budget")
	}
	deliberateEffort := config.DeliberateReasoningEffort
	deliberateBudget := config.DeliberateReasoningBudgetTokens
	deliberateMax := config.DeliberateMaxTokens
	deliberate := model.InferenceOptions{
		ReasoningEffort: &deliberateEffort, ReasoningBudgetTokens: &deliberateBudget, MaxTokens: &deliberateMax,
	}
	switch config.Mode {
	case DecisionOff:
		engine.decisionMode = DecisionOff
		engine.decider = nil
		engine.decisionCapabilityMode = config.CapabilityMode
		engine.decisionMinConfidence = config.MinConfidence
		engine.decisionDeliberate = deliberate
		return nil
	case DecisionShadow, DecisionActive:
		if config.Decider == nil {
			return fmt.Errorf("%s decision mode requires a decider", config.Mode)
		}
		engine.decisionMode = config.Mode
		engine.decider = config.Decider
		engine.decisionCapabilityMode = config.CapabilityMode
		engine.decisionMinConfidence = config.MinConfidence
		engine.decisionDeliberate = deliberate
		return nil
	default:
		return fmt.Errorf("unknown decision mode %q", config.Mode)
	}
}

func (engine *Engine) runDecisionPolicy(ctx context.Context, prompt string, pendingVerification bool, lastToolName string, lastToolSucceeded *bool, contextTokens int, inspection string, inspectionTruncated bool, emit func(Event)) (model.InferenceOptions, error) {
	var inference model.InferenceOptions
	if engine.decisionMode == "" || engine.decisionMode == DecisionOff {
		return inference, nil
	}
	userRequest, truncated := truncateDecisionInput(prompt, decisionUserRequestLimit)
	pressure := float64(contextTokens) / float64(engine.budget.MaxContextTokens)
	if pressure > 1 {
		pressure = 1
	}
	state := decision.State{
		SchemaVersion: decision.SchemaVersion, UserRequest: userRequest,
		CapabilityMode: engine.decisionCapabilityMode, FirstModelStep: inspection == "",
		InspectionEvidence: inspection, InspectionTruncated: inspectionTruncated,
		PendingVerification: pendingVerification, WorkspaceDirty: pendingVerification,
		LastToolName: lastToolName, LastToolSucceeded: lastToolSucceeded,
		RepairCount: 0, ActiveContextPressure: pressure,
	}
	result, err := engine.decider.Decide(ctx, state)
	evidence := &DecisionEvidence{
		SchemaVersion: decision.SchemaVersion, Mode: engine.decisionMode,
		InputTruncated: truncated, Applied: false,
	}
	evidence.Phase = "turn_start"
	if inspection != "" {
		evidence.Phase = "post_inspection"
		evidence.InspectionTruncated = inspectionTruncated
	}
	if engine.decisionMode == DecisionActive {
		evidence.MinConfidence = engine.decisionMinConfidence
		evidence.InferenceLane = "normal"
	}
	if err != nil {
		evidence.Error = err.Error()
	} else {
		evidence.Choice = result.Choice
		evidence.Probabilities = cloneProbabilities(result.Probabilities)
		evidence.Model = result.Model
		evidence.Checkpoint = result.Checkpoint
		evidence.EndpointLatencyNS = result.EndpointLatency.Nanoseconds()
		evidence.ObservedLatencyNS = result.ObservedLatency.Nanoseconds()
		evidence.InputTruncated = evidence.InputTruncated || result.InputTruncated
		evidence.Abstained = result.Abstained
		evidence.InputTokens = result.InputTokens
		evidence.StateTokens = result.StateTokens
		evidence.StateTokensDropped = result.StateTokensDropped
		evidence.ComplexityScore = cloneFloat(result.ComplexityScore)
		evidence.MutationProbability = cloneFloat(result.MutationProbability)
		if engine.decisionMode == DecisionActive && !result.Abstained && !evidence.InputTruncated {
			complexTask := result.ComplexityScore != nil && *result.ComplexityScore >= 2.5
			likelyMutation := result.MutationProbability != nil && *result.MutationProbability >= 0.9
			fastSafe := result.ComplexityScore != nil && *result.ComplexityScore <= 1.5 &&
				result.MutationProbability != nil && *result.MutationProbability <= 0.4
			switch {
			case (result.Choice == "A" && result.Probabilities["A"] >= 0.75) || (result.Choice == "C" && (complexTask || likelyMutation)):
				inference = engine.decisionDeliberate
				evidence.Applied = true
				evidence.InferenceLane = "deliberate"
			case result.Choice == "B" && fastSafe && !inspectionTruncated && result.Probabilities["B"] >= engine.decisionMinConfidence:
				disabled := false
				inference.EnableThinking = &disabled
				evidence.Applied = true
				evidence.InferenceLane = "fast"
			}
		}
	}
	// Required repair compute does not depend on sidecar success or abstention.
	if engine.decisionMode == DecisionActive && (pendingVerification || (lastToolSucceeded != nil && !*lastToolSucceeded)) {
		inference = engine.decisionDeliberate
		evidence.Applied = true
		evidence.InferenceLane = "deliberate"
	}
	if emit != nil {
		emit(Event{Kind: EventDecision, Decision: evidence})
	}
	// A sidecar failure is fail-open. Cancellation of the parent turn remains
	// authoritative, including cancellation caused by failed event recording.
	if ctx.Err() != nil {
		return model.InferenceOptions{}, ctx.Err()
	}
	return inference, nil
}

func truncateDecisionInput(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	cut := value[:limit]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut, true
}

func cloneFloat(source *float64) *float64 {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func cloneProbabilities(source map[string]float64) map[string]float64 {
	if source == nil {
		return nil
	}
	cloned := make(map[string]float64, len(source))
	for option, probability := range source {
		cloned[option] = probability
	}
	return cloned
}
