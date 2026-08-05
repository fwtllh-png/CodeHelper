package automation

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

// Repository persists automations and run ledgers in CodeHelper SQLite state.
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

type CreateRequest struct {
	ID              string
	SessionID       string
	ThreadID        string
	TurnID          string
	Name            string
	RRULE           string
	TaskKind        string
	TaskPayload     json.RawMessage
	TaskExecutor    string
	TaskMaxAttempts int
	CreatedAt       time.Time
}

func (r *Repository) Create(ctx context.Context, request CreateRequest) (Automation, error) {
	if r.db == nil {
		return Automation{}, errors.New("automation repository database is required")
	}
	if request.ID == "" || request.SessionID == "" || strings.TrimSpace(request.Name) == "" {
		return Automation{}, errors.New("automation id, session id, and name are required")
	}
	if request.TaskKind == "" {
		request.TaskKind = "automation"
	}
	rule, err := ParseRRULE(request.RRULE)
	if err != nil {
		return Automation{}, err
	}
	executor, attempts, err := validateExecution(request.TaskExecutor, request.TaskMaxAttempts)
	if err != nil {
		return Automation{}, err
	}
	payload, err := normalizedObject(request.TaskPayload)
	if err != nil {
		return Automation{}, fmt.Errorf("task payload: %w", err)
	}
	createdAt := request.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	next := rule.Next(createdAt, time.Time{})
	value := Automation{
		ID: request.ID, Version: 1, SessionID: request.SessionID,
		ThreadID: request.ThreadID, TurnID: request.TurnID,
		Name: strings.TrimSpace(request.Name), Status: StatusActive,
		RRULE: rule.Canonical(), Timezone: "UTC", TaskKind: request.TaskKind,
		TaskPayload: payload, TaskExecutor: executor, TaskMaxAttempts: attempts,
		CreatedAt: createdAt, UpdatedAt: createdAt,
		NextRunAt: &next,
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO automations(
			id, version, session_id, thread_id, turn_id, name, status, rrule, timezone,
			task_kind, task_payload_json, created_at, updated_at, next_run_at,
			task_executor, task_max_attempts
		) VALUES (?, 1, ?, ?, ?, ?, ?, ?, 'UTC', ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.SessionID, nullable(value.ThreadID), nullable(value.TurnID),
		value.Name, value.Status, value.RRULE, value.TaskKind, string(value.TaskPayload),
		timestamp(value.CreatedAt), timestamp(value.UpdatedAt), timestamp(next),
		nullable(value.TaskExecutor), value.TaskMaxAttempts,
	)
	if err != nil {
		return Automation{}, err
	}
	return value, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Automation, error) {
	return getAutomation(ctx, r.db, id)
}

func (r *Repository) List(ctx context.Context, filter Filter) ([]Automation, error) {
	query := `
		SELECT id, version, session_id, thread_id, turn_id, name, status, rrule, timezone,
			task_kind, task_payload_json, created_at, updated_at, next_run_at, last_run_at,
			task_executor, task_max_attempts
		FROM automations WHERE status != ?`
	args := []any{StatusDeleted}
	if filter.SessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, filter.SessionID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	query += ` ORDER BY created_at, id`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []Automation
	for rows.Next() {
		value, err := scanAutomation(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (r *Repository) Update(ctx context.Context, id string, change Update) (Automation, error) {
	if change.ExpectedVersion == 0 {
		return Automation{}, errors.New("expected version is required")
	}
	at := change.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var updated Automation
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		current, err := getAutomation(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Status == StatusDeleted {
			return ErrNotFound
		}
		if current.Version != change.ExpectedVersion {
			return ErrVersionConflict
		}
		if change.Name != "" {
			current.Name = strings.TrimSpace(change.Name)
		}
		if change.RRULE != "" {
			rule, err := ParseRRULE(change.RRULE)
			if err != nil {
				return err
			}
			current.RRULE = rule.Canonical()
			if current.Status == StatusActive {
				next := rule.Next(current.CreatedAt, at)
				current.NextRunAt = &next
			}
		}
		if change.TaskKind != "" {
			current.TaskKind = change.TaskKind
		}
		if change.TaskExecutor != "" || change.TaskMaxAttempts != 0 {
			executor := change.TaskExecutor
			if executor == "" {
				executor = current.TaskExecutor
			}
			attempts := change.TaskMaxAttempts
			if attempts == 0 {
				attempts = current.TaskMaxAttempts
			}
			normalized, normalizedAttempts, err := validateExecution(executor, attempts)
			if err != nil {
				return err
			}
			current.TaskExecutor, current.TaskMaxAttempts = normalized, normalizedAttempts
		}
		if change.TaskPayload != nil {
			payload, err := normalizedObject(change.TaskPayload)
			if err != nil {
				return err
			}
			current.TaskPayload = payload
		}
		if change.ThreadID != "" {
			current.ThreadID = change.ThreadID
		}
		if change.TurnID != "" {
			current.TurnID = change.TurnID
		}
		current.Version++
		current.UpdatedAt = at
		if err := writeAutomation(ctx, tx, current); err != nil {
			return err
		}
		updated = current
		return nil
	})
	return updated, err
}

func (r *Repository) Pause(ctx context.Context, id string, expectedVersion uint64, at time.Time) (Automation, error) {
	return r.setStatus(ctx, id, expectedVersion, StatusPaused, at, true)
}

func (r *Repository) Resume(ctx context.Context, id string, expectedVersion uint64, at time.Time) (Automation, error) {
	return r.setStatus(ctx, id, expectedVersion, StatusActive, at, false)
}

func (r *Repository) Delete(ctx context.Context, id string, expectedVersion uint64, at time.Time) (Automation, error) {
	return r.setStatus(ctx, id, expectedVersion, StatusDeleted, at, true)
}

func (r *Repository) setStatus(
	ctx context.Context, id string, expectedVersion uint64, status Status, at time.Time, clearNext bool,
) (Automation, error) {
	if expectedVersion == 0 {
		return Automation{}, errors.New("expected version is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	var updated Automation
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		current, err := getAutomation(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Status == StatusDeleted {
			return ErrNotFound
		}
		if current.Version != expectedVersion {
			return ErrVersionConflict
		}
		if status == StatusActive && current.Status != StatusPaused && current.Status != StatusActive {
			return ErrInvalidStatus
		}
		current.Status = status
		current.Version++
		current.UpdatedAt = at
		if clearNext || status != StatusActive {
			current.NextRunAt = nil
		} else {
			rule, err := ParseRRULE(current.RRULE)
			if err != nil {
				return err
			}
			next := rule.Next(current.CreatedAt, at)
			current.NextRunAt = &next
		}
		if err := writeAutomation(ctx, tx, current); err != nil {
			return err
		}
		updated = current
		return nil
	})
	return updated, err
}

// RunNow enqueues a manual occurrence without advancing the schedule slot.
func (r *Repository) RunNow(ctx context.Context, id string, expectedVersion uint64, at time.Time) (Run, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	var run Run
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		current, err := getAutomation(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Status == StatusDeleted {
			return ErrNotFound
		}
		if current.Version != expectedVersion {
			return ErrVersionConflict
		}
		run, err = enqueue(ctx, tx, current, at, TriggerManual, at)
		if err != nil {
			return err
		}
		current.Version++
		current.UpdatedAt = at
		current.LastRunAt = &at
		return writeAutomation(ctx, tx, current)
	})
	return run, err
}

// Tick enqueues every due active automation slot exactly once and advances
// next_run_at using the persisted creation anchor.
func (r *Repository) Tick(ctx context.Context, now time.Time) ([]Run, error) {
	if r.db == nil {
		return nil, errors.New("automation repository database is required")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	var runs []Run
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		if err := reconcileNextRuns(ctx, tx, now); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `
			SELECT id FROM automations
			WHERE status = ? AND next_run_at IS NOT NULL AND next_run_at <= ?
			ORDER BY next_run_at, id`,
			StatusActive, timestamp(now),
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
			current, err := getAutomation(ctx, tx, id)
			if err != nil {
				return err
			}
			if current.NextRunAt == nil || current.NextRunAt.After(now) {
				continue
			}
			scheduledFor := current.NextRunAt.UTC()
			run, err := enqueue(ctx, tx, current, scheduledFor, TriggerScheduled, now)
			if errors.Is(err, errDuplicateSlot) {
				rule, parseErr := ParseRRULE(current.RRULE)
				if parseErr != nil {
					return parseErr
				}
				next := rule.Next(current.CreatedAt, scheduledFor)
				current.NextRunAt = &next
				current.UpdatedAt = now
				current.Version++
				if err := writeAutomation(ctx, tx, current); err != nil {
					return err
				}
				continue
			}
			if err != nil {
				return err
			}
			rule, err := ParseRRULE(current.RRULE)
			if err != nil {
				return err
			}
			next := rule.Next(current.CreatedAt, scheduledFor)
			current.NextRunAt = &next
			current.LastRunAt = &scheduledFor
			current.UpdatedAt = now
			current.Version++
			if err := writeAutomation(ctx, tx, current); err != nil {
				return err
			}
			runs = append(runs, run)
		}
		return nil
	})
	return runs, err
}

func (r *Repository) GetRun(ctx context.Context, id string) (Run, error) {
	return getRun(ctx, r.db, id)
}

func (r *Repository) ListRuns(ctx context.Context, automationID string) ([]Run, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, version, automation_id, scheduled_for, trigger, status, task_id,
			task_idempotency_key, thread_id, turn_id, created_at, updated_at
		FROM automation_runs WHERE automation_id = ?
		ORDER BY scheduled_for DESC, id`, automationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []Run
	for rows.Next() {
		value, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

var errDuplicateSlot = errors.New("automation slot already enqueued")

// validateExecution rejects an executor no worker answers to. A schedule whose
// tasks nothing can run is allowed, because that is what automations produced
// before RFC-007 and those rows are records rather than work, but a misspelled
// executor is not: it would silently become one of those records.
func validateExecution(executor string, maxAttempts int) (string, int, error) {
	executor = strings.TrimSpace(executor)
	if executor == "" {
		if maxAttempts > 1 {
			return "", 0, errors.New("an automation without a task executor cannot have retries")
		}
		return "", 1, nil
	}
	switch executor {
	case task.ExecutorAgentTurn, task.ExecutorWorkflowRun, task.ExecutorShellCommand:
	default:
		return "", 0, fmt.Errorf("unknown automation task executor %q", executor)
	}
	if maxAttempts < 0 {
		return "", 0, errors.New("automation task max attempts cannot be negative")
	}
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	return executor, maxAttempts, nil
}

func reconcileNextRuns(ctx context.Context, tx *sql.Tx, now time.Time) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM automations
		WHERE status = ? AND next_run_at IS NULL
		ORDER BY id`, StatusActive,
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
		current, err := getAutomation(ctx, tx, id)
		if err != nil {
			return err
		}
		rule, err := ParseRRULE(current.RRULE)
		if err != nil {
			return err
		}
		after := current.CreatedAt.Add(-time.Nanosecond)
		if current.LastRunAt != nil {
			after = *current.LastRunAt
		}
		next := rule.Next(current.CreatedAt, after)
		current.NextRunAt = &next
		current.UpdatedAt = now
		current.Version++
		if err := writeAutomation(ctx, tx, current); err != nil {
			return err
		}
	}
	return nil
}

