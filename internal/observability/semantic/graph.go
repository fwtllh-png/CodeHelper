// Package semantic deterministically reduces canonical observations into a
// rebuildable query graph. It is an observation consumer, never an execution
// authority.
package semantic

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

const Version uint32 = 1

type Status string

const (
	StatusUnknown   Status = "unknown"
	StatusOpen      Status = "open"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusResolved  Status = "resolved"
	StatusCommitted Status = "committed"
	StatusClosed    Status = "closed"
)

type Graph struct {
	Version         uint32                        `json:"version"`
	LastSequence    uint64                        `json:"last_sequence"`
	Runtimes        map[string]RuntimeNode        `json:"runtimes"`
	Threads         map[string]ThreadNode         `json:"threads"`
	Turns           map[string]TurnNode           `json:"turns"`
	InferenceCalls  map[string]InferenceNode      `json:"inference_calls"`
	ToolAttempts    map[string]ToolNode           `json:"tool_attempts"`
	Effects         map[string]EffectNode         `json:"effects"`
	Approvals       map[string]ApprovalNode       `json:"approvals"`
	TerminalOps     map[string]TerminalNode       `json:"terminal_operations"`
	Verifications   map[string]VerificationNode   `json:"verifications"`
	Agents          map[string]AgentNode          `json:"agents"`
	Interactions    []InteractionEdge             `json:"interactions"`
	Visibility      []VisibilityEdge              `json:"visibility"`
	Inconsistencies []Inconsistency               `json:"inconsistencies"`
	Unknowns        []UnknownFact                 `json:"unknowns"`
	Observations    map[string]ObservationSummary `json:"observations"`
}

