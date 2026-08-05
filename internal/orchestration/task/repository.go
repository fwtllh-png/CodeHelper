// Package task persists background task lifecycle state. It does not execute
// tasks; Runtime remains the sole orchestration state machine.
package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

var (
	ErrNotFound          = errors.New("task not found")
	ErrInvalidTransition = errors.New("invalid task state transition")
)

type State string

const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateWaiting   State = "waiting"
	StateFailed    State = "failed"
	StateCanceled  State = "canceled"
	StateCompleted State = "completed"
)

type Task struct {
	ID                string
	SessionID         string
	ThreadID          string
	TurnID            string
	Kind              string
	State             State
	LifecycleSequence uint64
	Payload           json.RawMessage
	Result            json.RawMessage
	Reason            string
	FailureReason     string
	LeaseOwner        string
	LeaseExpiresAt    *time.Time
	TerminalAt        *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	// Executor names who may run this task. An empty executor means nobody may:
	// most tasks are the model's own work board, and a worker that executed them
	// would turn a to-do list into live turns.
	Executor      string
	Attempt       int
	MaxAttempts   int
	NextAttemptAt *time.Time
	HeartbeatAt   *time.Time
}

type Transition struct {
	State          State
	Result         json.RawMessage
	Reason         string
	LeaseOwner     string
	LeaseExpiresAt *time.Time
	At             time.Time
}

type LifecycleEntry struct {
	TaskID    string
	Sequence  uint64
	State     State
	Reason    string
	CreatedAt time.Time
}

type Filter struct {
	SessionID     string
	ThreadID      string
	TurnID        string
	State         State
	WorkspaceRoot string
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func NewSQLiteRepository(store *sqlitestate.Store) *Repository {
	if store == nil {
		return &Repository{}
	}
	return NewRepository(store.DB())
}

func (r *Repository) Create(ctx context.Context, value Task) (Task, error) {
	if r.db == nil {
		return Task{}, errors.New("task repository database is required")
	}
	if value.ID == "" || value.SessionID == "" || value.Kind == "" {
		return Task{}, errors.New("task id, session id, and kind are required")
	}
	if value.State == "" {
		value.State = StateQueued
	}
	if value.State != StateQueued {
		return Task{}, fmt.Errorf("%w: tasks must be created queued", ErrInvalidTransition)
	}
	if err := validateExecution(&value); err != nil {
		return Task{}, err
	}
	payload, err := normalizedObject(value.Payload)
	if err != nil {
		return Task{}, fmt.Errorf("task payload: %w", err)
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.CreatedAt
	}
	value.LifecycleSequence = 1
	err = withTx(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO tasks(
				id, session_id, thread_id, turn_id, kind, state, payload_json,
				lifecycle_sequence, created_at, updated_at,
				executor, attempt, max_attempts, next_attempt_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, 0, ?, ?)`,
			value.ID, value.SessionID, nullable(value.ThreadID), nullable(value.TurnID),
			value.Kind, value.State, payload, timestamp(value.CreatedAt), timestamp(value.UpdatedAt),
			nullable(value.Executor), value.MaxAttempts, nullableTime(value.NextAttemptAt),
		); err != nil {
			return fmt.Errorf("create task: %w", err)
		}
		_, err := tx.ExecContext(ctx, `
			INSERT INTO task_lifecycle(task_id, sequence, state, created_at)
			VALUES (?, 1, ?, ?)`,
			value.ID, value.State, timestamp(value.UpdatedAt),
		)
		return err
	})
	if err != nil {
		return Task{}, err
	}
	value.Payload = payload
	return value, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Task, error) {
	if r.db == nil {
		return Task{}, errors.New("task repository database is required")
	}
	return get(ctx, r.db, id)
}

func (r *Repository) List(ctx context.Context, filter Filter, limit int) ([]Task, error) {
	if r.db == nil {
		return nil, errors.New("task repository database is required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("task list limit must be between 1 and 1000")
	}
	query := "SELECT id FROM tasks WHERE 1 = 1"
	var arguments []any
	add := func(clause string, value any) {
		query += " AND " + clause
		arguments = append(arguments, value)
	}
	if filter.SessionID != "" {
		add("session_id = ?", filter.SessionID)
	}
	if filter.ThreadID != "" {
		add("thread_id = ?", filter.ThreadID)
	}
	if filter.TurnID != "" {
		add("turn_id = ?", filter.TurnID)
	}
	if filter.State != "" {
		add("state = ?", filter.State)
	}
	if filter.WorkspaceRoot != "" {
		add(`EXISTS (
			SELECT 1
			FROM sessions s
			JOIN workspaces w ON w.id = s.workspace_id
			WHERE s.id = tasks.session_id AND w.root_path = ?
		)`, filter.WorkspaceRoot)
	}
	query += " ORDER BY created_at DESC, id LIMIT ?"
	arguments = append(arguments, limit)
	rows, err := r.db.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	values := make([]Task, 0, len(ids))
	for _, id := range ids {
		value, err := r.Get(ctx, id)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

// Update atomically validates the state transition, updates all task fields,
// and appends the next per-task lifecycle sequence.
func (r *Repository) Update(ctx context.Context, id string, change Transition) (Task, error) {
	if r.db == nil {
		return Task{}, errors.New("task repository database is required")
	}
	if change.At.IsZero() {
		change.At = time.Now().UTC()
	}
	var updated Task
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		current, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if change.State == "" {
			return errors.New("task transition state is required")
		}
		if current.State == change.State {
			updated = current
			return nil
		}
		if !CanTransition(current.State, change.State) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.State, change.State)
		}
		result := current.Result
		if change.Result != nil {
			result, err = normalizedJSON(change.Result)
			if err != nil {
				return fmt.Errorf("task result: %w", err)
			}
		}
		reason := change.Reason
		if change.State == StateFailed && reason == "" {
			return errors.New("failed task reason is required")
		}
		if change.State != StateFailed && change.State != StateCanceled {
			reason = ""
		}
		sequence := current.LifecycleSequence + 1
		var terminalAt any
		if isTerminal(change.State) {
			terminalAt = timestamp(change.At)
		}
		updateResult, err := tx.ExecContext(ctx, `
			UPDATE tasks SET state = ?, result_json = ?, failure_reason = ?,
				lease_owner = ?, lease_expires_at = ?, lifecycle_sequence = ?,
				terminal_at = ?, updated_at = ?
			WHERE id = ? AND state = ? AND lifecycle_sequence = ?`,
			change.State, nullableJSON(result), nullable(reason), nullable(change.LeaseOwner),
			nullableTime(change.LeaseExpiresAt), sequence, terminalAt, timestamp(change.At),
			id, current.State, current.LifecycleSequence,
		)
		if err != nil {
			return err
		}
		affected, err := updateResult.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return errors.New("task changed during lifecycle transition")
		}
		resultExec, err := tx.ExecContext(ctx, `
			INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			id, sequence, change.State, nullable(change.Reason), timestamp(change.At),
		)
		if err != nil {
			return err
		}
		affected, err = resultExec.RowsAffected()
		if err != nil || affected != 1 {
			if err != nil {
				return err
			}
			return errors.New("task lifecycle update was not persisted")
		}
		updated, err = get(ctx, tx, id)
		return err
	})
	return updated, err
}