func enqueue(
	ctx context.Context, tx *sql.Tx, current Automation, scheduledFor time.Time, trigger Trigger, at time.Time,
) (Run, error) {
	taskID := "task_auto_" + current.ID + "_" + scheduledFor.UTC().Format("20060102T150405.000000000")
	if trigger == TriggerManual {
		taskID = "task_auto_manual_" + current.ID + "_" + at.UTC().Format("20060102T150405.000000000")
	}
	idempotency := fmt.Sprintf("automation:%s:%s:%s", current.ID, trigger, scheduledFor.UTC().Format(time.RFC3339Nano))
	runID := "run_" + current.ID + "_" + scheduledFor.UTC().Format("20060102T150405.000000000")
	if trigger == TriggerManual {
		runID = "run_manual_" + current.ID + "_" + at.UTC().Format("20060102T150405.000000000")
		idempotency = fmt.Sprintf("automation:%s:manual:%s", current.ID, at.UTC().Format(time.RFC3339Nano))
	}
	payload, err := normalizedObject(current.TaskPayload)
	if err != nil {
		return Run{}, err
	}
	attempts := current.TaskMaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO tasks(
			id, session_id, thread_id, turn_id, kind, state, payload_json,
			lifecycle_sequence, created_at, updated_at,
			executor, attempt, max_attempts
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, 0, ?)`,
		taskID, current.SessionID, nullable(current.ThreadID), nullable(current.TurnID),
		current.TaskKind, task.StateQueued, string(payload), timestamp(at), timestamp(at),
		nullable(current.TaskExecutor), attempts,
	); err != nil {
		if isUniqueViolation(err) {
			return Run{}, errDuplicateSlot
		}
		return Run{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
		VALUES (?, 1, ?, NULL, ?)`,
		taskID, task.StateQueued, timestamp(at),
	); err != nil {
		return Run{}, err
	}
	run := Run{
		ID: runID, Version: 1, AutomationID: current.ID, ScheduledFor: scheduledFor.UTC(),
		Trigger: trigger, Status: RunQueued, TaskID: taskID, TaskIdempotencyKey: idempotency,
		ThreadID: current.ThreadID, TurnID: current.TurnID, CreatedAt: at, UpdatedAt: at,
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO automation_runs(
			id, version, automation_id, scheduled_for, trigger, status, task_id,
			task_idempotency_key, thread_id, turn_id, created_at, updated_at
		) VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.AutomationID, timestamp(run.ScheduledFor), run.Trigger, run.Status,
		run.TaskID, run.TaskIdempotencyKey, nullable(run.ThreadID), nullable(run.TurnID),
		timestamp(run.CreatedAt), timestamp(run.UpdatedAt),
	)
	if err != nil {
		if isUniqueViolation(err) {
			return Run{}, errDuplicateSlot
		}
		return Run{}, err
	}
	return run, nil
}

