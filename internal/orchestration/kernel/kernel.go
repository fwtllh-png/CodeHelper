// Package kernel owns the pure WorkGraph state transition function.
package kernel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrConflict          = errors.New("work graph revision conflict")
	ErrInvalidTransition = errors.New("invalid work graph transition")
	ErrNotFound          = errors.New("work graph not found")
)

type CommandKind string

const (
	CommandSubmit           CommandKind = "submit_run"
	CommandCancel           CommandKind = "cancel_run"
	CommandBlock            CommandKind = "block_run"
	CommandResume           CommandKind = "resume_run"
	CommandRetryNode        CommandKind = "retry_node"
	CommandSkipNode         CommandKind = "skip_node"
	CommandClaimNode        CommandKind = "claim_node"
	CommandBindExecution    CommandKind = "bind_execution"
	CommandHeartbeatAttempt CommandKind = "heartbeat_attempt"
	CommandReleaseAttempt   CommandKind = "release_attempt"
	CommandSettleExecution  CommandKind = "settle_execution"
	CommandPublishEffect    CommandKind = "publish_effect"
)

type Command struct {
	ID                      string              `json:"id"`
	Kind                    CommandKind         `json:"kind"`
	RunID                   protocol.RunID      `json:"run_id"`
	ExpectedRevision        uint64              `json:"expected_revision"`
	At                      time.Time           `json:"at"`
	Submit                  *SubmitData         `json:"submit,omitempty"`
	NodeID                  protocol.NodeID     `json:"node_id,omitempty"`
	AttemptID               protocol.AttemptID  `json:"attempt_id,omitempty"`
	EffectID                protocol.EffectID   `json:"effect_id,omitempty"`
	LeaseOwner              string              `json:"lease_owner,omitempty"`
	LeaseEpoch              uint64              `json:"lease_epoch,omitempty"`
	LeaseExpiresAt          *time.Time          `json:"lease_expires_at,omitempty"`
	ExpectedAuthorityDigest string              `json:"expected_authority_digest,omitempty"`
	RetryAt                 *time.Time          `json:"retry_at,omitempty"`
	ConsumeAttempt          bool                `json:"consume_attempt,omitempty"`
	Execution               *model.ExecutionRef `json:"execution,omitempty"`
	Settlement              *SettlementData     `json:"settlement,omitempty"`
	Reason                  string              `json:"reason,omitempty"`
}

type SubmitData struct {
	Kind             model.RunKind     `json:"kind"`
	Source           string            `json:"source"`
	SessionID        string            `json:"session_id"`
	Workspace        string            `json:"workspace,omitempty"`
	RootThreadID     protocol.ThreadID `json:"root_thread_id"`
	DefinitionDigest string            `json:"definition_digest,omitempty"`
	AuthorityDigest  string            `json:"authority_digest,omitempty"`
	Nodes            []model.NodeSpec  `json:"nodes"`
}

type SettlementData struct {
	State             protocol.NodeState `json:"state"`
	ResultRef         string             `json:"result_ref,omitempty"`
	Result            json.RawMessage    `json:"result,omitempty"`
	Reason            string             `json:"reason,omitempty"`
	PermissionDigests []string           `json:"permission_digests,omitempty"`
}

type FactKind string

const (
	FactRunSubmitted   FactKind = "run_submitted"
	FactRunStatus      FactKind = "run_status"
	FactNodeDeclared   FactKind = "node_declared"
	FactNodeStatus     FactKind = "node_status"
	FactAttemptCreated FactKind = "attempt_created"
	FactAttemptStatus  FactKind = "attempt_status"
	FactExecutionBound FactKind = "execution_bound"
	FactEffectQueued   FactKind = "effect_queued"
	FactEffectStatus   FactKind = "effect_status"
)

type Fact struct {
	Sequence uint64         `json:"sequence"`
	Revision uint64         `json:"revision"`
	Kind     FactKind       `json:"kind"`
	At       time.Time      `json:"at"`
	Run      *model.Run     `json:"run,omitempty"`
	Node     *model.Node    `json:"node,omitempty"`
	Attempt  *model.Attempt `json:"attempt,omitempty"`
	Effect   *model.Effect  `json:"effect,omitempty"`
}

type Result struct {
	Graph     model.Graph    `json:"graph"`
	Facts     []Fact         `json:"facts,omitempty"`
	Effects   []model.Effect `json:"effects,omitempty"`
	Duplicate bool           `json:"duplicate,omitempty"`
}

