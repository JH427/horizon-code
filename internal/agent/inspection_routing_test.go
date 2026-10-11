package agent

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/JH427/horizon-code/internal/decision"
	"github.com/JH427/horizon-code/internal/model"
	"github.com/JH427/horizon-code/internal/tools"
)

type inspectionTool struct {
	content string
	changed bool
}

func (tool inspectionTool) Definition() model.Tool {
	return model.Tool{Name: "read_file", Parameters: map[string]any{"type": "object"}}
}
func (tool inspectionTool) Run(context.Context, []byte) (tools.Result, error) {
	return tools.Result{Content: tool.content, Changed: tool.changed}, nil
}

type evidenceDecider struct{ states []decision.State }

func (fake *evidenceDecider) Decide(_ context.Context, state decision.State) (decision.Result, error) {
	fake.states = append(fake.states, state)
	if state.InspectionEvidence != "" {
		return decision.Result{Choice: "B", Probabilities: map[string]float64{"A": .05, "B": .9, "C": .05}, ComplexityScore: floatPointer(1), MutationProbability: floatPointer(.1)}, nil
	}
	return decision.Result{Choice: "A", Probabilities: map[string]float64{"A": .9, "B": .05, "C": .05}}, nil
}

func TestPostInspectionDecisionReceivesTaskAndReplacesInitialRoute(t *testing.T) {
	for _, task := range []string{"Print primes through 100", "Draw cubes of increasing size"} {
		t.Run(task, func(t *testing.T) {
			streamer := &twoRoundInspectionModel{}
			decider := &evidenceDecider{}
			engine := NewEngine(streamer, []Tool{inspectionTool{content: task}}, 4)
			if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
				t.Fatal(err)
			}
			var phases []string
			if _, err := engine.Run(context.Background(), "Work on TASK.md", func(e Event) {
				if e.Decision != nil {
					phases = append(phases, e.Decision.Phase)
				}
			}); err != nil {
				t.Fatal(err)
			}
			if len(decider.states) != 2 || decider.states[1].FirstModelStep || !strings.Contains(decider.states[1].InspectionEvidence, task) {
				t.Fatalf("states=%#v", decider.states)
			}
			if decider.states[1].UserRequest != "Work on TASK.md" {
				t.Fatal("original request lost")
			}
			if streamer.requests[0].InferenceOptions.ReasoningBudgetTokens == nil || streamer.requests[2].InferenceOptions.EnableThinking == nil || *streamer.requests[2].InferenceOptions.EnableThinking {
				t.Fatal("post-inspection route not applied")
			}
			if len(phases) != 2 || phases[0] != "turn_start" || phases[1] != "post_inspection" {
				t.Fatalf("phases=%v", phases)
			}
		})
	}
}

func TestTruncatedInspectionCannotEnableFastLane(t *testing.T) {
	streamer := &twoRoundInspectionModel{}
	decider := &evidenceDecider{}
	engine := NewEngine(streamer, []Tool{inspectionTool{content: strings.Repeat("界", 1000)}}, 4)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "Read task", nil); err != nil {
		t.Fatal(err)
	}
	if len(decider.states) != 2 || !decider.states[1].InspectionTruncated || len(decider.states[1].InspectionEvidence) > 2048 {
		t.Fatalf("states=%#v", decider.states)
	}
	if streamer.requests[2].InferenceOptions.EnableThinking != nil {
		t.Fatal("truncated evidence enabled fast lane")
	}
}

type twoRoundInspectionModel struct{ requests []model.Request }

func (fake *twoRoundInspectionModel) Stream(_ context.Context, request model.Request, _ func(model.Event)) (model.Response, error) {
	fake.requests = append(fake.requests, request)
	if len(fake.requests) <= 2 {
		return model.Response{ToolCalls: []model.ToolCall{{ID: "read", Name: "read_file", Arguments: `{}`}}}, nil
	}
	return model.Response{Text: "done"}, nil
}

func TestShadowRequestsMatchDecisionOff(t *testing.T) {
	var requests [][]model.Request
	for _, mode := range []DecisionMode{DecisionOff, DecisionShadow} {
		fake := &twoRoundInspectionModel{}
		engine := NewEngine(fake, []Tool{inspectionTool{content: "task evidence"}}, 4)
		if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: mode, Decider: &evidenceDecider{}}); err != nil {
			t.Fatal(err)
		}
		if _, err := engine.Run(context.Background(), "inspect", nil); err != nil {
			t.Fatal(err)
		}
		requests = append(requests, fake.requests)
	}
	if !reflect.DeepEqual(requests[0], requests[1]) {
		t.Fatal("shadow mode changed Qwen requests")
	}
}

type lateInspectionModel struct{ steps int }

func (fake *lateInspectionModel) Stream(_ context.Context, _ model.Request, _ func(model.Event)) (model.Response, error) {
	fake.steps++
	name := "list_files"
	if fake.steps == 3 {
		name = "read_file"
	}
	if fake.steps <= 3 {
		return model.Response{ToolCalls: []model.ToolCall{{ID: "inspect", Name: name, Arguments: `{}`}}}, nil
	}
	return model.Response{Text: "done"}, nil
}

func TestInspectionWindowDoesNotReopenForLateReads(t *testing.T) {
	decider := &evidenceDecider{}
	engine := NewEngine(&lateInspectionModel{}, []Tool{inspectionTool{content: "late task"}, &finalizationTool{name: "list_files"}}, 5)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "inspect", nil); err != nil {
		t.Fatal(err)
	}
	if len(decider.states) != 1 {
		t.Fatalf("decision calls=%d, want only turn start", len(decider.states))
	}
}

type repeatedInspectionModel struct{ steps int }

func (fake *repeatedInspectionModel) Stream(_ context.Context, _ model.Request, _ func(model.Event)) (model.Response, error) {
	fake.steps++
	if fake.steps <= 3 {
		return model.Response{ToolCalls: []model.ToolCall{{ID: "read", Name: "read_file", Arguments: `{}`}}}, nil
	}
	return model.Response{Text: "done"}, nil
}

func TestPostInspectionRoutingIsBoundedAndShadowOnlyRecords(t *testing.T) {
	decider := &evidenceDecider{}
	engine := NewEngine(&repeatedInspectionModel{}, []Tool{inspectionTool{content: "task evidence"}}, 5)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	var applied bool
	if _, err := engine.Run(context.Background(), "Read task", func(event Event) {
		if event.Decision != nil && event.Decision.Applied {
			applied = true
		}
	}); err != nil {
		t.Fatal(err)
	}
	if len(decider.states) != 2 || applied {
		t.Fatalf("calls=%d applied=%t", len(decider.states), applied)
	}
}

func TestMutationSuppressesPostInspectionRouting(t *testing.T) {
	fake := &endlessFinalizationModel{}
	decider := &evidenceDecider{}
	// This fake mutates before reading: no post-inspection route may be applied.
	engine := NewEngine(fake, []Tool{&finalizationTool{name: "edit_file"}, &finalizationTool{name: "run_command"}, inspectionTool{content: "task"}}, 8)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionShadow, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	_, _ = engine.Run(context.Background(), "Work on task", nil)
	if len(decider.states) != 1 {
		t.Fatalf("calls=%d; mutation should suppress rerouting", len(decider.states))
	}
}