func (r *Repository) Cancel(ctx context.Context, id, reason string, at time.Time) (Task, error) {
	if reason == "" {
		reason = "canceled"
	}
	return r.Update(ctx, id, Transition{State: StateCanceled, Reason: reason, At: at})
}

// PatchPayload merges keys into the existing payload object without a lifecycle
// transition. Use this for gates, work-board items, and other non-state evidence.
func (r *Repository) PatchPayload(ctx context.Context, id string, patch map[string]any) (Task, error) {
	if r.db == nil {
		return Task{}, errors.New("task repository database is required")
	}
	if len(patch) == 0 {
		return r.Get(ctx, id)
	}
	var updated Task
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		current, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		merged := map[string]any{}
		if len(current.Payload) > 0 {
			if err := json.Unmarshal(current.Payload, &merged); err != nil {
				return fmt.Errorf("task payload: %w", err)
			}
		}
		for key, value := range patch {
			merged[key] = value
		}
		payload, err := json.Marshal(merged)
		if err != nil {
			return err
		}
		now := timestamp(time.Now().UTC())
		result, err := tx.ExecContext(ctx, `
			UPDATE tasks SET payload_json = ?, updated_at = ? WHERE id = ?`,
			payload, now, id,
		)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return errors.New("task payload was not updated")
		}
		updated, err = get(ctx, tx, id)
		return err
	})
	return updated, err
}

// Recovery reports what a restart did with the tasks that were running when the
// previous process died.
type Recovery struct {
	// Requeued tasks have an executor and attempts left, so a worker will pick
	// them up again.
	Requeued int64
	// Failed tasks either have no executor, so nothing can run them, or have
	// spent their attempts.
	Failed int64
}