func Reduce(current model.Graph, command Command) (Result, error) {
	return reduce(current, command, true)
}

// ReduceOwned applies a command to an aggregate the caller exclusively owns.
// Durable stores use it after decoding a private snapshot; public callers use
// Reduce when they need copy isolation.
func ReduceOwned(current model.Graph, command Command) (Result, error) {
	return reduce(current, command, false)
}

func reduce(
	current model.Graph,
	command Command,
	clone bool,
) (Result, error) {
	if err := validateCommand(command); err != nil {
		return Result{}, err
	}
	if current.Run.ID == "" {
		current = model.Empty(command.RunID)
	}
	if current.Run.ID != command.RunID {
		return Result{}, fmt.Errorf("%w: command run id does not match aggregate", ErrConflict)
	}
	if current.Run.Revision != command.ExpectedRevision {
		return Result{}, fmt.Errorf(
			"%w: expected %d, found %d",
			ErrConflict,
			command.ExpectedRevision,
			current.Run.Revision,
		)
	}
	graph := current
	if clone {
		graph = model.Clone(current)
	}
	builder := transitionBuilder{
		graph:    graph,
		at:       command.At.UTC(),
		revision: current.Run.Revision + 1,
	}
	var err error
	switch command.Kind {
	case CommandSubmit:
		err = builder.submit(*command.Submit)
	case CommandCancel:
		err = builder.cancel(command.Reason)
	case CommandBlock:
		err = builder.block(command.Reason)
	case CommandResume:
		err = builder.resume()
	case CommandRetryNode:
		err = builder.retryNode(command.NodeID)
	case CommandSkipNode:
		err = builder.skipNode(command.NodeID, command.Reason)
	case CommandClaimNode:
		err = builder.claimNode(command)
	case CommandBindExecution:
		err = builder.bindExecution(command)
	case CommandHeartbeatAttempt:
		err = builder.heartbeatAttempt(command)
	case CommandReleaseAttempt:
		err = builder.releaseAttempt(command)
	case CommandSettleExecution:
		err = builder.settleExecution(command)
	case CommandPublishEffect:
		err = builder.publishEffect(command.EffectID)
	default:
		err = fmt.Errorf("unknown work graph command %q", command.Kind)
	}
	if err != nil {
		return Result{}, err
	}
	builder.graph.Run.Revision = builder.revision
	builder.graph.Run.UpdatedAt = builder.at
	if clone || current.Run.Revision == 0 {
		if err := builder.graph.Validate(); err != nil {
			return Result{}, fmt.Errorf(
				"reduced work graph violates invariants: %w",
				err,
			)
		}
	}
	return Result{
		Graph:   builder.graph,
		Facts:   builder.facts,
		Effects: builder.effects,
	}, nil
}

func validateCommand(command Command) error {
	if strings.TrimSpace(command.ID) == "" || command.RunID == "" ||
		command.At.IsZero() {
		return errors.New("work graph command id, run id, and time are required")
	}
	switch command.Kind {
	case CommandSubmit:
		if command.Submit == nil {
			return errors.New("submit command payload is required")
		}
	case CommandCancel, CommandBlock:
		if strings.TrimSpace(command.Reason) == "" {
			return errors.New("run transition reason is required")
		}
	case CommandRetryNode, CommandSkipNode:
		if command.NodeID == "" {
			return errors.New("node command node id is required")
		}
	case CommandClaimNode:
		if command.NodeID == "" || command.AttemptID == "" ||
			command.EffectID == "" || command.LeaseOwner == "" ||
			command.LeaseEpoch == 0 || command.LeaseExpiresAt == nil {
			return errors.New("claim command identities and lease are required")
		}
	case CommandBindExecution:
		if command.AttemptID == "" || command.Execution == nil ||
			command.Execution.EffectID == "" || command.LeaseOwner == "" ||
			command.LeaseEpoch == 0 {
			return errors.New("bind command attempt, execution, and lease are required")
		}
	case CommandHeartbeatAttempt:
		if command.AttemptID == "" || command.LeaseOwner == "" ||
			command.LeaseEpoch == 0 || command.LeaseExpiresAt == nil {
			return errors.New("heartbeat command attempt and lease are required")
		}
	case CommandReleaseAttempt:
		if command.AttemptID == "" || command.LeaseOwner == "" ||
			command.LeaseEpoch == 0 || strings.TrimSpace(command.Reason) == "" {
			return errors.New("release command attempt, lease, and reason are required")
		}
	case CommandSettleExecution:
		if command.AttemptID == "" || command.Settlement == nil ||
			command.LeaseOwner == "" || command.LeaseEpoch == 0 {
			return errors.New("settle command attempt, settlement, and lease are required")
		}
	case CommandPublishEffect:
		if command.EffectID == "" {
			return errors.New("publish effect command effect id is required")
		}
	}
	return nil
}

