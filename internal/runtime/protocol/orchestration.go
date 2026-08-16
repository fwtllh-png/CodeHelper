package protocol

import (
	"errors"
	"strings"
)

// OrchestrationCorrelation binds one execution effect to its durable run,
// graph node, and attempt. A correlation is immutable after turn acceptance.
type OrchestrationCorrelation struct {
	RunID     RunID     `json:"run_id"`
	NodeID    NodeID    `json:"node_id"`
	AttemptID AttemptID `json:"attempt_id"`
	EffectID  EffectID  `json:"effect_id"`
}

type RunReference struct {
	RunID RunID `json:"run_id"`
}

func (r RunReference) Validate() error {
	if r.RunID == "" {
		return errors.New("run reference requires run_id")
	}
	return nil
}

type NodeReference struct {
	RunID  RunID  `json:"run_id"`
	NodeID NodeID `json:"node_id"`
}

func (r NodeReference) Validate() error {
	if r.RunID == "" || r.NodeID == "" {
		return errors.New("node reference requires run_id and node_id")
	}
	return nil
}

type AttemptReference struct {
	RunID     RunID     `json:"run_id"`
	NodeID    NodeID    `json:"node_id"`
	AttemptID AttemptID `json:"attempt_id"`
}

func (r AttemptReference) Validate() error {
	if r.RunID == "" || r.NodeID == "" || r.AttemptID == "" {
		return errors.New("attempt reference requires run_id, node_id, and attempt_id")
	}
	return nil
}

func (c OrchestrationCorrelation) Validate() error {
	if c.RunID == "" || c.NodeID == "" || c.AttemptID == "" || c.EffectID == "" {
		return errors.New(
			"orchestration correlation requires run_id, node_id, attempt_id, and effect_id",
		)
	}
	return nil
}

func CloneOrchestrationCorrelation(
	source *OrchestrationCorrelation,
) *OrchestrationCorrelation {
	if source == nil {
		return nil
	}
	cloned := *source
	return &cloned
}

type RunState string

const (
	RunStateSubmitted RunState = "submitted"
	RunStateActive    RunState = "active"
	RunStateWaiting   RunState = "waiting"
	RunStateCanceling RunState = "canceling"
	RunStateCompleted RunState = "completed"
	RunStateFailed    RunState = "failed"
	RunStateCanceled  RunState = "canceled"
	RunStateBlocked   RunState = "blocked"
)

func (s RunState) Valid() bool {
	switch s {
	case RunStateSubmitted, RunStateActive, RunStateWaiting, RunStateCanceling,
		RunStateCompleted, RunStateFailed, RunStateCanceled, RunStateBlocked:
		return true
	default:
		return false
	}
}

type NodeState string

const (
	NodeStatePending   NodeState = "pending"
	NodeStateReady     NodeState = "ready"
	NodeStateLeased    NodeState = "leased"
	NodeStateRunning   NodeState = "running"
	NodeStateWaiting   NodeState = "waiting"
	NodeStateSucceeded NodeState = "succeeded"
	NodeStateFailed    NodeState = "failed"
	NodeStateSkipped   NodeState = "skipped"
	NodeStateCanceled  NodeState = "canceled"
	NodeStateBlocked   NodeState = "blocked"
)

func (s NodeState) Valid() bool {
	switch s {
	case NodeStatePending, NodeStateReady, NodeStateLeased, NodeStateRunning,
		NodeStateWaiting, NodeStateSucceeded, NodeStateFailed, NodeStateSkipped,
		NodeStateCanceled, NodeStateBlocked:
		return true
	default:
		return false
	}
}

type AttemptState string

const (
	AttemptStateCreated       AttemptState = "created"
	AttemptStateLeased        AttemptState = "leased"
	AttemptStateEffectStarted AttemptState = "effect_started"
	AttemptStateWaiting       AttemptState = "waiting"
	AttemptStateSucceeded     AttemptState = "succeeded"
	AttemptStateFailed        AttemptState = "failed"
	AttemptStateCanceled      AttemptState = "canceled"
	AttemptStateInterrupted   AttemptState = "interrupted"
	AttemptStateLeaseLost     AttemptState = "lease_lost"
	AttemptStateIndeterminate AttemptState = "indeterminate"
)

func (s AttemptState) Valid() bool {
	switch s {
	case AttemptStateCreated, AttemptStateLeased, AttemptStateEffectStarted,
		AttemptStateWaiting, AttemptStateSucceeded, AttemptStateFailed,
		AttemptStateCanceled, AttemptStateInterrupted, AttemptStateLeaseLost,
		AttemptStateIndeterminate:
		return true
	default:
		return false
	}
}

func (s AttemptState) Terminal() bool {
	switch s {
	case AttemptStateSucceeded, AttemptStateFailed, AttemptStateCanceled,
		AttemptStateInterrupted, AttemptStateLeaseLost, AttemptStateIndeterminate:
		return true
	default:
		return false
	}
}

type RunStartedData struct {
	Run             RunReference `json:"run"`
	Kind            string       `json:"kind"`
	Source          string       `json:"source"`
	Revision        uint64       `json:"revision"`
	AuthorityDigest string       `json:"authority_digest,omitempty"`
}

func (*RunStartedData) eventKind() EventKind { return EventRunStarted }

func (d *RunStartedData) validate() error {
	if err := d.Run.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Kind) == "" || strings.TrimSpace(d.Source) == "" ||
		d.Revision == 0 {
		return errors.New("run started kind, source, and revision are required")
	}
	if d.AuthorityDigest != "" && !validSHA256(d.AuthorityDigest) {
		return errors.New("run started authority_digest must be a lowercase SHA-256")
	}
	return nil
}

