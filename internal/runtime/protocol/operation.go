package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type OperationKind string

const (
	OperationStartTurn         OperationKind = "turn.start"
	OperationCancelTurn        OperationKind = "turn.cancel"
	OperationSteerTurn         OperationKind = "turn.steer"
	OperationEnqueueTurn       OperationKind = "turn.enqueue"
	OperationUpdateQueuedTurn  OperationKind = "turn.queue.update"
	OperationRemoveQueuedTurn  OperationKind = "turn.queue.remove"
	OperationPromoteQueuedTurn OperationKind = "turn.queue.promote"
	OperationApprovalDecision  OperationKind = "approval.decision"
	OperationInputReply        OperationKind = "input.reply"
	OperationCompactThread     OperationKind = "thread.compact"
	OperationForkThread        OperationKind = "thread.fork"
	OperationRevertTurn        OperationKind = "turn.revert"
	OperationSubmitRun         OperationKind = "run.submit"
	OperationCancelRun         OperationKind = "run.cancel"
	OperationResumeRun         OperationKind = "run.resume"
	OperationRetryNode         OperationKind = "node.retry"
	OperationSkipNode          OperationKind = "node.skip"
)

func IsWorkGraphOperation(kind OperationKind) bool {
	switch kind {
	case OperationSubmitRun, OperationCancelRun, OperationResumeRun,
		OperationRetryNode, OperationSkipNode:
		return true
	default:
		return false
	}
}

type ApprovalDecision string

const (
	ApprovalApprove ApprovalDecision = "approve"
	ApprovalDeny    ApprovalDecision = "deny"
	ApprovalCancel  ApprovalDecision = "cancel"
)

type ApprovalScope string

const (
	ApprovalScopeOnce    ApprovalScope = "once"
	ApprovalScopeSession ApprovalScope = "session"
	ApprovalScopeAlways  ApprovalScope = "always"
)

type TurnIntent string

const (
	TurnIntentAnswer          TurnIntent = "answer"
	TurnIntentPlan            TurnIntent = "plan"
	TurnIntentWorkspaceChange TurnIntent = "workspace_change"
	TurnIntentOperation       TurnIntent = "operation"
)

func NormalizeTurnIntent(intent TurnIntent) TurnIntent {
	if intent == "" {
		return TurnIntentAnswer
	}
	return intent
}

func (i TurnIntent) Valid() bool {
	switch i {
	case TurnIntentAnswer, TurnIntentPlan, TurnIntentWorkspaceChange, TurnIntentOperation:
		return true
	default:
		return false
	}
}

type TurnOutcome string

const (
	TurnOutcomeAnswered TurnOutcome = "answered"
	TurnOutcomePlanned  TurnOutcome = "planned"
	TurnOutcomeChanged  TurnOutcome = "changed"
	TurnOutcomeOperated TurnOutcome = "operated"
)

func OutcomeForIntent(intent TurnIntent) TurnOutcome {
	switch NormalizeTurnIntent(intent) {
	case TurnIntentPlan:
		return TurnOutcomePlanned
	case TurnIntentWorkspaceChange:
		return TurnOutcomeChanged
	case TurnIntentOperation:
		return TurnOutcomeOperated
	default:
		return TurnOutcomeAnswered
	}
}

type OperationPayload interface {
	operationKind() OperationKind
	validate() error
	// references exposes the thread, turn, and item fields so callers can read
	// them uniformly and hosts can fill the ones a thin client left empty.
	references() (*ThreadID, *TurnID, *ItemID)
}

type StartTurnPayload struct {
	ThreadID          ThreadID                  `json:"thread_id"`
	TurnID            TurnID                    `json:"turn_id"`
	ItemID            ItemID                    `json:"item_id"`
	Prompt            string                    `json:"prompt"`
	DisplayPrompt     string                    `json:"display_prompt,omitempty"`
	Intent            TurnIntent                `json:"intent,omitempty"`
	Orchestration     *OrchestrationCorrelation `json:"orchestration,omitempty"`
	WorkspaceIdentity *WorkspaceIdentity        `json:"workspace_identity,omitempty"`
	Context           []EditorContextReference  `json:"context,omitempty"`
	Recovery          *TurnRecoveryContext      `json:"recovery,omitempty"`
	QueueID           string                    `json:"queue_id,omitempty"`
	// Idle marks extension/automation-initiated work. Plan mode rejects it (W6 / C4).
	Idle bool `json:"idle,omitempty"`
}

