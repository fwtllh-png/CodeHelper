// Package model defines the durable WorkGraph aggregate shared by orchestration
// reducers and repositories. It contains data and invariants, not I/O.
package model

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type RunKind string
type NodeKind string
type EffectKind string
type EffectState string

const (
	RunKindAgentTask  RunKind = "agent_task"
	RunKindWorkflow   RunKind = "workflow"
	RunKindAutomation RunKind = "automation"
	RunKindBackground RunKind = "background_command"
	RunKindVerify     RunKind = "verification"

	NodeKindAgentTurn NodeKind = "agent_turn"
	NodeKindPhase     NodeKind = "workflow_phase"
	NodeKindProcess   NodeKind = "process"
	NodeKindVerify    NodeKind = "verification"
	NodeKindIntegrate NodeKind = "integration"
	NodeKindApproval  NodeKind = "approval_gate"
	NodeKindInput     NodeKind = "input_gate"
	NodeKindJoin      NodeKind = "join"

	EffectDispatchExecution EffectKind = "dispatch_execution"
	EffectCancelExecution   EffectKind = "cancel_execution"
	EffectPublishTerminal   EffectKind = "publish_run_terminal"

	EffectPending    EffectState = "pending"
	EffectDispatched EffectState = "dispatched"
)

type Run struct {
	ID               protocol.RunID    `json:"id"`
	Kind             RunKind           `json:"kind"`
	Source           string            `json:"source"`
	SessionID        string            `json:"session_id"`
	Workspace        string            `json:"workspace,omitempty"`
	RootThreadID     protocol.ThreadID `json:"root_thread_id"`
	DefinitionDigest string            `json:"definition_digest,omitempty"`
	State            protocol.RunState `json:"state"`
	Revision         uint64            `json:"revision"`
	AuthorityDigest  string            `json:"authority_digest,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type Node struct {
	ID               protocol.NodeID    `json:"id"`
	RunID            protocol.RunID     `json:"run_id"`
	Kind             NodeKind           `json:"kind"`
	State            protocol.NodeState `json:"state"`
	Dependencies     []protocol.NodeID  `json:"dependencies,omitempty"`
	Condition        *NodeCondition     `json:"condition,omitempty"`
	AuthorityDigest  string             `json:"authority_digest,omitempty"`
	Execution        *ExecutionSpec     `json:"execution,omitempty"`
	AttemptsConsumed int                `json:"attempts_consumed,omitempty"`
	RetryAt          *time.Time         `json:"retry_at,omitempty"`
	ResultRef        string             `json:"result_ref,omitempty"`
	Result           json.RawMessage    `json:"result,omitempty"`
	Reason           string             `json:"reason,omitempty"`
	CreatedAt        time.Time          `json:"created_at"`
	UpdatedAt        time.Time          `json:"updated_at"`
}

type NodeCondition struct {
	NodeID protocol.NodeID    `json:"node_id"`
	State  protocol.NodeState `json:"state"`
}

type ExecutionSpec struct {
	TaskID      string          `json:"task_id,omitempty"`
	TaskKind    string          `json:"task_kind,omitempty"`
	ThreadID    string          `json:"thread_id,omitempty"`
	TurnID      string          `json:"turn_id,omitempty"`
	Executor    string          `json:"executor"`
	Payload     json.RawMessage `json:"payload"`
	MaxAttempts int             `json:"max_attempts"`
}

type ExecutionRef struct {
	Kind      string            `json:"kind"`
	EffectID  protocol.EffectID `json:"effect_id"`
	ThreadID  protocol.ThreadID `json:"thread_id,omitempty"`
	TurnID    protocol.TurnID   `json:"turn_id,omitempty"`
	ProcessID string            `json:"process_id,omitempty"`
	LaneID    string            `json:"lane_id,omitempty"`
}

type Attempt struct {
	ID                protocol.AttemptID    `json:"id"`
	RunID             protocol.RunID        `json:"run_id"`
	NodeID            protocol.NodeID       `json:"node_id"`
	Number            int                   `json:"number"`
	State             protocol.AttemptState `json:"state"`
	LeaseOwner        string                `json:"lease_owner,omitempty"`
	LeaseEpoch        uint64                `json:"lease_epoch,omitempty"`
	LeaseExpiresAt    *time.Time            `json:"lease_expires_at,omitempty"`
	HeartbeatAt       *time.Time            `json:"heartbeat_at,omitempty"`
	AuthorityDigest   string                `json:"authority_digest,omitempty"`
	PermissionDigests []string              `json:"permission_digests,omitempty"`
	Execution         *ExecutionRef         `json:"execution,omitempty"`
	Reason            string                `json:"reason,omitempty"`
	StartedAt         time.Time             `json:"started_at"`
	EndedAt           *time.Time            `json:"ended_at,omitempty"`
}

type Effect struct {
	ID              protocol.EffectID  `json:"id"`
	RunID           protocol.RunID     `json:"run_id"`
	NodeID          protocol.NodeID    `json:"node_id,omitempty"`
	AttemptID       protocol.AttemptID `json:"attempt_id,omitempty"`
	Kind            EffectKind         `json:"kind"`
	State           EffectState        `json:"state"`
	AuthorityDigest string             `json:"authority_digest,omitempty"`
	IdempotencyKey  string             `json:"idempotency_key"`
	Payload         json.RawMessage    `json:"payload,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
}