// RecoverInterrupted resolves legacy running tasks that never received a lease.
// Leased work belongs to its owner until lease_expires_at and is recovered only
// by Reclaim; otherwise opening a second persistent Host could steal work from a
// healthy worker. A repeated call is a no-op because only unleased running rows
// are selected.
//
// The split matters because tasks without an executor are records, so failing
// them is the only honest answer. A task that a dead worker was running is
// executable work that another worker should take over.
func (r *Repository) RecoverInterrupted(ctx context.Context, at time.Time) (Recovery, error) {
	if r.db == nil {
		return Recovery{}, errors.New("task repository database is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()
	var recovery Recovery
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
				SELECT id FROM tasks
				WHERE state = ? AND lease_owner IS NULL AND lease_expires_at IS NULL
				ORDER BY id`, StateRunning,
		)
		if err != nil {
			return err
		}
		var ids []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, id := range ids {
			current, err := get(ctx, tx, id)
			if err != nil {
				return err
			}
			if current.Executor == "" {
				if err := failInterrupted(ctx, tx, current, at); err != nil {
					return err
				}
				recovery.Failed++
				continue
			}
			// Delay zero: the work was interrupted rather than failing on its own
			// merits, so there is nothing to wait out before trying again.
			recovered, err := requeueLocked(ctx, tx, current, ReasonInterrupted, at, 0)
			if err != nil {
				return err
			}
			if recovered.State == StateQueued {
				recovery.Requeued++
			} else {
				recovery.Failed++
			}
		}
		return nil
	})
	return recovery, err
}

func failInterrupted(ctx context.Context, tx *sql.Tx, current Task, at time.Time) error {
	next := current.LifecycleSequence + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET state = ?, failure_reason = 'interrupted',
			lifecycle_sequence = ?, terminal_at = ?, updated_at = ?,
			lease_owner = NULL, lease_expires_at = NULL
		WHERE id = ? AND state = ? AND lifecycle_sequence = ?`,
		StateFailed, next, timestamp(at), timestamp(at),
		current.ID, StateRunning, current.LifecycleSequence,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("running task changed during recovery")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
		VALUES (?, ?, ?, 'interrupted', ?)`,
		current.ID, next, StateFailed, timestamp(at),
	); err != nil {
		return err
	}
	return endAttempt(ctx, tx, current.ID, current.Attempt, AttemptInterrupted, ReasonInterrupted, at)
}

func (r *Repository) Lifecycle(ctx context.Context, id string) ([]LifecycleEntry, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, sequence, state, COALESCE(reason, ''), created_at
		FROM task_lifecycle WHERE task_id = ? ORDER BY sequence`, id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []LifecycleEntry
	for rows.Next() {
		var entry LifecycleEntry
		var createdAt string
		if err := rows.Scan(
			&entry.TaskID, &entry.Sequence, &entry.State, &entry.Reason, &createdAt,
		); err != nil {
			return nil, err
		}
		entry.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func CanTransition(from, to State) bool {
	switch from {
	case StateQueued:
		return to == StateRunning || to == StateCanceled
	case StateRunning:
		// running -> queued is how a lease that expired, a worker that drained,
		// and a retryable failure all get back in line. The wait for
		// the next attempt is next_attempt_at, not a state, so that "waiting" keeps
		// meaning the one thing an operator has to act on: it is waiting for them.
		return to == StateWaiting || to == StateFailed ||
			to == StateCanceled || to == StateCompleted || to == StateQueued
	case StateWaiting:
		return to == StateRunning || to == StateFailed || to == StateCanceled
	default:
		return false
	}
}

func isTerminal(state State) bool {
	return state == StateFailed || state == StateCanceled || state == StateCompleted
}

type queryable interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func get(ctx context.Context, db queryable, id string) (Task, error) {
	var value Task
	var threadID, turnID, result, reason, owner, leaseAt, terminalAt sql.NullString
	var executor, nextAttemptAt, heartbeatAt sql.NullString
	var payload, createdAt, updatedAt string
	err := db.QueryRowContext(ctx, `
		SELECT id, session_id, thread_id, turn_id, kind, state, lifecycle_sequence,
			payload_json, result_json, failure_reason, lease_owner, lease_expires_at,
			terminal_at, created_at, updated_at,
			executor, attempt, max_attempts, next_attempt_at, heartbeat_at
		FROM tasks WHERE id = ?`, id,
	).Scan(
		&value.ID, &value.SessionID, &threadID, &turnID, &value.Kind, &value.State,
		&value.LifecycleSequence, &payload, &result, &reason, &owner, &leaseAt,
		&terminalAt, &createdAt, &updatedAt,
		&executor, &value.Attempt, &value.MaxAttempts, &nextAttemptAt, &heartbeatAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	value.ThreadID, value.TurnID = threadID.String, turnID.String
	value.Payload = json.RawMessage(payload)
	if result.Valid {
		value.Result = json.RawMessage(result.String)
	}
	value.Reason = reason.String
	value.FailureReason, value.LeaseOwner = reason.String, owner.String
	if value.CreatedAt, err = parseTime(createdAt); err != nil {
		return Task{}, err
	}
	if value.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Task{}, err
	}
	if leaseAt.Valid {
		parsed, parseErr := parseTime(leaseAt.String)
		if parseErr != nil {
			return Task{}, parseErr
		}
		value.LeaseExpiresAt = &parsed
	}
	if terminalAt.Valid {
		parsed, parseErr := parseTime(terminalAt.String)
		if parseErr != nil {
			return Task{}, parseErr
		}
		value.TerminalAt = &parsed
	}
	value.Executor = executor.String
	if value.NextAttemptAt, err = optionalTime(nextAttemptAt); err != nil {
		return Task{}, err
	}
	if value.HeartbeatAt, err = optionalTime(heartbeatAt); err != nil {
		return Task{}, err
	}
	return value, nil
}

func optionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func normalizedObject(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func normalizedJSON(value json.RawMessage) (json.RawMessage, error) {
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func nullableJSON(value json.RawMessage) any {
	if value == nil {
		return nil
	}
	return string(value)
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return timestamp(*value)
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	result, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse task timestamp: %w", err)
	}
	return result, nil
}

func withTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