func (*StartTurnPayload) operationKind() OperationKind { return OperationStartTurn }

func (p *StartTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *StartTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.Prompt == "" {
		return errors.New("start turn prompt is required")
	}
	if !NormalizeTurnIntent(p.Intent).Valid() {
		return fmt.Errorf("start turn intent %q is invalid", p.Intent)
	}
	if p.Orchestration != nil {
		if err := p.Orchestration.Validate(); err != nil {
			return err
		}
	}
	if p.WorkspaceIdentity != nil {
		if err := p.WorkspaceIdentity.Validate(); err != nil {
			return err
		}
	}
	if p.Recovery != nil {
		if err := p.Recovery.Validate(); err != nil {
			return err
		}
	}
	return validateEditorContextReferences(p.Context, "start turn")
}

type CancelTurnPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
	Reason   string   `json:"reason,omitempty"`
}

// Well-known cancel reasons (F4). Hosts may pass free-form detail; NormalizeCancelReason
// maps empty/unknown values onto a stable default for audit events.
const (
	CancelReasonUserInterrupted  = "user_interrupted"
	CancelReasonHostInterrupted  = "host_interrupted"
	CancelReasonReplaced         = "replaced"
	CancelReasonShutdown         = "shutdown"
	CancelReasonInterrupted      = "interrupted"
	CancelReasonApprovalCanceled = "approval_canceled"
)

// NormalizeCancelReason returns a non-empty cancellation reason for TurnCanceledData.
func NormalizeCancelReason(reason string) string {
	trimmed := strings.TrimSpace(reason)
	if trimmed == "" {
		return CancelReasonInterrupted
	}
	return trimmed
}

func (*CancelTurnPayload) operationKind() OperationKind { return OperationCancelTurn }

func (p *CancelTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *CancelTurnPayload) validate() error {
	return validateReferences(p.ThreadID, p.TurnID, p.ItemID)
}

type SteerTurnPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
	Prompt   string   `json:"prompt"`
	QueueID  string   `json:"queue_id,omitempty"`
}

func (*SteerTurnPayload) operationKind() OperationKind { return OperationSteerTurn }

func (p *SteerTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *SteerTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.Prompt == "" {
		return errors.New("steering prompt is required")
	}
	return nil
}

type EnqueueTurnPayload struct {
	ThreadID          ThreadID                 `json:"thread_id"`
	TurnID            TurnID                   `json:"turn_id"`
	ItemID            ItemID                   `json:"item_id"`
	QueueID           string                   `json:"queue_id"`
	Prompt            string                   `json:"prompt"`
	DisplayPrompt     string                   `json:"display_prompt,omitempty"`
	Intent            TurnIntent               `json:"intent,omitempty"`
	WorkspaceIdentity *WorkspaceIdentity       `json:"workspace_identity,omitempty"`
	Context           []EditorContextReference `json:"context,omitempty"`
}

func (*EnqueueTurnPayload) operationKind() OperationKind { return OperationEnqueueTurn }

func (p *EnqueueTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *EnqueueTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if strings.TrimSpace(p.QueueID) == "" || strings.TrimSpace(p.Prompt) == "" {
		return errors.New("enqueue turn queue_id and prompt are required")
	}
	if !NormalizeTurnIntent(p.Intent).Valid() {
		return fmt.Errorf("enqueue turn intent %q is invalid", p.Intent)
	}
	if p.WorkspaceIdentity != nil {
		if err := p.WorkspaceIdentity.Validate(); err != nil {
			return err
		}
	}
	return validateEditorContextReferences(p.Context, "enqueue turn")
}

