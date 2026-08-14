package protocol

import (
	"errors"
	"strings"
)

const WorkflowIntentVersion = 1

type TurnRecoveryAction string

const (
	TurnRecoveryRetry    TurnRecoveryAction = "retry"
	TurnRecoveryContinue TurnRecoveryAction = "continue"
)

// TurnRecoveryContext binds a newly-created recovery Turn to the terminal Turn
// whose request or retained workspace draft it is recovering.
type TurnRecoveryContext struct {
	Action       TurnRecoveryAction `json:"action"`
	SourceTurnID TurnID             `json:"source_turn_id"`
}

func (c TurnRecoveryContext) Validate() error {
	if !validProfileIdentifier(string(c.SourceTurnID)) {
		return errors.New("turn recovery source is invalid")
	}
	switch c.Action {
	case TurnRecoveryRetry, TurnRecoveryContinue:
		return nil
	default:
		return errors.New("turn recovery action is invalid")
	}
}

// TurnRecoveryRequest always creates a new Turn. Retry reuses the source Turn's
// user request and safe model-visible context; Continue uses the terminal
// history plus Guidance. Neither action replays historical Tool operations.
type TurnRecoveryRequest struct {
	Version        int                `json:"version"`
	Action         TurnRecoveryAction `json:"action"`
	SessionID      string             `json:"session_id"`
	SourceTurnID   TurnID             `json:"source_turn_id"`
	Guidance       string             `json:"guidance,omitempty"`
	IdempotencyKey string             `json:"idempotency_key"`
}

func (r TurnRecoveryRequest) Validate() error {
	if r.Version != WorkflowIntentVersion ||
		!validProfileIdentifier(r.SessionID) ||
		!validProfileIdentifier(string(r.SourceTurnID)) ||
		!validProfileIdentifier(r.IdempotencyKey) ||
		len(r.Guidance) > 64<<10 ||
		strings.ContainsRune(r.Guidance, '\x00') {
		return errors.New("turn recovery request is invalid")
	}
	switch r.Action {
	case TurnRecoveryRetry:
		if strings.TrimSpace(r.Guidance) != "" {
			return errors.New("retry cannot replace the source user request")
		}
	case TurnRecoveryContinue:
	default:
		return errors.New("turn recovery action is invalid")
	}
	return nil
}

type PlanDestination string

const (
	PlanDestinationCurrentSession PlanDestination = "current_session"
	PlanDestinationNewSession     PlanDestination = "new_session"
	PlanDestinationCheckpointFork PlanDestination = "checkpoint_fork"
)

type PlanTransitionRequest struct {
	Version        int             `json:"version"`
	SessionID      string          `json:"session_id"`
	PlanID         string          `json:"plan_id"`
	Transition     PlanTransition  `json:"transition"`
	Destination    PlanDestination `json:"destination"`
	CheckpointID   string          `json:"checkpoint_id,omitempty"`
	Title          string          `json:"title,omitempty"`
	IdempotencyKey string          `json:"idempotency_key"`
}

func (r PlanTransitionRequest) Validate() error {
	if r.Version != WorkflowIntentVersion ||
		!validProfileIdentifier(r.SessionID) ||
		!validProfileIdentifier(r.PlanID) ||
		!validProfileIdentifier(r.IdempotencyKey) ||
		len(r.Title) > 256 ||
		strings.ContainsAny(r.Title, "\x00\r\n") {
		return errors.New("plan transition request is invalid")
	}
	switch r.Transition {
	case PlanTransitionImplement, PlanTransitionAutopilot:
	default:
		return errors.New("plan transition is invalid")
	}
	switch r.Destination {
	case PlanDestinationCurrentSession:
		if r.CheckpointID != "" {
			return errors.New("current Session transition cannot name a Checkpoint")
		}
	case PlanDestinationNewSession:
		if r.CheckpointID != "" {
			return errors.New("new Session transition cannot name a Checkpoint")
		}
	case PlanDestinationCheckpointFork:
		if !validProfileIdentifier(r.CheckpointID) {
			return errors.New("Checkpoint Fork transition requires a Checkpoint")
		}
	default:
		return errors.New("plan destination is invalid")
	}
	return nil
}