type Graph struct {
	Run          Run                            `json:"run"`
	Nodes        map[protocol.NodeID]Node       `json:"nodes"`
	Attempts     map[protocol.AttemptID]Attempt `json:"attempts"`
	Effects      map[protocol.EffectID]Effect   `json:"effects"`
	NextSequence uint64                         `json:"next_sequence"`
}

type NodeSpec struct {
	ID              protocol.NodeID   `json:"id"`
	Kind            NodeKind          `json:"kind"`
	Dependencies    []protocol.NodeID `json:"dependencies,omitempty"`
	Condition       *NodeCondition    `json:"condition,omitempty"`
	AuthorityDigest string            `json:"authority_digest,omitempty"`
	Execution       *ExecutionSpec    `json:"execution,omitempty"`
}

func Empty(runID protocol.RunID) Graph {
	return Graph{
		Run:          Run{ID: runID},
		Nodes:        make(map[protocol.NodeID]Node),
		Attempts:     make(map[protocol.AttemptID]Attempt),
		Effects:      make(map[protocol.EffectID]Effect),
		NextSequence: 1,
	}
}

func Clone(source Graph) Graph {
	cloned := source
	cloned.Nodes = make(map[protocol.NodeID]Node, len(source.Nodes))
	for id, node := range source.Nodes {
		node.Dependencies = append([]protocol.NodeID(nil), node.Dependencies...)
		if node.Condition != nil {
			condition := *node.Condition
			node.Condition = &condition
		}
		node.Result = append(json.RawMessage(nil), node.Result...)
		if node.Execution != nil {
			execution := *node.Execution
			execution.Payload = append(json.RawMessage(nil), node.Execution.Payload...)
			node.Execution = &execution
		}
		if node.RetryAt != nil {
			retryAt := *node.RetryAt
			node.RetryAt = &retryAt
		}
		cloned.Nodes[id] = node
	}
	cloned.Attempts = make(map[protocol.AttemptID]Attempt, len(source.Attempts))
	for id, attempt := range source.Attempts {
		attempt.PermissionDigests = append(
			[]string(nil),
			attempt.PermissionDigests...,
		)
		if attempt.Execution != nil {
			execution := *attempt.Execution
			attempt.Execution = &execution
		}
		if attempt.EndedAt != nil {
			ended := *attempt.EndedAt
			attempt.EndedAt = &ended
		}
		if attempt.LeaseExpiresAt != nil {
			expires := *attempt.LeaseExpiresAt
			attempt.LeaseExpiresAt = &expires
		}
		if attempt.HeartbeatAt != nil {
			heartbeat := *attempt.HeartbeatAt
			attempt.HeartbeatAt = &heartbeat
		}
		cloned.Attempts[id] = attempt
	}
	cloned.Effects = make(map[protocol.EffectID]Effect, len(source.Effects))
	for id, effect := range source.Effects {
		effect.Payload = append(json.RawMessage(nil), effect.Payload...)
		cloned.Effects[id] = effect
	}
	if cloned.Nodes == nil {
		cloned.Nodes = make(map[protocol.NodeID]Node)
	}
	if cloned.Attempts == nil {
		cloned.Attempts = make(map[protocol.AttemptID]Attempt)
	}
	if cloned.Effects == nil {
		cloned.Effects = make(map[protocol.EffectID]Effect)
	}
	if cloned.NextSequence == 0 {
		cloned.NextSequence = 1
	}
	return cloned
}