type transitionBuilder struct {
	graph    model.Graph
	at       time.Time
	revision uint64
	facts    []Fact
	effects  []model.Effect
}

func (b *transitionBuilder) append(fact Fact) {
	fact.Sequence = b.graph.NextSequence
	fact.Revision = b.revision
	fact.At = b.at
	b.graph.NextSequence++
	b.facts = append(b.facts, fact)
}

func (b *transitionBuilder) setRun(run model.Run, kind FactKind) {
	run.Revision = b.revision
	run.UpdatedAt = b.at
	b.graph.Run = run
	copy := run
	b.append(Fact{Kind: kind, Run: &copy})
}

func (b *transitionBuilder) setNode(node model.Node, kind FactKind) {
	node.UpdatedAt = b.at
	b.graph.Nodes[node.ID] = node
	copy := node
	copy.Dependencies = append([]protocol.NodeID(nil), node.Dependencies...)
	if node.Condition != nil {
		condition := *node.Condition
		copy.Condition = &condition
	}
	copy.Result = append(json.RawMessage(nil), node.Result...)
	if node.Execution != nil {
		execution := *node.Execution
		execution.Payload = append(json.RawMessage(nil), node.Execution.Payload...)
		copy.Execution = &execution
	}
	if node.RetryAt != nil {
		retryAt := *node.RetryAt
		copy.RetryAt = &retryAt
	}
	b.append(Fact{Kind: kind, Node: &copy})
}

func (b *transitionBuilder) setAttempt(attempt model.Attempt, kind FactKind) {
	b.graph.Attempts[attempt.ID] = attempt
	copy := attempt
	if attempt.Execution != nil {
		execution := *attempt.Execution
		copy.Execution = &execution
	}
	if attempt.LeaseExpiresAt != nil {
		expires := *attempt.LeaseExpiresAt
		copy.LeaseExpiresAt = &expires
	}
	if attempt.HeartbeatAt != nil {
		heartbeat := *attempt.HeartbeatAt
		copy.HeartbeatAt = &heartbeat
	}
	b.append(Fact{Kind: kind, Attempt: &copy})
}

func (b *transitionBuilder) queueEffect(effect model.Effect) {
	b.graph.Effects[effect.ID] = effect
	b.effects = append(b.effects, effect)
	copy := effect
	copy.Payload = append(json.RawMessage(nil), effect.Payload...)
	b.append(Fact{Kind: FactEffectQueued, Effect: &copy})
}

func (b *transitionBuilder) updateEffect(effect model.Effect) {
	b.graph.Effects[effect.ID] = effect
	b.effects = append(b.effects, effect)
	copy := effect
	copy.Payload = append(json.RawMessage(nil), effect.Payload...)
	b.append(Fact{Kind: FactEffectStatus, Effect: &copy})
}

func (b *transitionBuilder) publishEffect(effectID protocol.EffectID) error {
	effect, exists := b.graph.Effects[effectID]
	if !exists {
		return ErrNotFound
	}
	if effect.Kind != model.EffectPublishTerminal {
		return fmt.Errorf(
			"%w: only terminal publication effects can be acknowledged",
			ErrInvalidTransition,
		)
	}
	if effect.State != model.EffectPending {
		return fmt.Errorf(
			"%w: terminal publication effect is not pending",
			ErrInvalidTransition,
		)
	}
	effect.State = model.EffectDispatched
	b.updateEffect(effect)
	return nil
}