type ExecutionWindow struct {
	StartedSequence uint64     `json:"started_sequence,omitempty"`
	EndedSequence   uint64     `json:"ended_sequence,omitempty"`
	StartedAt       *time.Time `json:"started_at,omitempty"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	Status          Status     `json:"status"`
}

type RuntimeNode struct {
	ID       string          `json:"id"`
	Window   ExecutionWindow `json:"window"`
	Evidence []Evidence      `json:"evidence"`
}

type ThreadNode struct {
	ID        string     `json:"id"`
	RuntimeID string     `json:"runtime_id"`
	SessionID string     `json:"session_id,omitempty"`
	TurnIDs   []string   `json:"turn_ids"`
	Evidence  []Evidence `json:"evidence"`
}

type TurnNode struct {
	ID        string          `json:"id"`
	RuntimeID string          `json:"runtime_id"`
	SessionID string          `json:"session_id,omitempty"`
	ThreadID  string          `json:"thread_id,omitempty"`
	TurnID    string          `json:"turn_id"`
	Recovered bool            `json:"recovered,omitempty"`
	ResumeIDs []string        `json:"resume_ids,omitempty"`
	Window    ExecutionWindow `json:"window"`
	Evidence  []Evidence      `json:"evidence"`
}

type InferenceNode struct {
	ID              string          `json:"id"`
	RuntimeID       string          `json:"runtime_id"`
	TurnID          string          `json:"turn_id"`
	SampleID        string          `json:"sample_id"`
	Attempt         uint32          `json:"attempt"`
	AttemptExplicit bool            `json:"attempt_explicit"`
	Window          ExecutionWindow `json:"window"`
	Evidence        []Evidence      `json:"evidence"`
}

type ToolNode struct {
	ID              string            `json:"id"`
	RuntimeID       string            `json:"runtime_id"`
	TurnID          string            `json:"turn_id"`
	CallID          string            `json:"call_id"`
	Attempt         uint32            `json:"attempt"`
	AttemptExplicit bool              `json:"attempt_explicit"`
	RuntimeResult   *PayloadEvidence  `json:"runtime_result,omitempty"`
	ModelVisible    []PayloadEvidence `json:"model_visible,omitempty"`
	Window          ExecutionWindow   `json:"window"`
	Evidence        []Evidence        `json:"evidence"`
}

type EffectNode struct {
	ID             string           `json:"id"`
	RuntimeID      string           `json:"runtime_id"`
	EffectID       string           `json:"effect_id"`
	RunID          string           `json:"run_id,omitempty"`
	NodeID         string           `json:"node_id,omitempty"`
	AttemptID      string           `json:"attempt_id,omitempty"`
	LeaseOwner     string           `json:"lease_owner,omitempty"`
	LeaseEpoch     uint64           `json:"lease_epoch,omitempty"`
	RequestedAt    uint64           `json:"requested_sequence,omitempty"`
	ResultRetained *PayloadEvidence `json:"result_retained,omitempty"`
	Requeues       []uint64         `json:"requeues,omitempty"`
	Window         ExecutionWindow  `json:"window"`
	Evidence       []Evidence       `json:"evidence"`
}

type ApprovalNode struct {
	ID        string          `json:"id"`
	RuntimeID string          `json:"runtime_id"`
	TurnID    string          `json:"turn_id"`
	CallID    string          `json:"call_id"`
	Window    ExecutionWindow `json:"window"`
	Evidence  []Evidence      `json:"evidence"`
}

type TerminalNode struct {
	ID          string                       `json:"id"`
	RuntimeID   string                       `json:"runtime_id"`
	TurnID      string                       `json:"turn_id"`
	OperationID string                       `json:"operation_id,omitempty"`
	EffectID    string                       `json:"effect_id,omitempty"`
	Outcome     *observation.TerminalOutcome `json:"outcome,omitempty"`
	Window      ExecutionWindow              `json:"window"`
	Evidence    []Evidence                   `json:"evidence"`
}

type VerificationNode struct {
	ID        string          `json:"id"`
	RuntimeID string          `json:"runtime_id"`
	TurnID    string          `json:"turn_id"`
	Window    ExecutionWindow `json:"window"`
	Evidence  []Evidence      `json:"evidence"`
}

type AgentNode struct {
	ID        string          `json:"id"`
	RuntimeID string          `json:"runtime_id"`
	AgentID   string          `json:"agent_id"`
	Window    ExecutionWindow `json:"window"`
	Evidence  []Evidence      `json:"evidence"`
}

type InteractionEdge struct {
	ID          string                    `json:"id"`
	Kind        observation.Kind          `json:"kind"`
	AgentID     string                    `json:"agent_id"`
	SourceAgent string                    `json:"source_agent_id,omitempty"`
	TargetAgent string                    `json:"target_agent_id,omitempty"`
	Sequence    uint64                    `json:"sequence"`
	Observation observation.ObservationID `json:"observation_id"`
	Payload     *PayloadEvidence          `json:"payload,omitempty"`
}

type VisibilityEdge struct {
	ID          string                    `json:"id"`
	SourceKind  string                    `json:"source_kind"`
	SourceID    string                    `json:"source_id"`
	Target      string                    `json:"target"`
	TargetID    string                    `json:"target_id,omitempty"`
	Sequence    uint64                    `json:"sequence"`
	Observation observation.ObservationID `json:"observation_id"`
	Payload     *PayloadEvidence          `json:"payload,omitempty"`
}

type Evidence struct {
	ObservationID observation.ObservationID `json:"observation_id"`
	Kind          observation.Kind          `json:"kind"`
	Sequence      uint64                    `json:"sequence"`
	Payload       *PayloadEvidence          `json:"payload,omitempty"`
}

type PayloadAvailability string

const (
	PayloadAvailable   PayloadAvailability = "available"
	PayloadUnavailable PayloadAvailability = "unavailable"
	PayloadUnverified  PayloadAvailability = "unverified"
)

type PayloadEvidence struct {
	Reference    observation.PayloadRef `json:"reference"`
	Availability PayloadAvailability    `json:"availability"`
}

type ObservationSummary struct {
	ID               observation.ObservationID `json:"id"`
	Kind             observation.Kind          `json:"kind"`
	Sequence         uint64                    `json:"sequence"`
	ObservedSequence uint64                    `json:"observed_sequence"`
	RuntimeID        string                    `json:"runtime_id"`
}

type Inconsistency struct {
	Code         string                      `json:"code"`
	ObjectKind   string                      `json:"object_kind"`
	ObjectID     string                      `json:"object_id"`
	Sequences    []uint64                    `json:"sequences"`
	Observations []observation.ObservationID `json:"observations"`
	Detail       string                      `json:"detail"`
}

type UnknownFact struct {
	Code        string                    `json:"code"`
	ObjectKind  string                    `json:"object_kind"`
	ObjectID    string                    `json:"object_id"`
	Sequence    uint64                    `json:"sequence,omitempty"`
	Observation observation.ObservationID `json:"observation_id,omitempty"`
	Detail      string                    `json:"detail"`
}

func emptyGraph() Graph {
	return Graph{
		Version:        Version,
		Runtimes:       make(map[string]RuntimeNode),
		Threads:        make(map[string]ThreadNode),
		Turns:          make(map[string]TurnNode),
		InferenceCalls: make(map[string]InferenceNode),
		ToolAttempts:   make(map[string]ToolNode),
		Effects:        make(map[string]EffectNode),
		Approvals:      make(map[string]ApprovalNode),
		TerminalOps:    make(map[string]TerminalNode),
		Verifications:  make(map[string]VerificationNode),
		Agents:         make(map[string]AgentNode),
		Observations:   make(map[string]ObservationSummary),
	}
}