type UpdateQueuedTurnPayload struct {
	ThreadID      ThreadID `json:"thread_id"`
	TurnID        TurnID   `json:"turn_id"`
	ItemID        ItemID   `json:"item_id"`
	QueueID       string   `json:"queue_id"`
	Prompt        string   `json:"prompt"`
	DisplayPrompt string   `json:"display_prompt,omitempty"`
}

func (*UpdateQueuedTurnPayload) operationKind() OperationKind {
	return OperationUpdateQueuedTurn
}

func (p *UpdateQueuedTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *UpdateQueuedTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if strings.TrimSpace(p.QueueID) == "" || strings.TrimSpace(p.Prompt) == "" {
		return errors.New("queued turn update queue_id and prompt are required")
	}
	return nil
}

type RemoveQueuedTurnPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
	QueueID  string   `json:"queue_id"`
}

func (*RemoveQueuedTurnPayload) operationKind() OperationKind {
	return OperationRemoveQueuedTurn
}

func (p *RemoveQueuedTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *RemoveQueuedTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if strings.TrimSpace(p.QueueID) == "" {
		return errors.New("queued turn removal queue_id is required")
	}
	return nil
}

type PromoteQueuedTurnPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
	QueueID  string   `json:"queue_id"`
}

func (*PromoteQueuedTurnPayload) operationKind() OperationKind {
	return OperationPromoteQueuedTurn
}

func (p *PromoteQueuedTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *PromoteQueuedTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if strings.TrimSpace(p.QueueID) == "" {
		return errors.New("queued turn promotion queue_id is required")
	}
	return nil
}

type ApprovalDecisionPayload struct {
	ThreadID             ThreadID         `json:"thread_id"`
	TurnID               TurnID           `json:"turn_id"`
	ItemID               ItemID           `json:"item_id"`
	RequestID            string           `json:"request_id"`
	Decision             ApprovalDecision `json:"decision"`
	Scope                ApprovalScope    `json:"scope,omitempty"`
	ExpiresAt            time.Time        `json:"expires_at,omitempty"`
	ReplacementArguments json.RawMessage  `json:"replacement_arguments,omitempty"`
	PlanID               string           `json:"plan_id,omitempty"`
}

func (*ApprovalDecisionPayload) operationKind() OperationKind { return OperationApprovalDecision }

func (p *ApprovalDecisionPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *ApprovalDecisionPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.Decision != ApprovalApprove && p.Decision != ApprovalDeny && p.Decision != ApprovalCancel {
		return errors.New("approval decision must be approve, deny, or cancel")
	}
	if p.RequestID == "" {
		return errors.New("approval request_id is required")
	}
	if p.Scope != "" && p.Scope != ApprovalScopeOnce && p.Scope != ApprovalScopeSession &&
		p.Scope != ApprovalScopeAlways {
		return errors.New("approval scope must be once, session, or always")
	}
	if len(p.ReplacementArguments) != 0 {
		var value map[string]any
		if err := decodeStrict(p.ReplacementArguments, &value); err != nil {
			return fmt.Errorf("replacement arguments: %w", err)
		}
	}
	if p.PlanID != "" && !validSHA256(p.PlanID) {
		return errors.New("approval plan_id must be a lowercase SHA-256")
	}
	return nil
}

type InputReplyPayload struct {
	ThreadID  ThreadID          `json:"thread_id"`
	TurnID    TurnID            `json:"turn_id"`
	ItemID    ItemID            `json:"item_id"`
	RequestID string            `json:"request_id"`
	Answer    string            `json:"answer"`
	Values    map[string]string `json:"values,omitempty"`
}

func (*InputReplyPayload) operationKind() OperationKind { return OperationInputReply }

func (p *InputReplyPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *InputReplyPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.RequestID == "" {
		return errors.New("input request_id is required")
	}
	if strings.TrimSpace(p.Answer) == "" && len(p.Values) == 0 {
		return errors.New("input answer or values are required")
	}
	return nil
}

type CompactThreadPayload struct {
	ThreadID ThreadID `json:"thread_id"`
	TurnID   TurnID   `json:"turn_id"`
	ItemID   ItemID   `json:"item_id"`
}

func (*CompactThreadPayload) operationKind() OperationKind { return OperationCompactThread }