func (b *transitionBuilder) submit(data SubmitData) error {
	if b.graph.Run.Revision != 0 {
		return fmt.Errorf("%w: run already exists", ErrInvalidTransition)
	}
	if !data.Kind.Valid() || strings.TrimSpace(data.Source) == "" ||
		strings.TrimSpace(data.SessionID) == "" || data.RootThreadID == "" {
		return errors.New("submitted run kind, source, session, and root thread are required")
	}
	if err := model.ValidateNodeSpecs(data.Nodes); err != nil {
		return err
	}
	run := model.Run{
		ID: b.graph.Run.ID, Kind: data.Kind, Source: data.Source,
		SessionID: data.SessionID, Workspace: data.Workspace,
		RootThreadID:     data.RootThreadID,
		DefinitionDigest: data.DefinitionDigest,
		State:            protocol.RunStateSubmitted,
		AuthorityDigest:  data.AuthorityDigest,
		CreatedAt:        b.at, UpdatedAt: b.at,
	}
	b.setRun(run, FactRunSubmitted)
	for _, spec := range data.Nodes {
		authorityDigest := spec.AuthorityDigest
		if authorityDigest == "" {
			authorityDigest = run.AuthorityDigest
		}
		var execution *model.ExecutionSpec
		if spec.Execution != nil {
			copied := *spec.Execution
			copied.Payload = append(json.RawMessage(nil), spec.Execution.Payload...)
			execution = &copied
		}
		var condition *model.NodeCondition
		if spec.Condition != nil {
			copied := *spec.Condition
			condition = &copied
		}
		node := model.Node{
			ID: spec.ID, RunID: run.ID, Kind: spec.Kind,
			State:           protocol.NodeStatePending,
			Dependencies:    append([]protocol.NodeID(nil), spec.Dependencies...),
			Condition:       condition,
			AuthorityDigest: authorityDigest,
			Execution:       execution,
			CreatedAt:       b.at, UpdatedAt: b.at,
		}
		b.setNode(node, FactNodeDeclared)
	}
	for _, id := range model.SortedNodeIDs(b.graph.Nodes) {
		node := b.graph.Nodes[id]
		if len(node.Dependencies) == 0 {
			node.State = protocol.NodeStateReady
			b.setNode(node, FactNodeStatus)
		}
	}
	run = b.graph.Run
	run.State = protocol.RunStateActive
	b.setRun(run, FactRunStatus)
	return nil
}

func (b *transitionBuilder) cancel(reason string) error {
	if b.graph.Run.Revision == 0 {
		return ErrNotFound
	}
	if runTerminal(b.graph.Run.State) {
		return fmt.Errorf("%w: terminal run cannot be canceled", ErrInvalidTransition)
	}
	run := b.graph.Run
	run.State, run.Reason = protocol.RunStateCanceling, reason
	b.setRun(run, FactRunStatus)
	active := false
	for _, id := range model.SortedNodeIDs(b.graph.Nodes) {
		node := b.graph.Nodes[id]
		switch node.State {
		case protocol.NodeStatePending, protocol.NodeStateReady:
			node.State, node.Reason = protocol.NodeStateCanceled, reason
			b.setNode(node, FactNodeStatus)
		case protocol.NodeStateLeased, protocol.NodeStateRunning,
			protocol.NodeStateWaiting:
			active = true
			attempt, ok := b.activeAttempt(node.ID)
			if !ok {
				return fmt.Errorf("active node %q has no active attempt", node.ID)
			}
			effect := model.Effect{
				ID: protocol.EffectID(fmt.Sprintf(
					"effect_cancel_%s_%s_%d",
					b.graph.Run.ID,
					attempt.ID,
					b.revision,
				)),
				RunID: b.graph.Run.ID, NodeID: node.ID, AttemptID: attempt.ID,
				Kind: model.EffectCancelExecution, State: model.EffectPending,
				AuthorityDigest: attempt.AuthorityDigest,
				IdempotencyKey:  fmt.Sprintf("cancel:%s:%d", attempt.ID, b.revision),
				CreatedAt:       b.at,
			}
			b.queueEffect(effect)
		}
	}
	if !active {
		b.settleRun(protocol.RunStateCanceled, reason)
	}
	return nil
}

func (b *transitionBuilder) block(reason string) error {
	if b.graph.Run.Revision == 0 {
		return ErrNotFound
	}
	if runTerminal(b.graph.Run.State) ||
		b.graph.Run.State == protocol.RunStateBlocked {
		return fmt.Errorf("%w: run cannot be blocked from %s", ErrInvalidTransition, b.graph.Run.State)
	}
	for _, node := range b.graph.Nodes {
		switch node.State {
		case protocol.NodeStateLeased, protocol.NodeStateRunning,
			protocol.NodeStateWaiting:
			return fmt.Errorf("%w: active run cannot be blocked", ErrInvalidTransition)
		}
	}
	for _, id := range model.SortedNodeIDs(b.graph.Nodes) {
		node := b.graph.Nodes[id]
		switch node.State {
		case protocol.NodeStatePending, protocol.NodeStateReady:
			node.State, node.Reason = protocol.NodeStateBlocked, reason
			b.setNode(node, FactNodeStatus)
		}
	}
	b.settleRun(protocol.RunStateBlocked, reason)
	return nil
}

