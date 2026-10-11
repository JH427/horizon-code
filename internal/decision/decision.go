package decision

import "time"

const SchemaVersion = 1

// State contains compact turn-start or post-inspection evidence. File excerpts
// are untrusted task data, not instructions or authority. Conversation history
// and tool arguments are excluded.
type State struct {
	SchemaVersion         int     `json:"schema_version"`
	UserRequest           string  `json:"user_request"`
	CapabilityMode        string  `json:"capability_mode"`
	FirstModelStep        bool    `json:"first_model_step"`
	InspectionEvidence    string  `json:"inspection_evidence,omitempty"`
	InspectionTruncated   bool    `json:"inspection_truncated,omitempty"`
	PendingVerification   bool    `json:"pending_verification"`
	WorkspaceDirty        bool    `json:"workspace_dirty"`
	LastToolName          string  `json:"last_tool_name,omitempty"`
	LastToolSucceeded     *bool   `json:"last_tool_succeeded,omitempty"`
	RepairCount           int     `json:"repair_count"`
	ActiveContextPressure float64 `json:"active_context_pressure"`
}

type Result struct {
	Choice              string             `json:"choice"`
	Probabilities       map[string]float64 `json:"probabilities"`
	Model               string             `json:"model"`
	Checkpoint          string             `json:"checkpoint"`
	EndpointLatency     time.Duration      `json:"endpoint_latency"`
	ObservedLatency     time.Duration      `json:"observed_latency"`
	InputTruncated      bool               `json:"input_truncated"`
	Abstained           bool               `json:"abstained"`
	InputTokens         int                `json:"input_tokens"`
	StateTokens         int                `json:"state_tokens"`
	StateTokensDropped  int                `json:"state_tokens_dropped"`
	ComplexityScore     *float64           `json:"complexity_score,omitempty"`
	MutationProbability *float64           `json:"mutation_probability,omitempty"`
}
