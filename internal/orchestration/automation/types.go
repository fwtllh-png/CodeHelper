// Package automation persists recurring task schedules and enqueues each
// occurrence exactly once through CodeHelper's existing task tables.
package automation

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("automation not found")
	ErrRunNotFound     = errors.New("automation run not found")
	ErrVersionConflict = errors.New("automation version conflict")
	ErrInvalidStatus   = errors.New("invalid automation status")
)

type Status string

const (
	StatusActive  Status = "active"
	StatusPaused  Status = "paused"
	StatusDeleted Status = "deleted"
)

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunWaiting   RunStatus = "waiting"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
	RunCompleted RunStatus = "completed"
)

type Trigger string

const (
	TriggerScheduled Trigger = "scheduled"
	TriggerManual    Trigger = "manual"
)

// Automation is a versioned task template. CreatedAt is also the immutable
// recurrence anchor and is therefore always persisted.
type Automation struct {
	ID          string
	Version     uint64
	SessionID   string
	ThreadID    string
	TurnID      string
	Name        string
	Status      Status
	RRULE       string
	Timezone    string
	TaskKind    string
	TaskPayload json.RawMessage
	// TaskExecutor decides whether this schedule produces work or only a record.
	// Empty means the enqueued task is a note for someone to read, which is all
	// an automation could produce before background execution existed.
	TaskExecutor    string
	TaskMaxAttempts int
	CreatedAt       time.Time
	UpdatedAt       time.Time
	NextRunAt       *time.Time
	LastRunAt       *time.Time
}

type Run struct {
	ID                 string
	Version            uint64
	AutomationID       string
	ScheduledFor       time.Time
	Trigger            Trigger
	Status             RunStatus
	TaskID             string
	TaskIdempotencyKey string
	ThreadID           string
	TurnID             string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Filter struct {
	SessionID string
	Status    Status
}

// Update contains mutable automation fields. ExpectedVersion is mandatory and
// protects concurrent API writers.
type Update struct {
	ExpectedVersion uint64
	Name            string
	RRULE           string
	TaskKind        string
	TaskPayload     json.RawMessage
	TaskExecutor    string
	TaskMaxAttempts int
	ThreadID        string
	TurnID          string
	At              time.Time
}