func (b *transitionBuilder) resume() error {
	run := b.graph.Run
	if run.State != protocol.RunStateBlocked && run.State != protocol.RunStateWaiting {
		return fmt.Errorf("%w: run state %s cannot resume", ErrInvalidTransition, run.State)
	}
	run.State, run.Reason = protocol.RunStateActive, ""
	b.setRun(run, FactRunStatus)
	for _, id := range model.SortedNodeIDs(b.graph.Nodes) {
		node := b.graph.Nodes[id]
		if node.State == protocol.NodeStateBlocked {
			node.State, node.Reason = protocol.NodeStatePending, ""
			b.setNode(node, FactNodeStatus)
		}
	}
	b.deriveReady()
	return nil
}

func (b *transitionBuilder) retryNode(id protocol.NodeID) error {
	node, exists := b.graph.Nodes[id]
	if !exists {
		return ErrNotFound
	}
	switch node.State {
	case protocol.NodeStateSucceeded, protocol.NodeStateFailed,
		protocol.NodeStateBlocked,
		protocol.NodeStateCanceled, protocol.NodeStateSkipped:
	default:
		return fmt.Errorf("%w: node state %s cannot retry", ErrInvalidTransition, node.State)
	}
	if _, active := b.activeAttempt(id); active {
		return fmt.Errorf("%w: node has an active attempt", ErrInvalidTransition)
	}
	node.State, node.Reason, node.ResultRef, node.Result =
		protocol.NodeStatePending, "", "", nil
	node.AttemptsConsumed = 0
	node.RetryAt = nil
	b.setNode(node, FactNodeStatus)
	run := b.graph.Run
	if runTerminal(run.State) || run.State == protocol.RunStateBlocked {
		run.State, run.Reason = protocol.RunStateActive, ""
		b.setRun(run, FactRunStatus)
	}
	b.deriveReady()
	return nil
}

func (b *transitionBuilder) skipNode(id protocol.NodeID, reason string) error {
	node, exists := b.graph.Nodes[id]
	if !exists {
		return ErrNotFound
	}
	switch node.State {
	case protocol.NodeStatePending, protocol.NodeStateReady,
		protocol.NodeStateFailed, protocol.NodeStateBlocked:
	default:
		return fmt.Errorf("%w: node state %s cannot skip", ErrInvalidTransition, node.State)
	}
	node.State, node.Reason = protocol.NodeStateSkipped, reason
	b.setNode(node, FactNodeStatus)
	b.deriveReady()
	b.maybeSettleRun()
	return nil
}

func (b *transitionBuilder) claimNode(command Command) error {
	node, exists := b.graph.Nodes[command.NodeID]
	if !exists {
		return ErrNotFound
	}
	if node.State != protocol.NodeStateReady ||
		b.graph.Run.State != protocol.RunStateActive {
		return fmt.Errorf("%w: node is not ready", ErrInvalidTransition)
	}
	if command.ExpectedAuthorityDigest != "" &&
		command.ExpectedAuthorityDigest != node.AuthorityDigest {
		return fmt.Errorf("%w: node authority digest changed", ErrConflict)
	}
	if node.RetryAt != nil && b.at.Before(*node.RetryAt) {
		return fmt.Errorf("%w: node retry backoff has not elapsed", ErrInvalidTransition)
	}
	if node.Execution != nil &&
		node.AttemptsConsumed >= node.Execution.MaxAttempts {
		return fmt.Errorf("%w: node attempts are exhausted", ErrInvalidTransition)
	}
	if _, exists := b.graph.Attempts[command.AttemptID]; exists {
		return fmt.Errorf("%w: attempt already exists", ErrInvalidTransition)
	}
	number := node.AttemptsConsumed + 1
	attempt := model.Attempt{
		ID: command.AttemptID, RunID: b.graph.Run.ID, NodeID: node.ID,
		Number: number, State: protocol.AttemptStateCreated,
		AuthorityDigest: node.AuthorityDigest, StartedAt: b.at,
	}
	b.setAttempt(attempt, FactAttemptCreated)
	attempt.State = protocol.AttemptStateLeased
	attempt.LeaseOwner, attempt.LeaseEpoch = command.LeaseOwner, command.LeaseEpoch
	expires := command.LeaseExpiresAt.UTC()
	attempt.LeaseExpiresAt, attempt.HeartbeatAt = &expires, &b.at
	b.setAttempt(attempt, FactAttemptStatus)
	node.State = protocol.NodeStateLeased
	node.RetryAt = nil
	b.setNode(node, FactNodeStatus)
	effect := model.Effect{
		ID: command.EffectID, RunID: b.graph.Run.ID,
		NodeID: node.ID, AttemptID: attempt.ID,
		Kind: model.EffectDispatchExecution, State: model.EffectPending,
		AuthorityDigest: attempt.AuthorityDigest,
		IdempotencyKey:  "dispatch:" + string(command.AttemptID),
		CreatedAt:       b.at,
	}
	b.queueEffect(effect)
	return nil
}

