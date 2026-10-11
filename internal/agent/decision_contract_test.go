package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/JH427/horizon-code/internal/decision"
	"github.com/JH427/horizon-code/internal/tools"
)

func TestHTTPUncertaintyAndExplicitAbstentionHaveDifferentPolicies(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			abstention := ""
			if explicit {
				abstention = `,"abstention":"abstained"`
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"model":"THX-01","answers":{"inference_mode":{"type":"choice","choice":"C","probabilities":{"A":0.2,"B":0.1,"C":0.7}%s},"task_complexity":{"type":"score","score":2.8},"workspace_mutation":{"type":"noul","noul":0.95}},"usage":{}}`, abstention)
			}))
			defer server.Close()
			client, err := decision.NewClient(decision.Config{BaseURL: server.URL, Timeout: time.Second}, server.Client())
			if err != nil {
				t.Fatal(err)
			}
			fake := &policyModel{}
			engine := NewEngine(fake, []Tool{policyTool{}}, 2)
			if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: client}); err != nil {
				t.Fatal(err)
			}
			if _, err := engine.Run(context.Background(), "complex task", nil); err != nil {
				t.Fatal(err)
			}
			escalated := fake.requests[0].InferenceOptions.ReasoningBudgetTokens != nil
			if escalated == explicit {
				t.Fatalf("explicit abstention=%t escalated=%t", explicit, escalated)
			}
		})
	}
}

func TestRequiredRepairSurvivesSidecarFailureAndAbstention(t *testing.T) {
	for _, decider := range []*recordingDecider{
		{err: errors.New("unavailable")},
		{result: decision.Result{Choice: "C", Abstained: true}},
	} {
		engine := NewEngine(&policyModel{}, nil, 2)
		if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
			t.Fatal(err)
		}
		failed := false
		options, err := engine.runDecisionPolicy(context.Background(), "repair", false, "run_command", &failed, 100, "", false, nil)
		if err != nil || options.ReasoningBudgetTokens == nil {
			t.Fatalf("repair options=%#v err=%v", options, err)
		}
	}
}

func TestMissingAuxiliaryOutputsCannotEnableFastRoute(t *testing.T) {
	fake := &policyModel{}
	engine := NewEngine(fake, []Tool{policyTool{}}, 2)
	decider := &recordingDecider{result: decision.Result{Choice: "B", Probabilities: map[string]float64{"B": .95}}}
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "inspect", nil); err != nil {
		t.Fatal(err)
	}
	if fake.requests[0].InferenceOptions.EnableThinking != nil {
		t.Fatal("missing evidence enabled fast mode")
	}
}

type failingInspectionTool struct{ policyTool }

func (failingInspectionTool) Run(context.Context, []byte) (tools.Result, error) {
	return tools.Result{}, errors.New("read failed")
}

func TestToolFailureEscalatesFastTurnWithoutAnotherClassifierCall(t *testing.T) {
	fake := &activeContinuationModel{}
	decider := &recordingDecider{result: decision.Result{Choice: "B", Probabilities: map[string]float64{"B": .9}, ComplexityScore: floatPointer(1), MutationProbability: floatPointer(.1)}}
	engine := NewEngine(fake, []Tool{failingInspectionTool{}}, 3)
	if err := engine.ConfigureDecisionPolicy(DecisionPolicyConfig{Mode: DecisionActive, Decider: decider}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), "inspect", nil); err != nil {
		t.Fatal(err)
	}
	if fake.requests[0].InferenceOptions.EnableThinking == nil || fake.requests[1].InferenceOptions.ReasoningBudgetTokens == nil || len(decider.states) != 1 {
		t.Fatal("failure did not independently escalate repair")
	}
}

func TestIntermediateVerificationDoesNotLockOrdinaryTask(t *testing.T) {
	fake := &finalizationModel{}
	edit := &finalizationTool{name: "edit_file"}
	engine := NewEngine(fake, []Tool{edit, &finalizationTool{name: "run_command"}}, 8)
	_, _ = engine.Run(context.Background(), "multi-stage implementation", nil)
	if edit.runs != 2 {
		t.Fatalf("intermediate verification blocked next edit: runs=%d", edit.runs)
	}
}