type RunStatusData struct {
	Run      RunReference `json:"run"`
	State    RunState     `json:"state"`
	Revision uint64       `json:"revision"`
	Reason   string       `json:"reason,omitempty"`
}

func (*RunStatusData) eventKind() EventKind { return EventRunStatus }

func (d *RunStatusData) validate() error {
	if err := d.Run.Validate(); err != nil {
		return err
	}
	if !d.State.Valid() || d.Revision == 0 {
		return errors.New("run status state is invalid")
	}
	return nil
}

type RunCompletedData struct {
	Run       RunReference `json:"run"`
	Revision  uint64       `json:"revision"`
	Summary   string       `json:"summary,omitempty"`
	ResultRef string       `json:"result_ref,omitempty"`
}

func (*RunCompletedData) eventKind() EventKind { return EventRunCompleted }

func (d *RunCompletedData) validate() error {
	if err := d.Run.Validate(); err != nil {
		return err
	}
	if d.Revision == 0 {
		return errors.New("run completed revision is required")
	}
	return nil
}

type RunFailedData struct {
	Run      RunReference `json:"run"`
	Revision uint64       `json:"revision"`
	Code     ErrorCode    `json:"code"`
	Message  string       `json:"message"`
}

func (*RunFailedData) eventKind() EventKind { return EventRunFailed }

func (d *RunFailedData) validate() error {
	if err := d.Run.Validate(); err != nil {
		return err
	}
	if d.Revision == 0 || d.Code == "" || strings.TrimSpace(d.Message) == "" {
		return errors.New("run failed code and message are required")
	}
	return nil
}

type RunCanceledData struct {
	Run      RunReference `json:"run"`
	Revision uint64       `json:"revision"`
	Reason   string       `json:"reason"`
}

func (*RunCanceledData) eventKind() EventKind { return EventRunCanceled }

func (d *RunCanceledData) validate() error {
	if err := d.Run.Validate(); err != nil {
		return err
	}
	if d.Revision == 0 || strings.TrimSpace(d.Reason) == "" {
		return errors.New("run canceled reason is required")
	}
	return nil
}

type NodeStatusData struct {
	Node     NodeReference `json:"node"`
	State    NodeState     `json:"state"`
	Revision uint64        `json:"revision"`
	Reason   string        `json:"reason,omitempty"`
}

func (*NodeStatusData) eventKind() EventKind { return EventNodeStatus }

func (d *NodeStatusData) validate() error {
	if err := d.Node.Validate(); err != nil {
		return err
	}
	if !d.State.Valid() || d.Revision == 0 {
		return errors.New("node status state is invalid")
	}
	return nil
}

type AttemptStatusData struct {
	Attempt    AttemptReference `json:"attempt"`
	State      AttemptState     `json:"state"`
	Revision   uint64           `json:"revision"`
	LeaseOwner string           `json:"lease_owner,omitempty"`
	LeaseEpoch uint64           `json:"lease_epoch,omitempty"`
	Reason     string           `json:"reason,omitempty"`
}

func (*AttemptStatusData) eventKind() EventKind { return EventAttemptStatus }

func (d *AttemptStatusData) validate() error {
	if err := d.Attempt.Validate(); err != nil {
		return err
	}
	if !d.State.Valid() || d.Revision == 0 {
		return errors.New("attempt status state is invalid")
	}
	if (d.LeaseOwner == "") != (d.LeaseEpoch == 0) {
		return errors.New("attempt status lease_owner and lease_epoch must be set together")
	}
	return nil
}

type ExecutionBoundData struct {
	Correlation OrchestrationCorrelation `json:"correlation"`
	Kind        string                   `json:"kind"`
	ThreadID    ThreadID                 `json:"thread_id,omitempty"`
	TurnID      TurnID                   `json:"turn_id,omitempty"`
	ProcessID   string                   `json:"process_id,omitempty"`
	LaneID      string                   `json:"lane_id,omitempty"`
}

func (*ExecutionBoundData) eventKind() EventKind { return EventExecutionBound }

func (d *ExecutionBoundData) validate() error {
	if err := d.Correlation.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(d.Kind) == "" {
		return errors.New("execution bound kind is required")
	}
	if (d.ThreadID == "") != (d.TurnID == "") {
		return errors.New("execution bound thread_id and turn_id must be set together")
	}
	if d.ThreadID == "" && d.ProcessID == "" {
		return errors.New("execution bound requires a turn or process identity")
	}
	return nil
}

type BudgetUpdatedData struct {
	Attempt        AttemptReference `json:"attempt"`
	TokensUsed     uint64           `json:"tokens_used"`
	MaxTokens      uint64           `json:"max_tokens,omitempty"`
	CostMicrounits uint64           `json:"cost_microunits"`
	MaxCostMicros  uint64           `json:"max_cost_microunits,omitempty"`
	Reserved       bool             `json:"reserved,omitempty"`
}

func (*BudgetUpdatedData) eventKind() EventKind { return EventBudgetUpdated }

func (d *BudgetUpdatedData) validate() error {
	if err := d.Attempt.Validate(); err != nil {
		return err
	}
	if d.MaxTokens != 0 && d.TokensUsed > d.MaxTokens {
		return errors.New("budget updated tokens_used exceeds max_tokens")
	}
	if d.MaxCostMicros != 0 && d.CostMicrounits > d.MaxCostMicros {
		return errors.New("budget updated cost exceeds max_cost_microunits")
	}
	return nil
}