func (b *transitionBuilder) bindExecution(command Command) error {
	attempt, exists := b.graph.Attempts[command.AttemptID]
	if !exists {
		return ErrNotFound
	}
	if attempt.State != protocol.AttemptStateLeased &&
		attempt.State != protocol.AttemptStateEffectStarted {
		return fmt.Errorf("%w: attempt cannot bind execution", ErrInvalidTransition)
	}
	if err := requireLease(attempt, command); err != nil {
		return err
	}
	node := b.graph.Nodes[attempt.NodeID]
	execution := *command.Execution
	if execution.EffectID == "" {
		return errors.New("execution effect id is required")
	}
	effect, exists := b.graph.Effects[execution.EffectID]
	if !exists || effect.AttemptID != attempt.ID ||
		effect.Kind != model.EffectDispatchExecution {
		return errors.New("execution must bind the attempt dispatch effect")
	}
	if strings.TrimSpace(execution.Kind) == "" ||
		((execution.ThreadID == "") != (execution.TurnID == "")) ||
		(execution.ThreadID == "" && execution.ProcessID == "") {
		return errors.New("execution identity is invalid")
	}
	attempt.State, attempt.Execution = protocol.AttemptStateEffectStarted, &execution
	b.setAttempt(attempt, FactExecutionBound)
	effect.State = model.EffectDispatched
	b.updateEffect(effect)
	node.State = protocol.NodeStateRunning
	b.setNode(node, FactNodeStatus)
	return nil
}

func (b *transitionBuilder) heartbeatAttempt(command Command) error {
	attempt, exists := b.graph.Attempts[command.AttemptID]
	if !exists {
		return ErrNotFound
	}
	if attempt.State.Terminal() {
		return fmt.Errorf("%w: terminal attempt cannot heartbeat", ErrInvalidTransition)
	}
	if err := requireLease(attempt, command); err != nil {
		return err
	}
	expires := command.LeaseExpiresAt.UTC()
	attempt.LeaseExpiresAt, attempt.HeartbeatAt = &expires, &b.at
	b.setAttempt(attempt, FactAttemptStatus)
	return nil
}

func (b *transitionBuilder) releaseAttempt(command Command) error {
	attempt, exists := b.graph.Attempts[command.AttemptID]
	if !exists {
		return ErrNotFound
	}
	if attempt.State.Terminal() {
		return fmt.Errorf("%w: attempt is already terminal", ErrInvalidTransition)
	}
	if err := requireLease(attempt, command); err != nil {
		return err
	}
	ended := b.at
	switch command.Reason {
	case "lease_expired":
		attempt.State = protocol.AttemptStateLeaseLost
	case "draining", "interrupted":
		attempt.State = protocol.AttemptStateInterrupted
	default:
		attempt.State = protocol.AttemptStateFailed
	}
	attempt.EndedAt, attempt.Reason = &ended, command.Reason
	b.setAttempt(attempt, FactAttemptStatus)
	node := b.graph.Nodes[attempt.NodeID]
	if command.ConsumeAttempt {
		node.AttemptsConsumed++
	}
	exhausted := node.Execution != nil &&
		node.AttemptsConsumed >= node.Execution.MaxAttempts
	if exhausted {
		node.State, node.Reason = protocol.NodeStateFailed, command.Reason
		node.RetryAt = nil
	} else {
		node.State, node.Reason = protocol.NodeStateReady, ""
		if command.RetryAt != nil {
			retryAt := command.RetryAt.UTC()
			node.RetryAt = &retryAt
		} else {
			node.RetryAt = nil
		}
	}
	b.setNode(node, FactNodeStatus)
	if attempt.Execution != nil && command.Reason == "lease_expired" {
		effect := model.Effect{
			ID: protocol.EffectID(fmt.Sprintf(
				"effect_release_%s_%d",
				attempt.ID,
				b.revision,
			)),
			RunID: b.graph.Run.ID, NodeID: node.ID, AttemptID: attempt.ID,
			Kind: model.EffectCancelExecution, State: model.EffectPending,
			AuthorityDigest: attempt.AuthorityDigest,
			IdempotencyKey: fmt.Sprintf(
				"release:%s:%d",
				attempt.ID,
				b.revision,
			),
			CreatedAt: b.at,
		}
		b.queueEffect(effect)
	}
	if exhausted {
		b.maybeSettleRun()
	}
	return nil
}

