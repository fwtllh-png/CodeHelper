package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var ErrOperationConflict = protocol.NewProblem(
	protocol.CodeConflict,
	"operation identity was reused with a different payload",
	false,
	nil,
)

// Acceptance is the durable result of accepting an operation. Duplicate
// operations are deliberately not dispatched to Engine.
type Acceptance struct {
	OperationID protocol.OperationID
	Duplicate   bool
	Committed   bool
}

type PendingApproval struct {
	RequestID string
	ThreadID  protocol.ThreadID
	TurnID    protocol.TurnID
	ItemID    protocol.ItemID
	Data      protocol.ApprovalRequiredData
}

type PendingInput struct {
	RequestID string
	ThreadID  protocol.ThreadID
	TurnID    protocol.TurnID
	ItemID    protocol.ItemID
	Data      protocol.InputRequiredData
}

type PendingOperation struct {
	ID             protocol.OperationID
	IdempotencyKey string
	Canonical      json.RawMessage
}

// RecoveryState is explicit input to Runtime's state machine. It contains only
// state required to continue allocating events and reject unsafe replays.
type RecoveryState struct {
	LastSequence      protocol.Cursor
	Terminals         map[protocol.TurnID]protocol.EventKind
	PendingApprovals  map[string]PendingApproval
	PendingInputs     map[string]PendingInput
	PendingOperations map[protocol.OperationID]PendingOperation
	// ToolItems remaps call_id → ItemID so post-recovery tool.result events
	// keep a stable item identity (F5).
	ToolItems map[string]protocol.ItemID
}

type CommitReceipt struct {
	OperationID  protocol.OperationID `json:"operation_id"`
	Status       string               `json:"status"`
	LastSequence protocol.Cursor      `json:"last_sequence"`
	CompletedAt  time.Time            `json:"completed_at"`
}

// DurableLifecycle persists operation acceptance and relational projections.
// Runtime remains the sole state machine; implementations only enforce durable
// compare-and-set transitions and rebuild recovery input.
type DurableLifecycle interface {
	Recover(context.Context) (RecoveryState, error)
	Accept(context.Context, protocol.Operation, string, json.RawMessage) (Acceptance, error)
	Project(context.Context, protocol.Event) error
	Commit(context.Context, CommitReceipt) error
}

func CanonicalOperationPayload(operation protocol.Operation) (json.RawMessage, error) {
	raw, err := json.Marshal(struct {
		Kind    protocol.OperationKind    `json:"kind"`
		Payload protocol.OperationPayload `json:"payload"`
	}{Kind: operation.Kind, Payload: operation.Payload})
	if err != nil {
		return nil, fmt.Errorf("encode operation payload: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("canonicalize operation payload: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalize operation payload: %w", err)
	}
	return canonical, nil
}