type scanner interface {
	Scan(dest ...any) error
}

type queryable interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getAutomation(ctx context.Context, db queryable, id string) (Automation, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, version, session_id, thread_id, turn_id, name, status, rrule, timezone,
			task_kind, task_payload_json, created_at, updated_at, next_run_at, last_run_at,
			task_executor, task_max_attempts
		FROM automations WHERE id = ?`, id)
	value, err := scanAutomation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Automation{}, ErrNotFound
	}
	return value, err
}

func writeAutomation(ctx context.Context, tx *sql.Tx, value Automation) error {
	result, err := tx.ExecContext(ctx, `
		UPDATE automations SET version = ?, session_id = ?, thread_id = ?, turn_id = ?,
			name = ?, status = ?, rrule = ?, timezone = 'UTC', task_kind = ?,
			task_payload_json = ?, updated_at = ?, next_run_at = ?, last_run_at = ?,
			task_executor = ?, task_max_attempts = ?
		WHERE id = ? AND version = ?`,
		value.Version, value.SessionID, nullable(value.ThreadID), nullable(value.TurnID),
		value.Name, value.Status, value.RRULE, value.TaskKind, string(value.TaskPayload),
		timestamp(value.UpdatedAt), nullableTime(value.NextRunAt), nullableTime(value.LastRunAt),
		nullable(value.TaskExecutor), value.TaskMaxAttempts,
		value.ID, value.Version-1,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return ErrVersionConflict
	}
	return nil
}

func scanAutomation(row scanner) (Automation, error) {
	var value Automation
	var threadID, turnID, nextRun, lastRun, executor sql.NullString
	var payload, createdAt, updatedAt string
	if err := row.Scan(
		&value.ID, &value.Version, &value.SessionID, &threadID, &turnID, &value.Name,
		&value.Status, &value.RRULE, &value.Timezone, &value.TaskKind, &payload,
		&createdAt, &updatedAt, &nextRun, &lastRun, &executor, &value.TaskMaxAttempts,
	); err != nil {
		return Automation{}, err
	}
	value.TaskExecutor = executor.String
	value.ThreadID, value.TurnID = threadID.String, turnID.String
	value.TaskPayload = json.RawMessage(payload)
	var err error
	value.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Automation{}, err
	}
	value.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Automation{}, err
	}
	if nextRun.Valid {
		parsed, err := parseTime(nextRun.String)
		if err != nil {
			return Automation{}, err
		}
		value.NextRunAt = &parsed
	}
	if lastRun.Valid {
		parsed, err := parseTime(lastRun.String)
		if err != nil {
			return Automation{}, err
		}
		value.LastRunAt = &parsed
	}
	return value, nil
}

func getRun(ctx context.Context, db queryable, id string) (Run, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, version, automation_id, scheduled_for, trigger, status, task_id,
			task_idempotency_key, thread_id, turn_id, created_at, updated_at
		FROM automation_runs WHERE id = ?`, id)
	value, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrRunNotFound
	}
	return value, err
}

func scanRun(row scanner) (Run, error) {
	var value Run
	var taskID, threadID, turnID sql.NullString
	var scheduledFor, createdAt, updatedAt string
	if err := row.Scan(
		&value.ID, &value.Version, &value.AutomationID, &scheduledFor, &value.Trigger,
		&value.Status, &taskID, &value.TaskIdempotencyKey, &threadID, &turnID,
		&createdAt, &updatedAt,
	); err != nil {
		return Run{}, err
	}
	value.TaskID, value.ThreadID, value.TurnID = taskID.String, threadID.String, turnID.String
	var err error
	value.ScheduledFor, err = parseTime(scheduledFor)
	if err != nil {
		return Run{}, err
	}
	value.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Run{}, err
	}
	value.UpdatedAt, err = parseTime(updatedAt)
	return value, err
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

func normalizedObject(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
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

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique") || strings.Contains(message, "constraint")
}
