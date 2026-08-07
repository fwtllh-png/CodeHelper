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
	OperationStartTurn        OperationKind = "turn.start"
	OperationCancelTurn       OperationKind = "turn.cancel"
	OperationSteerTurn        OperationKind = "turn.steer"
	OperationApprovalDecision OperationKind = "approval.decision"
	OperationInputReply       OperationKind = "input.reply"
	OperationCompactThread    OperationKind = "thread.compact"
	OperationForkThread       OperationKind = "thread.fork"
	OperationRevertTurn       OperationKind = "turn.revert"
)

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

type OperationPayload interface {
	operationKind() OperationKind
	validate() error
	// references exposes the thread, turn, and item fields so callers can read
	// them uniformly and hosts can fill the ones a thin client left empty.
	references() (*ThreadID, *TurnID, *ItemID)
}

type StartTurnPayload struct {
	ThreadID          ThreadID                 `json:"thread_id"`
	TurnID            TurnID                   `json:"turn_id"`
	ItemID            ItemID                   `json:"item_id"`
	Prompt            string                   `json:"prompt"`
	WorkspaceIdentity *WorkspaceIdentity       `json:"workspace_identity,omitempty"`
	Context           []EditorContextReference `json:"context,omitempty"`
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
	if p.WorkspaceIdentity != nil {
		if err := p.WorkspaceIdentity.Validate(); err != nil {
			return err
		}
	}
	if len(p.Context) > 8 {
		return errors.New("start turn accepts at most 8 editor context references")
	}
	for _, reference := range p.Context {
		if err := reference.validate(); err != nil {
			return err
		}
	}
	return nil
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