func (p *CompactThreadPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *CompactThreadPayload) validate() error {
	return validateReferences(p.ThreadID, p.TurnID, p.ItemID)
}

type ForkThreadPayload struct {
	ThreadID    ThreadID `json:"thread_id"`
	TurnID      TurnID   `json:"turn_id"`
	ItemID      ItemID   `json:"item_id"`
	NewThreadID ThreadID `json:"new_thread_id"`
}

func (*ForkThreadPayload) operationKind() OperationKind { return OperationForkThread }

func (p *ForkThreadPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *ForkThreadPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.NewThreadID == "" || p.NewThreadID == p.ThreadID {
		return errors.New("fork new_thread_id must be non-empty and different")
	}
	return nil
}

type RevertTurnPayload struct {
	ThreadID     ThreadID `json:"thread_id"`
	TurnID       TurnID   `json:"turn_id"`
	ItemID       ItemID   `json:"item_id"`
	TargetTurnID TurnID   `json:"target_turn_id"`
}

func (*RevertTurnPayload) operationKind() OperationKind { return OperationRevertTurn }

func (p *RevertTurnPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *RevertTurnPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.TargetTurnID == "" {
		return errors.New("revert target_turn_id is required")
	}
	return nil
}

type RunNodeSpec struct {
	ID              NodeID   `json:"id"`
	Kind            string   `json:"kind"`
	Dependencies    []NodeID `json:"dependencies,omitempty"`
	AuthorityDigest string   `json:"authority_digest,omitempty"`
}

type SubmitRunPayload struct {
	ThreadID        ThreadID      `json:"thread_id"`
	TurnID          TurnID        `json:"turn_id"`
	ItemID          ItemID        `json:"item_id"`
	RunID           RunID         `json:"run_id"`
	Kind            string        `json:"kind"`
	Source          string        `json:"source"`
	SessionID       string        `json:"session_id"`
	Workspace       string        `json:"workspace,omitempty"`
	RootThreadID    ThreadID      `json:"root_thread_id"`
	AuthorityDigest string        `json:"authority_digest,omitempty"`
	Nodes           []RunNodeSpec `json:"nodes"`
}

func (*SubmitRunPayload) operationKind() OperationKind { return OperationSubmitRun }

func (p *SubmitRunPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *SubmitRunPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.RunID == "" || strings.TrimSpace(p.Kind) == "" ||
		strings.TrimSpace(p.Source) == "" || strings.TrimSpace(p.SessionID) == "" ||
		p.RootThreadID == "" || len(p.Nodes) == 0 {
		return errors.New(
			"run submit requires run_id, kind, source, session_id, root_thread_id, and nodes",
		)
	}
	if p.AuthorityDigest != "" && !validSHA256(p.AuthorityDigest) {
		return errors.New("run submit authority_digest must be a lowercase SHA-256")
	}
	seen := make(map[NodeID]bool, len(p.Nodes))
	for _, node := range p.Nodes {
		if node.ID == "" || strings.TrimSpace(node.Kind) == "" || seen[node.ID] {
			return errors.New("run submit node id and kind must be unique")
		}
		if node.AuthorityDigest != "" && !validSHA256(node.AuthorityDigest) {
			return errors.New("run submit node authority_digest must be a lowercase SHA-256")
		}
		seen[node.ID] = true
	}
	return nil
}

type CancelRunPayload struct {
	ThreadID         ThreadID `json:"thread_id"`
	TurnID           TurnID   `json:"turn_id"`
	ItemID           ItemID   `json:"item_id"`
	RunID            RunID    `json:"run_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
	Reason           string   `json:"reason"`
}

func (*CancelRunPayload) operationKind() OperationKind { return OperationCancelRun }

func (p *CancelRunPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *CancelRunPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.RunID == "" || p.ExpectedRevision == 0 ||
		strings.TrimSpace(p.Reason) == "" {
		return errors.New("run cancel requires run_id, expected_revision, and reason")
	}
	return nil
}

type ResumeRunPayload struct {
	ThreadID         ThreadID `json:"thread_id"`
	TurnID           TurnID   `json:"turn_id"`
	ItemID           ItemID   `json:"item_id"`
	RunID            RunID    `json:"run_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
}

