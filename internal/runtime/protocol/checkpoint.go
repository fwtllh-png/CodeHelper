package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const CheckpointProtocolVersion = 1

type CheckpointStatus string

const (
	CheckpointCompleted   CheckpointStatus = "completed"
	CheckpointInterrupted CheckpointStatus = "interrupted"
)

type SessionCheckpoint struct {
	Version             int               `json:"version"`
	ID                  string            `json:"id"`
	SessionID           string            `json:"session_id"`
	ThreadID            ThreadID          `json:"thread_id"`
	TurnID              TurnID            `json:"turn_id"`
	Cursor              Cursor            `json:"cursor"`
	Status              CheckpointStatus  `json:"status"`
	Summary             string            `json:"summary"`
	ProfileRevision     uint64            `json:"profile_revision"`
	ParentCheckpointID  string            `json:"parent_checkpoint_id,omitempty"`
	ChangeReceipt       *ReceiptReference `json:"change_receipt,omitempty"`
	ChangedFiles        int               `json:"changed_files"`
	ExternalSideEffects bool              `json:"external_side_effects"`
	SideEffectNote      string            `json:"side_effect_note,omitempty"`
	CanRestore          bool              `json:"can_restore"`
	CanFork             bool              `json:"can_fork"`
	CreatedAt           time.Time         `json:"created_at"`
}

type ReceiptReference struct {
	EventID EventID `json:"event_id"`
	TurnID  TurnID  `json:"turn_id"`
	Cursor  Cursor  `json:"cursor"`
}

func (r ReceiptReference) Validate() error {
	if !validProfileIdentifier(string(r.EventID)) ||
		!validProfileIdentifier(string(r.TurnID)) ||
		r.Cursor == 0 {
		return errors.New("receipt reference is invalid")
	}
	return nil
}

func (c SessionCheckpoint) Validate() error {
	if c.Version != CheckpointProtocolVersion ||
		!validProfileIdentifier(c.ID) ||
		!validProfileIdentifier(c.SessionID) ||
		!validProfileIdentifier(string(c.ThreadID)) ||
		!validProfileIdentifier(string(c.TurnID)) ||
		c.ProfileRevision == 0 ||
		c.CreatedAt.IsZero() {
		return errors.New("session checkpoint identity is invalid")
	}
	if c.Status != CheckpointCompleted && c.Status != CheckpointInterrupted {
		return fmt.Errorf("session checkpoint status %q is invalid", c.Status)
	}
	if strings.TrimSpace(c.Summary) == "" || len(c.Summary) > 2048 ||
		strings.ContainsRune(c.Summary, '\x00') ||
		c.ChangedFiles < 0 ||
		len(c.SideEffectNote) > 2048 ||
		strings.ContainsRune(c.SideEffectNote, '\x00') {
		return errors.New("session checkpoint projection is invalid")
	}
	if c.ParentCheckpointID != "" &&
		!validProfileIdentifier(c.ParentCheckpointID) {
		return errors.New("session checkpoint parent identity is invalid")
	}
	if c.ChangeReceipt != nil {
		if err := c.ChangeReceipt.Validate(); err != nil {
			return err
		}
		if c.ChangeReceipt.TurnID != c.TurnID ||
			c.ChangeReceipt.Cursor > c.Cursor {
			return errors.New("session checkpoint receipt identity is inconsistent")
		}
	}
	return nil
}

type CheckpointList struct {
	Version     int                 `json:"version"`
	SessionID   string              `json:"session_id"`
	Checkpoints []SessionCheckpoint `json:"checkpoints"`
}

func (l CheckpointList) Validate() error {
	if l.Version != CheckpointProtocolVersion ||
		!validProfileIdentifier(l.SessionID) ||
		len(l.Checkpoints) > 1000 {
		return errors.New("checkpoint list is invalid")
	}
	seen := make(map[string]struct{}, len(l.Checkpoints))
	for _, checkpoint := range l.Checkpoints {
		if err := checkpoint.Validate(); err != nil {
			return err
		}
		if checkpoint.SessionID != l.SessionID {
			return errors.New("checkpoint list crosses Session identity")
		}
		if _, duplicate := seen[checkpoint.ID]; duplicate {
			return fmt.Errorf("checkpoint %q is duplicated", checkpoint.ID)
		}
		seen[checkpoint.ID] = struct{}{}
	}
	return nil
}

type CheckpointRestoreResult struct {
	Version             int               `json:"version"`
	Checkpoint          SessionCheckpoint `json:"checkpoint"`
	ThreadID            ThreadID          `json:"thread_id"`
	RestoredCursor      Cursor            `json:"restored_cursor"`
	SideEffectsReplayed bool              `json:"side_effects_replayed"`
}

type CheckpointForkResult struct {
	Version    int               `json:"version"`
	Checkpoint SessionCheckpoint `json:"checkpoint"`
	SessionID  string            `json:"session_id"`
	ThreadID   ThreadID          `json:"thread_id"`
	ParentID   ThreadID          `json:"parent_thread_id"`
}

type PlanArtifactStatus string
type PlanTransition string

const (
	PlanArtifactReady PlanArtifactStatus = "ready"

	PlanTransitionImplement PlanTransition = "implement"
	PlanTransitionAutopilot PlanTransition = "autopilot"
)

type SessionPlanArtifact struct {
	Version         int                `json:"version"`
	ID              string             `json:"id"`
	SessionID       string             `json:"session_id"`
	ThreadID        ThreadID           `json:"thread_id"`
	TurnID          TurnID             `json:"turn_id"`
	Cursor          Cursor             `json:"cursor"`
	Status          PlanArtifactStatus `json:"status"`
	Body            string             `json:"body"`
	ProfileRevision uint64             `json:"profile_revision"`
	CanImplement    bool               `json:"can_implement"`
	CanAutopilot    bool               `json:"can_autopilot"`
	CreatedAt       time.Time          `json:"created_at"`
}

func (p SessionPlanArtifact) Validate() error {
	if p.Version != CheckpointProtocolVersion ||
		!validProfileIdentifier(p.ID) ||
		!validProfileIdentifier(p.SessionID) ||
		!validProfileIdentifier(string(p.ThreadID)) ||
		!validProfileIdentifier(string(p.TurnID)) ||
		p.ProfileRevision == 0 ||
		p.CreatedAt.IsZero() {
		return errors.New("Session Plan Artifact identity is invalid")
	}
	if p.Status != PlanArtifactReady ||
		strings.TrimSpace(p.Body) == "" ||
		len(p.Body) > 64<<10 ||
		strings.ContainsRune(p.Body, '\x00') {
		return errors.New("Session Plan Artifact body or status is invalid")
	}
	if !p.CanImplement && !p.CanAutopilot {
		return errors.New("Session Plan Artifact has no transition")
	}
	return nil
}

type SessionPlanSnapshot struct {
	Version  int                  `json:"version"`
	Artifact *SessionPlanArtifact `json:"artifact,omitempty"`
}
