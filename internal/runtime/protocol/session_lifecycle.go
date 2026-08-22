package protocol

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const SessionLifecycleVersion = 1

type SessionCreateSeed struct {
	Version        int      `json:"version"`
	SessionID      string   `json:"session_id"`
	WorkspaceID    string   `json:"workspace_id"`
	WorkspaceRoot  string   `json:"workspace_root"`
	WorkspaceLabel string   `json:"workspace_label"`
	ThreadID       ThreadID `json:"thread_id"`
	Title          string   `json:"title"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	Isolation      string   `json:"isolation"`
}

func (s SessionCreateSeed) Validate() error {
	if s.Version != SessionLifecycleVersion ||
		!validProfileIdentifier(s.SessionID) ||
		!validProfileIdentifier(s.WorkspaceID) ||
		s.ThreadID == "" || len(s.ThreadID) > 256 ||
		strings.TrimSpace(s.WorkspaceRoot) == "" ||
		strings.TrimSpace(s.Title) == "" || len(s.Title) > 256 ||
		strings.ContainsAny(s.Title, "\x00\r\n") ||
		!validProfileIdentifier(s.Provider) ||
		!validProfileIdentifier(s.Model) {
		return errors.New("session create seed is invalid")
	}
	if s.Isolation != "shared" && s.Isolation != "worktree" {
		return errors.New("session create isolation is invalid")
	}
	return nil
}

type SessionLifecycleStatus string

const (
	SessionStatusIdle             SessionLifecycleStatus = "idle"
	SessionStatusRunning          SessionLifecycleStatus = "running"
	SessionStatusAwaitingApproval SessionLifecycleStatus = "awaiting_approval"
	SessionStatusAwaitingInput    SessionLifecycleStatus = "awaiting_input"
	SessionStatusCompleted        SessionLifecycleStatus = "completed"
	SessionStatusFailed           SessionLifecycleStatus = "failed"
	SessionStatusInterrupted      SessionLifecycleStatus = "interrupted"
)

type SessionSummary struct {
	Version          int                    `json:"version"`
	Revision         uint64                 `json:"revision"`
	SessionID        string                 `json:"session_id"`
	ThreadID         ThreadID               `json:"thread_id"`
	Title            string                 `json:"title"`
	Status           SessionLifecycleStatus `json:"status"`
	Pinned           bool                   `json:"pinned"`
	Archived         bool                   `json:"archived"`
	Isolation        string                 `json:"isolation"`
	WorkspaceRoot    string                 `json:"workspace_root"`
	WorkspaceLabel   string                 `json:"workspace_label"`
	Provider         string                 `json:"provider,omitempty"`
	Model            string                 `json:"model,omitempty"`
	Mode             string                 `json:"mode,omitempty"`
	ExecutionTarget  string                 `json:"execution_target"`
	ParentThreadID   ThreadID               `json:"parent_thread_id,omitempty"`
	LatestTurnID     TurnID                 `json:"latest_turn_id,omitempty"`
	LatestSequence   Cursor                 `json:"latest_sequence"`
	PendingApprovals int                    `json:"pending_approvals"`
	PendingInputs    int                    `json:"pending_inputs"`
	CheckpointCount  int                    `json:"checkpoint_count"`
	ChangedFiles     int                    `json:"changed_files"`
	TotalTokens      uint64                 `json:"total_tokens"`
	CostMicrounits   uint64                 `json:"cost_microunits"`
	CostKnown        bool                   `json:"cost_known"`
	CreatedAt        time.Time              `json:"created_at"`
	UpdatedAt        time.Time              `json:"updated_at"`
}

// SessionReadFence binds revisioned lifecycle state to one durable event
// watermark. Repositories must populate all fields from one read transaction.
type SessionReadFence struct {
	Session         SessionSummary `json:"session"`
	ThreadIDs       []ThreadID     `json:"thread_ids"`
	ThroughSequence Cursor         `json:"through_sequence"`
}

func (s SessionSummary) Validate() error {
	if s.Version != SessionLifecycleVersion || s.Revision == 0 {
		return errors.New("session summary version and revision are invalid")
	}
	if !validProfileIdentifier(s.SessionID) ||
		!validProfileIdentifier(string(s.ThreadID)) {
		return errors.New("session summary identity is invalid")
	}
	if strings.TrimSpace(s.Title) == "" || len(s.Title) > 256 ||
		strings.ContainsAny(s.Title, "\x00\r\n") {
		return errors.New("session summary title is invalid")
	}
	if !validSessionLifecycleStatus(s.Status) {
		return fmt.Errorf("session summary status %q is invalid", s.Status)
	}
	if s.Isolation != "shared" && s.Isolation != "worktree" {
		return errors.New("session summary isolation is invalid")
	}
	if s.WorkspaceRoot == "" || s.WorkspaceLabel == "" ||
		s.CreatedAt.IsZero() || s.UpdatedAt.IsZero() ||
		s.PendingApprovals < 0 || s.PendingInputs < 0 ||
		s.CheckpointCount < 0 {
		return errors.New("session summary projection is incomplete")
	}
	if s.ChangedFiles < 0 {
		return errors.New("session summary changed files is invalid")
	}
	if s.ExecutionTarget != "local" {
		return errors.New("session summary execution target must be local")
	}
	if s.UpdatedAt.Before(s.CreatedAt) ||
		len(s.WorkspaceRoot) > 4096 ||
		len(s.WorkspaceLabel) > 256 ||
		strings.ContainsRune(s.WorkspaceRoot, '\x00') ||
		strings.ContainsRune(s.WorkspaceLabel, '\x00') {
		return errors.New("session summary workspace or time is invalid")
	}
	for name, value := range map[string]string{
		"provider": string(s.Provider),
		"model":    string(s.Model),
		"mode":     string(s.Mode),
	} {
		if len(value) > 256 || strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("session summary %s is invalid", name)
		}
	}
	for name, value := range map[string]string{
		"parent thread": string(s.ParentThreadID),
		"latest turn":   string(s.LatestTurnID),
	} {
		if value != "" && !validProfileIdentifier(value) {
			return fmt.Errorf("session summary %s identity is invalid", name)
		}
	}
	return nil
}

type SessionSearchMatch struct {
	SessionID string `json:"session_id"`
	TurnID    TurnID `json:"turn_id"`
	Kind      string `json:"kind"`
	Snippet   string `json:"snippet,omitempty"`
}

func (m SessionSearchMatch) Validate() error {
	if !validProfileIdentifier(m.SessionID) ||
		!validProfileIdentifier(string(m.TurnID)) {
		return errors.New("session search match identity is invalid")
	}
	switch m.Kind {
	case "title", "user_request", "agent_output", "path", "symbol", "content":
	default:
		return errors.New("session search match kind is invalid")
	}
	if len(m.Snippet) > 2048 || strings.ContainsRune(m.Snippet, '\x00') {
		return errors.New("session search match snippet is invalid")
	}
	return nil
}

type SessionList struct {
	Version  int                  `json:"version"`
	Query    string               `json:"query,omitempty"`
	Sessions []SessionSummary     `json:"sessions"`
	Matches  []SessionSearchMatch `json:"matches,omitempty"`
}

type SessionListQuery struct {
	WorkspaceRoot   string                 `json:"workspace_root,omitempty"`
	Query           string                 `json:"query,omitempty"`
	IncludeArchived bool                   `json:"include_archived,omitempty"`
	PinnedOnly      bool                   `json:"pinned_only,omitempty"`
	Status          SessionLifecycleStatus `json:"status,omitempty"`
	Limit           int                    `json:"limit,omitempty"`
}

func (q SessionListQuery) Validate() error {
	if len(q.Query) > 256 || strings.ContainsRune(q.Query, '\x00') ||
		q.Limit < 0 || q.Limit > 1000 {
		return errors.New("session list query is invalid")
	}
	if q.Status != "" && !validSessionLifecycleStatus(q.Status) {
		return errors.New("session list status is invalid")
	}
	return nil
}

func (l SessionList) Validate() error {
	if l.Version != SessionLifecycleVersion || len(l.Query) > 256 ||
		strings.ContainsRune(l.Query, '\x00') || len(l.Sessions) > 1000 {
		return errors.New("session list is invalid")
	}
	if strings.TrimSpace(l.Query) == "" && len(l.Matches) != 0 {
		return errors.New("session list without a query cannot contain matches")
	}
	seen := make(map[string]struct{}, len(l.Sessions))
	for _, session := range l.Sessions {
		if err := session.Validate(); err != nil {
			return err
		}
		if _, duplicate := seen[session.SessionID]; duplicate {
			return fmt.Errorf("session %q is duplicated", session.SessionID)
		}
		seen[session.SessionID] = struct{}{}
	}
	matchSeen := make(map[string]struct{}, len(l.Matches))
	for _, match := range l.Matches {
		if err := match.Validate(); err != nil {
			return err
		}
		if _, exists := seen[match.SessionID]; !exists {
			return fmt.Errorf("session search match %q has no listed session", match.SessionID)
		}
		key := match.SessionID + "\x00" + string(match.TurnID)
		if _, duplicate := matchSeen[key]; duplicate {
			return fmt.Errorf("session search match %q is duplicated", key)
		}
		matchSeen[key] = struct{}{}
	}
	return nil
}

type SessionLifecyclePatch struct {
	Title    *string `json:"title,omitempty"`
	Pinned   *bool   `json:"pinned,omitempty"`
	Archived *bool   `json:"archived,omitempty"`
}

func (p SessionLifecyclePatch) Validate() error {
	if p.Title == nil && p.Pinned == nil && p.Archived == nil {
		return errors.New("session lifecycle patch is empty")
	}
	if p.Title != nil {
		title := strings.TrimSpace(*p.Title)
		if title == "" || len(title) > 256 ||
			strings.ContainsAny(title, "\x00\r\n") {
			return errors.New("session lifecycle title is invalid")
		}
	}
	return nil
}

type SessionLifecycleUpdate struct {
	Session SessionSummary `json:"session"`
}

type SessionDeleteResult struct {
	Version   int       `json:"version"`
	SessionID string    `json:"session_id"`
	ThreadID  ThreadID  `json:"thread_id"`
	DeletedAt time.Time `json:"deleted_at"`
}

func validSessionLifecycleStatus(status SessionLifecycleStatus) bool {
	switch status {
	case SessionStatusIdle,
		SessionStatusRunning,
		SessionStatusAwaitingApproval,
		SessionStatusAwaitingInput,
		SessionStatusCompleted,
		SessionStatusFailed,
		SessionStatusInterrupted:
		return true
	default:
		return false
	}
}