func (*ResumeRunPayload) operationKind() OperationKind { return OperationResumeRun }

func (p *ResumeRunPayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *ResumeRunPayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.RunID == "" || p.ExpectedRevision == 0 {
		return errors.New("run resume requires run_id and expected_revision")
	}
	return nil
}

type RetryNodePayload struct {
	ThreadID         ThreadID `json:"thread_id"`
	TurnID           TurnID   `json:"turn_id"`
	ItemID           ItemID   `json:"item_id"`
	RunID            RunID    `json:"run_id"`
	NodeID           NodeID   `json:"node_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
}

func (*RetryNodePayload) operationKind() OperationKind { return OperationRetryNode }

func (p *RetryNodePayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *RetryNodePayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.RunID == "" || p.NodeID == "" || p.ExpectedRevision == 0 {
		return errors.New("node retry requires run_id, node_id, and expected_revision")
	}
	return nil
}

type SkipNodePayload struct {
	ThreadID         ThreadID `json:"thread_id"`
	TurnID           TurnID   `json:"turn_id"`
	ItemID           ItemID   `json:"item_id"`
	RunID            RunID    `json:"run_id"`
	NodeID           NodeID   `json:"node_id"`
	ExpectedRevision uint64   `json:"expected_revision"`
	Reason           string   `json:"reason"`
}

func (*SkipNodePayload) operationKind() OperationKind { return OperationSkipNode }

func (p *SkipNodePayload) references() (*ThreadID, *TurnID, *ItemID) {
	return &p.ThreadID, &p.TurnID, &p.ItemID
}

func (p *SkipNodePayload) validate() error {
	if err := validateReferences(p.ThreadID, p.TurnID, p.ItemID); err != nil {
		return err
	}
	if p.RunID == "" || p.NodeID == "" || p.ExpectedRevision == 0 ||
		strings.TrimSpace(p.Reason) == "" {
		return errors.New(
			"node skip requires run_id, node_id, expected_revision, and reason",
		)
	}
	return nil
}

type Operation struct {
	Version   int              `json:"version"`
	ID        OperationID      `json:"id"`
	Kind      OperationKind    `json:"kind"`
	CreatedAt time.Time        `json:"created_at"`
	Payload   OperationPayload `json:"payload"`
}

func NewOperation(payload OperationPayload) (Operation, error) {
	if payload == nil {
		return Operation{}, errors.New("operation payload is required")
	}
	id, err := newID("op")
	if err != nil {
		return Operation{}, err
	}
	operation := Operation{
		Version:   Version,
		ID:        OperationID(id),
		Kind:      payload.operationKind(),
		CreatedAt: time.Now().UTC(),
		Payload:   payload,
	}
	return operation, operation.Validate()
}

// FillOperationReferences fills only the empty references, so a reference the
// client did supply always wins over the host default.
func FillOperationReferences(
	payload OperationPayload,
	thread ThreadID,
	turn TurnID,
	item ItemID,
) {
	if payload == nil {
		return
	}
	threadRef, turnRef, itemRef := payload.references()
	if *threadRef == "" {
		*threadRef = thread
	}
	if *turnRef == "" {
		*turnRef = turn
	}
	if *itemRef == "" {
		*itemRef = item
	}
}

func OperationReferences(operation Operation) (ThreadID, TurnID, ItemID) {
	return PayloadReferences(operation.Payload)
}

// PayloadReferences reads the references of a payload that is not wrapped in an
// Operation yet, which is how a host inspects what a client did supply before
// filling the rest.
func PayloadReferences(payload OperationPayload) (ThreadID, TurnID, ItemID) {
	if payload == nil {
		return "", "", ""
	}
	thread, turn, item := payload.references()
	return *thread, *turn, *item
}

func validateReferences(threadID ThreadID, turnID TurnID, itemID ItemID) error {
	if threadID == "" || turnID == "" || itemID == "" {
		return errors.New("thread_id, turn_id, and item_id are required")
	}
	return nil
}
