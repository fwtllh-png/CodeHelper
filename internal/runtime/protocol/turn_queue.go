package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const TurnQueueVersion = 1

type QueuedTurn struct {
	QueueID           string                   `json:"queue_id"`
	ThreadID          ThreadID                 `json:"thread_id"`
	SourceTurnID      TurnID                   `json:"source_turn_id"`
	Prompt            string                   `json:"prompt"`
	DisplayPrompt     string                   `json:"display_prompt,omitempty"`
	Intent            TurnIntent               `json:"intent,omitempty"`
	WorkspaceIdentity *WorkspaceIdentity       `json:"workspace_identity,omitempty"`
	Context           []EditorContextReference `json:"context,omitempty"`
	AddedSequence     Cursor                   `json:"added_sequence"`
	CreatedAt         time.Time                `json:"created_at"`
	UpdatedAt         time.Time                `json:"updated_at"`
}

func (q QueuedTurn) Validate() error {
	if strings.TrimSpace(q.QueueID) == "" || q.ThreadID == "" ||
		q.SourceTurnID == "" || strings.TrimSpace(q.Prompt) == "" ||
		q.AddedSequence == 0 || q.CreatedAt.IsZero() || q.UpdatedAt.IsZero() {
		return errors.New("queued turn identity, prompt, sequence, and timestamps are required")
	}
	if !NormalizeTurnIntent(q.Intent).Valid() {
		return fmt.Errorf("queued turn intent %q is invalid", q.Intent)
	}
	if q.WorkspaceIdentity != nil {
		if err := q.WorkspaceIdentity.Validate(); err != nil {
			return err
		}
	}
	return validateEditorContextReferences(q.Context, "queued turn")
}

type TurnQueue struct {
	Version int          `json:"version"`
	Items   []QueuedTurn `json:"items"`
}

func (q TurnQueue) Validate() error {
	if q.Version != TurnQueueVersion {
		return errors.New("unsupported turn queue version")
	}
	seen := make(map[string]struct{}, len(q.Items))
	var previous Cursor
	for _, item := range q.Items {
		if err := item.Validate(); err != nil {
			return err
		}
		if _, exists := seen[item.QueueID]; exists {
			return errors.New("turn queue contains a duplicate queue_id")
		}
		if previous != 0 && item.AddedSequence <= previous {
			return errors.New("turn queue is not ordered by added_sequence")
		}
		seen[item.QueueID] = struct{}{}
		previous = item.AddedSequence
	}
	return nil
}

type TurnQueuedData struct {
	QueueID           string                   `json:"queue_id"`
	Prompt            string                   `json:"prompt"`
	DisplayPrompt     string                   `json:"display_prompt,omitempty"`
	Intent            TurnIntent               `json:"intent,omitempty"`
	WorkspaceIdentity *WorkspaceIdentity       `json:"workspace_identity,omitempty"`
	Context           []EditorContextReference `json:"context,omitempty"`
}

func (*TurnQueuedData) eventKind() EventKind { return EventTurnQueued }

func (d *TurnQueuedData) validate() error {
	payload := EnqueueTurnPayload{
		ThreadID:          "thread",
		TurnID:            "turn",
		ItemID:            "item",
		QueueID:           d.QueueID,
		Prompt:            d.Prompt,
		DisplayPrompt:     d.DisplayPrompt,
		Intent:            d.Intent,
		WorkspaceIdentity: d.WorkspaceIdentity,
		Context:           d.Context,
	}
	return payload.validate()
}

type QueuedTurnUpdatedData struct {
	QueueID       string `json:"queue_id"`
	Prompt        string `json:"prompt"`
	DisplayPrompt string `json:"display_prompt,omitempty"`
}

func (*QueuedTurnUpdatedData) eventKind() EventKind { return EventQueuedTurnUpdated }

func (d *QueuedTurnUpdatedData) validate() error {
	if strings.TrimSpace(d.QueueID) == "" || strings.TrimSpace(d.Prompt) == "" {
		return errors.New("queued turn update queue_id and prompt are required")
	}
	return nil
}

type QueuedTurnRemovedData struct {
	QueueID string `json:"queue_id"`
	Reason  string `json:"reason"`
}

func (*QueuedTurnRemovedData) eventKind() EventKind { return EventQueuedTurnRemoved }

func (d *QueuedTurnRemovedData) validate() error {
	if strings.TrimSpace(d.QueueID) == "" {
		return errors.New("queued turn removal queue_id is required")
	}
	switch d.Reason {
	case "user", "promoted":
		return nil
	default:
		return errors.New("queued turn removal reason is invalid")
	}
}