func (b *transitionBuilder) settleExecution(command Command) error {
	attempt, exists := b.graph.Attempts[command.AttemptID]
	if !exists {
		return ErrNotFound
	}
	if attempt.State != protocol.AttemptStateEffectStarted &&
		attempt.State != protocol.AttemptStateWaiting {
		return fmt.Errorf("%w: attempt is not executable", ErrInvalidTransition)
	}
	if err := requireLease(attempt, command); err != nil {
		return err
	}
	settlement := *command.Settlement
	var attemptState protocol.AttemptState
	switch settlement.State {
	case protocol.NodeStateSucceeded:
		attemptState = protocol.AttemptStateSucceeded
	case protocol.NodeStateFailed:
		attemptState = protocol.AttemptStateFailed
	case protocol.NodeStateCanceled:
		attemptState = protocol.AttemptStateCanceled
	case protocol.NodeStateBlocked:
		attemptState = protocol.AttemptStateIndeterminate
	default:
		return errors.New("execution settlement state is invalid")
	}
	ended := b.at
	attempt.State, attempt.EndedAt, attempt.Reason = attemptState, &ended, settlement.Reason
	attempt.PermissionDigests = append(
		[]string(nil),
		settlement.PermissionDigests...,
	)
	b.setAttempt(attempt, FactAttemptStatus)
	node := b.graph.Nodes[attempt.NodeID]
	node.AttemptsConsumed++
	node.State, node.ResultRef, node.Reason =
		settlement.State, settlement.ResultRef, settlement.Reason
	node.Result = append(json.RawMessage(nil), settlement.Result...)
	node.RetryAt = nil
	b.setNode(node, FactNodeStatus)
	if b.graph.Run.State == protocol.RunStateCanceling {
		b.cancelRemaining()
	} else {
		b.deriveReady()
	}
	b.maybeSettleRun()
	return nil
}

func requireLease(attempt model.Attempt, command Command) error {
	if attempt.LeaseOwner != command.LeaseOwner ||
		attempt.LeaseEpoch != command.LeaseEpoch {
		return fmt.Errorf(
			"%w: attempt lease is %s/%d, command has %s/%d",
			ErrConflict,
			attempt.LeaseOwner,
			attempt.LeaseEpoch,
			command.LeaseOwner,
			command.LeaseEpoch,
		)
	}
	return nil
}

func (b *transitionBuilder) cancelRemaining() {
	for _, id := range model.SortedNodeIDs(b.graph.Nodes) {
		node := b.graph.Nodes[id]
		if node.State == protocol.NodeStatePending ||
			node.State == protocol.NodeStateReady {
			node.State = protocol.NodeStateCanceled
			node.Reason = b.graph.Run.Reason
			b.setNode(node, FactNodeStatus)
		}
	}
}

func (b *transitionBuilder) deriveReady() {
	for {
		changed := false
		for _, id := range model.SortedNodeIDs(b.graph.Nodes) {
			node := b.graph.Nodes[id]
			if node.State != protocol.NodeStatePending {
				continue
			}
			allTerminal, dependencyFailed := true, false
			for _, dependencyID := range node.Dependencies {
				dependency := b.graph.Nodes[dependencyID]
				if !nodeTerminal(dependency.State) {
					allTerminal = false
					break
				}
				if dependency.State != protocol.NodeStateSucceeded {
					dependencyFailed = true
				}
			}
			if !allTerminal {
				continue
			}
			if node.Condition != nil {
				actual := b.graph.Nodes[node.Condition.NodeID].State
				if actual == node.Condition.State {
					node.State = protocol.NodeStateReady
				} else {
					node.State = protocol.NodeStateSkipped
					node.Reason = fmt.Sprintf(
						"condition not met: %s is %s, want %s",
						node.Condition.NodeID,
						actual,
						node.Condition.State,
					)
				}
			} else if node.Kind == model.NodeKindJoin {
				node.State = protocol.NodeStateReady
			} else if dependencyFailed {
				node.State, node.Reason =
					protocol.NodeStateSkipped, "dependency did not succeed"
			} else {
				node.State = protocol.NodeStateReady
			}
			b.setNode(node, FactNodeStatus)
			changed = true
		}
		if !changed {
			return
		}
	}
}