func ValidateNodeSpecs(specs []NodeSpec) error {
	if len(specs) == 0 {
		return errors.New("work graph requires at least one node")
	}
	nodes := make(map[protocol.NodeID]NodeSpec, len(specs))
	for _, spec := range specs {
		if spec.ID == "" || !spec.Kind.Valid() {
			return errors.New("work graph node id and kind are required")
		}
		if spec.AuthorityDigest != "" && !validDigest(spec.AuthorityDigest) {
			return fmt.Errorf(
				"work graph node %q authority digest is invalid",
				spec.ID,
			)
		}
		if spec.Execution != nil {
			if strings.TrimSpace(spec.Execution.Executor) == "" ||
				spec.Execution.MaxAttempts <= 0 ||
				len(spec.Execution.Payload) == 0 ||
				!json.Valid(spec.Execution.Payload) {
				return fmt.Errorf(
					"work graph node %q execution is invalid",
					spec.ID,
				)
			}
		}
		if spec.Condition != nil {
			validState := spec.Condition.State == protocol.NodeStateSucceeded ||
				spec.Condition.State == protocol.NodeStateFailed ||
				spec.Condition.State == protocol.NodeStateSkipped ||
				spec.Condition.State == protocol.NodeStateCanceled ||
				spec.Condition.State == protocol.NodeStateBlocked
			if spec.Condition.NodeID == "" || !validState {
				return fmt.Errorf(
					"work graph node %q condition is invalid",
					spec.ID,
				)
			}
		}
		if _, exists := nodes[spec.ID]; exists {
			return fmt.Errorf("duplicate work graph node %q", spec.ID)
		}
		nodes[spec.ID] = spec
	}
	for _, spec := range specs {
		seen := make(map[protocol.NodeID]bool, len(spec.Dependencies))
		for _, dependency := range spec.Dependencies {
			if dependency == spec.ID {
				return fmt.Errorf("work graph node %q depends on itself", spec.ID)
			}
			if _, exists := nodes[dependency]; !exists {
				return fmt.Errorf(
					"work graph node %q has unknown dependency %q",
					spec.ID,
					dependency,
				)
			}
			if seen[dependency] {
				return fmt.Errorf(
					"work graph node %q repeats dependency %q",
					spec.ID,
					dependency,
				)
			}
			seen[dependency] = true
		}
		if spec.Condition != nil && !seen[spec.Condition.NodeID] {
			return fmt.Errorf(
				"work graph node %q condition is not a dependency",
				spec.ID,
			)
		}
	}
	visiting := make(map[protocol.NodeID]bool, len(specs))
	visited := make(map[protocol.NodeID]bool, len(specs))
	var visit func(protocol.NodeID) error
	visit = func(id protocol.NodeID) error {
		if visiting[id] {
			return fmt.Errorf("work graph contains a dependency cycle at %q", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		for _, dependency := range nodes[id].Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		delete(visiting, id)
		visited[id] = true
		return nil
	}
	for id := range nodes {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func (g Graph) Validate() error {
	if g.Run.ID == "" {
		return errors.New("work graph run id is required")
	}
	if !g.Run.State.Valid() || !g.Run.Kind.Valid() {
		return errors.New("work graph run state or kind is invalid")
	}
	if g.Run.AuthorityDigest != "" && !validDigest(g.Run.AuthorityDigest) {
		return errors.New("work graph run authority digest is invalid")
	}
	if g.Run.DefinitionDigest != "" && !validDigest(g.Run.DefinitionDigest) {
		return errors.New("work graph definition digest is invalid")
	}
	if g.Run.Revision == 0 || g.NextSequence == 0 {
		return errors.New("work graph revision and next sequence must be positive")
	}
	specs := make([]NodeSpec, 0, len(g.Nodes))
	activeByNode := make(map[protocol.NodeID]int)
	for id, node := range g.Nodes {
		if id == "" || node.ID != id || node.RunID != g.Run.ID ||
			!node.State.Valid() || !node.Kind.Valid() {
			return fmt.Errorf("work graph node %q is inconsistent", id)
		}
		specs = append(specs, NodeSpec{
			ID: node.ID, Kind: node.Kind,
			Dependencies:    node.Dependencies,
			Condition:       node.Condition,
			AuthorityDigest: node.AuthorityDigest,
			Execution:       node.Execution,
		})
		if node.AttemptsConsumed < 0 {
			return fmt.Errorf("work graph node %q has negative attempts", id)
		}
		if node.Execution != nil &&
			node.AttemptsConsumed > node.Execution.MaxAttempts {
			return fmt.Errorf("work graph node %q exceeded max attempts", id)
		}
		if len(node.Result) != 0 && !json.Valid(node.Result) {
			return fmt.Errorf("work graph node %q result is invalid", id)
		}
	}
	if err := ValidateNodeSpecs(specs); err != nil {
		return err
	}
	for id, attempt := range g.Attempts {
		if id == "" || attempt.ID != id || attempt.RunID != g.Run.ID ||
			attempt.Number <= 0 || !attempt.State.Valid() {
			return fmt.Errorf("work graph attempt %q is inconsistent", id)
		}
		if attempt.AuthorityDigest != "" &&
			!validDigest(attempt.AuthorityDigest) {
			return fmt.Errorf(
				"work graph attempt %q authority digest is invalid",
				id,
			)
		}
		for _, digest := range attempt.PermissionDigests {
			if !validDigest(digest) {
				return fmt.Errorf(
					"work graph attempt %q permission digest is invalid",
					id,
				)
			}
		}
		if _, exists := g.Nodes[attempt.NodeID]; !exists {
			return fmt.Errorf("work graph attempt %q has unknown node", id)
		}
		if (attempt.LeaseOwner == "") != (attempt.LeaseEpoch == 0) {
			return fmt.Errorf("work graph attempt %q has an incomplete lease", id)
		}
		if attempt.LeaseExpiresAt != nil && attempt.LeaseOwner == "" {
			return fmt.Errorf("work graph attempt %q expiry has no owner", id)
		}
		if attempt.State == protocol.AttemptStateLeased &&
			attempt.LeaseOwner == "" {
			return fmt.Errorf("work graph leased attempt %q has no owner", id)
		}
		if attempt.State == protocol.AttemptStateEffectStarted &&
			attempt.Execution == nil {
			return fmt.Errorf("work graph started attempt %q has no execution", id)
		}
		if !attempt.State.Terminal() {
			activeByNode[attempt.NodeID]++
		}
	}
	for nodeID, count := range activeByNode {
		if count > 1 {
			return fmt.Errorf("work graph node %q has %d active attempts", nodeID, count)
		}
	}
	for id, node := range g.Nodes {
		active := activeByNode[id]
		switch node.State {
		case protocol.NodeStateLeased, protocol.NodeStateRunning,
			protocol.NodeStateWaiting:
			if active != 1 {
				return fmt.Errorf(
					"work graph active node %q has %d active attempts",
					id,
					active,
				)
			}
		default:
			if active != 0 {
				return fmt.Errorf(
					"work graph inactive node %q has an active attempt",
					id,
				)
			}
		}
		if runStateTerminal(g.Run.State) && !nodeStateTerminal(node.State) {
			return fmt.Errorf("terminal work graph has nonterminal node %q", id)
		}
	}
	for id, effect := range g.Effects {
		if id == "" || effect.ID != id || effect.RunID != g.Run.ID ||
			!effect.Kind.Valid() || !effect.State.Valid() ||
			strings.TrimSpace(effect.IdempotencyKey) == "" {
			return fmt.Errorf("work graph effect %q is inconsistent", id)
		}
		if effect.AuthorityDigest != "" &&
			!validDigest(effect.AuthorityDigest) {
			return fmt.Errorf(
				"work graph effect %q authority digest is invalid",
				id,
			)
		}
		if effect.NodeID != "" {
			if _, exists := g.Nodes[effect.NodeID]; !exists {
				return fmt.Errorf("work graph effect %q has unknown node", id)
			}
		}
		if effect.AttemptID != "" {
			attempt, exists := g.Attempts[effect.AttemptID]
			if !exists || attempt.NodeID != effect.NodeID {
				return fmt.Errorf("work graph effect %q has unknown attempt", id)
			}
		}
	}
	return nil
}

func (k RunKind) Valid() bool {
	switch k {
	case RunKindAgentTask, RunKindWorkflow, RunKindAutomation,
		RunKindBackground, RunKindVerify:
		return true
	default:
		return false
	}
}

func (k NodeKind) Valid() bool {
	switch k {
	case NodeKindAgentTurn, NodeKindPhase, NodeKindProcess, NodeKindVerify,
		NodeKindIntegrate, NodeKindApproval, NodeKindInput, NodeKindJoin:
		return true
	default:
		return false
	}
}

func (k EffectKind) Valid() bool {
	return k == EffectDispatchExecution || k == EffectCancelExecution ||
		k == EffectPublishTerminal
}

func (s EffectState) Valid() bool {
	return s == EffectPending || s == EffectDispatched
}

func SortedNodeIDs(nodes map[protocol.NodeID]Node) []protocol.NodeID {
	ids := make([]protocol.NodeID, 0, len(nodes))
	for id := range nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}

func runStateTerminal(state protocol.RunState) bool {
	return state == protocol.RunStateCompleted ||
		state == protocol.RunStateFailed ||
		state == protocol.RunStateCanceled
}

func nodeStateTerminal(state protocol.NodeState) bool {
	switch state {
	case protocol.NodeStateSucceeded, protocol.NodeStateFailed,
		protocol.NodeStateSkipped, protocol.NodeStateCanceled,
		protocol.NodeStateBlocked:
		return true
	default:
		return false
	}
}

func validDigest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