func (b *transitionBuilder) maybeSettleRun() {
	var failed, blocked, canceled bool
	for _, node := range b.graph.Nodes {
		if !nodeTerminal(node.State) {
			return
		}
		failed = failed || node.State == protocol.NodeStateFailed
		blocked = blocked || node.State == protocol.NodeStateBlocked
		canceled = canceled || node.State == protocol.NodeStateCanceled
	}
	switch {
	case b.graph.Run.State == protocol.RunStateCanceling || canceled:
		b.settleRun(protocol.RunStateCanceled, b.graph.Run.Reason)
	case blocked:
		b.settleRun(protocol.RunStateBlocked, "one or more nodes are blocked")
	case failed:
		b.settleRun(protocol.RunStateFailed, "one or more nodes failed")
	default:
		b.settleRun(protocol.RunStateCompleted, "")
	}
}

func (b *transitionBuilder) settleRun(state protocol.RunState, reason string) {
	run := b.graph.Run
	run.State, run.Reason = state, reason
	b.setRun(run, FactRunStatus)
	if !runTerminal(state) {
		return
	}
	effect := model.Effect{
		ID: protocol.EffectID(fmt.Sprintf(
			"effect_terminal_%s_%d",
			run.ID,
			b.revision,
		)),
		RunID: run.ID, Kind: model.EffectPublishTerminal,
		State:           model.EffectPending,
		AuthorityDigest: run.AuthorityDigest,
		IdempotencyKey:  fmt.Sprintf("terminal:%s:%d", run.ID, b.revision),
		CreatedAt:       b.at,
	}
	b.queueEffect(effect)
}

func (b *transitionBuilder) activeAttempt(
	nodeID protocol.NodeID,
) (model.Attempt, bool) {
	for _, attempt := range b.graph.Attempts {
		if attempt.NodeID == nodeID && !attempt.State.Terminal() {
			return attempt, true
		}
	}
	return model.Attempt{}, false
}

func runTerminal(state protocol.RunState) bool {
	switch state {
	case protocol.RunStateCompleted, protocol.RunStateFailed,
		protocol.RunStateCanceled:
		return true
	default:
		return false
	}
}

func nodeTerminal(state protocol.NodeState) bool {
	switch state {
	case protocol.NodeStateSucceeded, protocol.NodeStateFailed,
		protocol.NodeStateSkipped, protocol.NodeStateCanceled,
		protocol.NodeStateBlocked:
		return true
	default:
		return false
	}
}

func ApplyFact(graph *model.Graph, fact Fact) error {
	if graph == nil || fact.Sequence == 0 || fact.Revision == 0 ||
		fact.At.IsZero() {
		return errors.New("work graph fact identity is invalid")
	}
	if graph.NextSequence == 0 {
		graph.NextSequence = 1
	}
	if fact.Sequence != graph.NextSequence {
		return fmt.Errorf(
			"work graph fact sequence %d, want %d",
			fact.Sequence,
			graph.NextSequence,
		)
	}
	switch fact.Kind {
	case FactRunSubmitted, FactRunStatus:
		if fact.Run == nil {
			return errors.New("run fact is missing run")
		}
		graph.Run = *fact.Run
	case FactNodeDeclared, FactNodeStatus:
		if fact.Node == nil {
			return errors.New("node fact is missing node")
		}
		if graph.Nodes == nil {
			graph.Nodes = make(map[protocol.NodeID]model.Node)
		}
		graph.Nodes[fact.Node.ID] = *fact.Node
	case FactAttemptCreated, FactAttemptStatus, FactExecutionBound:
		if fact.Attempt == nil {
			return errors.New("attempt fact is missing attempt")
		}
		if graph.Attempts == nil {
			graph.Attempts = make(map[protocol.AttemptID]model.Attempt)
		}
		graph.Attempts[fact.Attempt.ID] = *fact.Attempt
	case FactEffectQueued, FactEffectStatus:
		if fact.Effect == nil {
			return errors.New("effect fact is missing effect")
		}
		if graph.Effects == nil {
			graph.Effects = make(map[protocol.EffectID]model.Effect)
		}
		graph.Effects[fact.Effect.ID] = *fact.Effect
	default:
		return fmt.Errorf("unknown work graph fact %q", fact.Kind)
	}
	graph.NextSequence++
	graph.Run.Revision = fact.Revision
	graph.Run.UpdatedAt = fact.At
	return nil
}
