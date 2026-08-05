package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Executor names. A task is executable only when its executor is one of these,
// which is what keeps the model's work board out of the worker's queue.
const (
	ExecutorAgentTurn    = "agent_turn"
	ExecutorWorkflowRun  = "workflow_run"
	ExecutorShellCommand = "shell_command"
)

// ErrClaimLost reports that another owner holds the task. It is a normal
// outcome of two workers reaching for the same row, not a failure to report:
// the loser skips the task and takes the next one.
var ErrClaimLost = errors.New("task claim is held by another owner")

// AttemptStatus records how one attempt at a task ended.
type AttemptStatus string

const (
	AttemptRunning     AttemptStatus = "running"
	AttemptCompleted   AttemptStatus = "completed"
	AttemptFailed      AttemptStatus = "failed"
	AttemptCanceled    AttemptStatus = "canceled"
	AttemptInterrupted AttemptStatus = "interrupted"
	AttemptWaiting     AttemptStatus = "waiting"
)

// Requeue reasons. They are the only three ways a running task returns to the
// queue, and they read differently to an operator, so they stay distinct.
const (
	ReasonLeaseExpired = "lease_expired"
	ReasonDraining     = "draining"
	ReasonRetry        = "retry"
	ReasonInterrupted  = "interrupted"
)

// Attempt is one execution of a task by one owner.
type Attempt struct {
	TaskID    string
	Attempt   int
	Owner     string
	ThreadID  string
	TurnID    string
	Status    AttemptStatus
	Reason    string
	StartedAt time.Time
	EndedAt   *time.Time
}

// Backoff spaces out retries. It is deliberately without jitter: a single
// claimer per workspace cannot stampede, and a deterministic delay is one a
// test can assert against.
type Backoff struct {
	Base time.Duration
	Max  time.Duration
}

// Delay returns how long to wait before the given attempt number runs again.
func (b Backoff) Delay(attempt int) time.Duration {
	base := b.Base
	if base <= 0 {
		base = 15 * time.Second
	}
	max := b.Max
	if max <= 0 {
		max = 10 * time.Minute
	}
	delay := base
	for i := 1; i < attempt && delay < max; i++ {
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	return delay
}

// ClaimRequest asks for up to Limit runnable tasks on behalf of one owner.
type ClaimRequest struct {
	Owner string
	// Executors is what this worker can actually run. An empty list claims
	// nothing: a worker must name the work it understands.
	Executors []string
	Lease     time.Duration
	Limit     int
	Now       time.Time
	// SessionID narrows the claim to one session. Empty claims across sessions,
	// while WorkspaceRoot remains mandatory so takeover cannot cross a workspace.
	SessionID string
	// WorkspaceRoot is the execution authority boundary. Executors and their
	// security policy are rooted in one workspace, so claiming outside it would
	// run a valid task with the wrong filesystem and policy.
	WorkspaceRoot string
}

// Claim moves runnable tasks from queued to running under a lease. The select
// and the update are one statement so that two processes racing on the same
// database cannot both win a row.
func (r *Repository) Claim(ctx context.Context, request ClaimRequest) ([]Task, error) {
	if r.db == nil {
		return nil, errors.New("task repository database is required")
	}
	owner := strings.TrimSpace(request.Owner)
	if owner == "" {
		return nil, errors.New("task claim owner is required")
	}
	if request.Lease <= 0 {
		return nil, errors.New("task claim lease must be positive")
	}
	executors := normalizedExecutors(request.Executors)
	if len(executors) == 0 {
		return nil, nil
	}
	workspaceRoot := strings.TrimSpace(request.WorkspaceRoot)
	if workspaceRoot == "" {
		return nil, errors.New("task claim workspace root is required")
	}
	workspaceRoot, err := NormalizeWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve task claim workspace root: %w", err)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 1
	}
	now := request.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	var claimed []Task
	err = withTx(ctx, r.db, func(tx *sql.Tx) error {
		ids, err := claimIDs(
			ctx, tx, owner, executors, request.SessionID, workspaceRoot,
			now, request.Lease, limit,
		)
		if err != nil {
			return err
		}
		for _, id := range ids {
			value, err := get(ctx, tx, id)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
				VALUES (?, ?, ?, NULL, ?)`,
				id, value.LifecycleSequence, StateRunning, timestamp(now),
			); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO task_attempts(task_id, attempt, owner, status, started_at)
				VALUES (?, ?, ?, ?, ?)`,
				id, value.Attempt, owner, AttemptRunning, timestamp(now),
			); err != nil {
				return err
			}
			claimed = append(claimed, value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func claimIDs(
	ctx context.Context, tx *sql.Tx, owner string, executors []string,
	sessionID, workspaceRoot string, now time.Time, lease time.Duration, limit int,
) ([]string, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(executors)), ", ")
	arguments := []any{
		owner, timestamp(now.Add(lease)), timestamp(now), timestamp(now), StateQueued,
	}
	for _, executor := range executors {
		arguments = append(arguments, executor)
	}
	sessionClause := ""
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		sessionClause = " AND session_id = ?"
		arguments = append(arguments, trimmed)
	}
	workspaceClause := `
				AND EXISTS (
					SELECT 1
					FROM sessions AS claim_session
					JOIN workspaces AS claim_workspace
						ON claim_workspace.id = claim_session.workspace_id
					WHERE claim_session.id = tasks.session_id
						AND claim_workspace.root_path = ?
				)`
	arguments = append(arguments, workspaceRoot)
	arguments = append(arguments, timestamp(now), limit)
	rows, err := tx.QueryContext(ctx, `
		UPDATE tasks SET state = 'running', lease_owner = ?, lease_expires_at = ?,
			heartbeat_at = ?, attempt = attempt + 1, next_attempt_at = NULL,
			lifecycle_sequence = lifecycle_sequence + 1, updated_at = ?
		WHERE id IN (
			SELECT id FROM tasks
			WHERE state = ? AND executor IN (`+placeholders+`)
					AND attempt < max_attempts`+sessionClause+workspaceClause+`
				AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY created_at, id
			LIMIT ?
		)
		RETURNING id`, arguments...,
	)
	if err != nil {
		return nil, fmt.Errorf("claim tasks: %w", err)
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
	return ids, rows.Err()
}

// Heartbeat extends the lease of a task this owner still holds. It leaves the
// lifecycle sequence alone: a heartbeat is not a transition, and Update cannot
// express it because a same-state update there is a no-op.
func (r *Repository) Heartbeat(ctx context.Context, id, owner string, until time.Time) error {
	if r.db == nil {
		return errors.New("task repository database is required")
	}
	if until.IsZero() {
		return errors.New("task heartbeat expiry is required")
	}
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx, `
		UPDATE tasks SET lease_expires_at = ?, heartbeat_at = ?, updated_at = ?
		WHERE id = ? AND state = ? AND lease_owner = ?`,
		timestamp(until.UTC()), timestamp(now), timestamp(now), id, StateRunning, owner,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%w: task %s", ErrClaimLost, id)
	}
	return nil
}

// RecordAttemptTurn ties the current attempt to the thread and turn that is
// carrying it out, which is what makes a background execution auditable.
func (r *Repository) RecordAttemptTurn(ctx context.Context, id, owner, threadID, turnID string) error {
	if r.db == nil {
		return errors.New("task repository database is required")
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE task_attempts SET thread_id = ?, turn_id = ?
		WHERE task_id = ? AND owner = ? AND status = ?
			AND attempt = (SELECT attempt FROM tasks WHERE id = ?)`,
		nullable(threadID), nullable(turnID), id, owner, AttemptRunning, id,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%w: task %s", ErrClaimLost, id)
	}
	return nil
}

// Settle writes a task's outcome, but only for the owner that holds the lease.
// The owner predicate is the fence: a worker whose lease was reclaimed while it
// was stuck must not overwrite the result of the worker that took over.
func (r *Repository) Settle(ctx context.Context, id, owner string, change Transition) (Task, error) {
	if r.db == nil {
		return Task{}, errors.New("task repository database is required")
	}
	if change.State == "" {
		return Task{}, errors.New("task transition state is required")
	}
	if change.State == StateQueued {
		return Task{}, errors.New("use Requeue to return a task to the queue")
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
		if current.LeaseOwner != owner {
			return fmt.Errorf("%w: task %s is held by %q", ErrClaimLost, id, current.LeaseOwner)
		}
		if !CanTransition(current.State, change.State) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.State, change.State)
		}
		reason := change.Reason
		if change.State == StateFailed && reason == "" {
			return errors.New("failed task reason is required")
		}
		result := current.Result
		if change.Result != nil {
			if result, err = normalizedJSON(change.Result); err != nil {
				return fmt.Errorf("task result: %w", err)
			}
		}
		sequence := current.LifecycleSequence + 1
		var terminalAt any
		if isTerminal(change.State) {
			terminalAt = timestamp(change.At)
		}
		// The lease is released here whatever the outcome: a settled task is not
		// being worked on, and a stale lease would only delay the reclaimer.
		outcome, err := tx.ExecContext(ctx, `
			UPDATE tasks SET state = ?, result_json = ?, failure_reason = ?,
				lease_owner = NULL, lease_expires_at = NULL, lifecycle_sequence = ?,
				terminal_at = ?, updated_at = ?
			WHERE id = ? AND state = ? AND lifecycle_sequence = ? AND lease_owner = ?`,
			change.State, nullableJSON(result), nullable(failureReason(change.State, reason)),
			sequence, terminalAt, timestamp(change.At),
			id, current.State, current.LifecycleSequence, owner,
		)
		if err != nil {
			return err
		}
		affected, err := outcome.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("%w: task %s changed while settling", ErrClaimLost, id)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
			VALUES (?, ?, ?, ?, ?)`,
			id, sequence, change.State, nullable(reason), timestamp(change.At),
		); err != nil {
			return err
		}
		if err := endAttempt(
			ctx, tx, id, current.Attempt, attemptStatusFor(change.State), reason, change.At,
		); err != nil {
			return err
		}
		updated, err = get(ctx, tx, id)
		return err
	})
	return updated, err
}

// Requeue returns a running task to the queue with a delay before it may be
// claimed again. When the attempts are spent it fails the task instead, because
// a task that can never run again should not sit in the queue looking runnable.
func (r *Repository) Requeue(
	ctx context.Context, id, owner, reason string, at time.Time, delay time.Duration,
) (Task, error) {
	if r.db == nil {
		return Task{}, errors.New("task repository database is required")
	}
	if strings.TrimSpace(reason) == "" {
		return Task{}, errors.New("requeue reason is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()
	var updated Task
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		current, err := get(ctx, tx, id)
		if err != nil {
			return err
		}
		if owner != "" && current.LeaseOwner != owner {
			return fmt.Errorf("%w: task %s is held by %q", ErrClaimLost, id, current.LeaseOwner)
		}
		if current.State != StateRunning {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, current.State, StateQueued)
		}
		updated, err = requeueLocked(ctx, tx, current, reason, at, delay)
		return err
	})
	return updated, err
}

// requeueLocked assumes the caller already read the row inside the transaction.
//
// Draining gives the attempt back. We chose to stop, so the work never had its
// turn, and a task allowed one attempt would otherwise be killed by a routine
// restart. A lost lease does not get that treatment: there we cannot tell
// whether the task is what killed the worker, and a poison task that retries
// forever is worse than one that fails and waits for a person.
func requeueLocked(
	ctx context.Context, tx *sql.Tx, current Task, reason string, at time.Time, delay time.Duration,
) (Task, error) {
	restore := reason == ReasonDraining
	next := StateQueued
	exhausted := !restore && current.Attempt >= current.MaxAttempts
	if exhausted {
		next = StateFailed
	}
	sequence := current.LifecycleSequence + 1
	attempt := current.Attempt
	if restore && attempt > 0 {
		attempt--
	}
	var nextAttemptAt, terminalAt any
	if exhausted {
		terminalAt = timestamp(at)
	} else if delay > 0 {
		nextAttemptAt = timestamp(at.Add(delay))
	}
	outcome, err := tx.ExecContext(ctx, `
		UPDATE tasks SET state = ?, failure_reason = ?, lease_owner = NULL,
			lease_expires_at = NULL, next_attempt_at = ?, lifecycle_sequence = ?,
			terminal_at = ?, updated_at = ?, attempt = ?
		WHERE id = ? AND state = ? AND lifecycle_sequence = ?`,
		next, nullable(failureReason(next, reason)), nextAttemptAt, sequence,
		terminalAt, timestamp(at), attempt,
		current.ID, current.State, current.LifecycleSequence,
	)
	if err != nil {
		return Task{}, err
	}
	affected, err := outcome.RowsAffected()
	if err != nil {
		return Task{}, err
	}
	if affected != 1 {
		return Task{}, fmt.Errorf("%w: task %s changed while requeueing", ErrClaimLost, current.ID)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		current.ID, sequence, next, nullable(reason), timestamp(at),
	); err != nil {
		return Task{}, err
	}
	if restore {
		// The attempt number is going to be handed out again, so the row for it
		// has to go: an attempt row means an attempt that counted.
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM task_attempts WHERE task_id = ? AND attempt = ?`,
			current.ID, current.Attempt,
		); err != nil {
			return Task{}, err
		}
		return get(ctx, tx, current.ID)
	}
	status := AttemptInterrupted
	if reason == ReasonRetry {
		status = AttemptFailed
	}
	if err := endAttempt(ctx, tx, current.ID, current.Attempt, status, reason, at); err != nil {
		return Task{}, err
	}
	return get(ctx, tx, current.ID)
}

// Reclaim requeues every running task whose lease has expired. It is what makes
// a killed worker recoverable by a live one rather than by a restart.
func (r *Repository) Reclaim(
	ctx context.Context, at time.Time, backoff Backoff,
) ([]Task, error) {
	if r.db == nil {
		return nil, errors.New("task repository database is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	at = at.UTC()
	var reclaimed []Task
	err := withTx(ctx, r.db, func(tx *sql.Tx) error {
		ids, err := expiredIDs(ctx, tx, at)
		if err != nil {
			return err
		}
		for _, id := range ids {
			current, err := get(ctx, tx, id)
			if err != nil {
				return err
			}
			value, err := requeueLocked(
				ctx, tx, current, ReasonLeaseExpired, at, backoff.Delay(current.Attempt),
			)
			if err != nil {
				return err
			}
			reclaimed = append(reclaimed, value)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reclaimed, nil
}

func expiredIDs(ctx context.Context, tx *sql.Tx, at time.Time) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT id FROM tasks
		WHERE state = ? AND executor IS NOT NULL AND lease_expires_at IS NOT NULL
			AND lease_expires_at <= ?
		ORDER BY lease_expires_at, id`,
		StateRunning, timestamp(at),
	)
	if err != nil {
		return nil, err
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
	return ids, rows.Err()
}

// Attempts lists what has been tried for a task, oldest first.
func (r *Repository) Attempts(ctx context.Context, id string) ([]Attempt, error) {
	if r.db == nil {
		return nil, errors.New("task repository database is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, attempt, owner, COALESCE(thread_id, ''), COALESCE(turn_id, ''),
			status, COALESCE(reason, ''), started_at, ended_at
		FROM task_attempts WHERE task_id = ? ORDER BY attempt`, id,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var attempts []Attempt
	for rows.Next() {
		var value Attempt
		var startedAt string
		var endedAt sql.NullString
		if err := rows.Scan(
			&value.TaskID, &value.Attempt, &value.Owner, &value.ThreadID, &value.TurnID,
			&value.Status, &value.Reason, &startedAt, &endedAt,
		); err != nil {
			return nil, err
		}
		if value.StartedAt, err = parseTime(startedAt); err != nil {
			return nil, err
		}
		if value.EndedAt, err = optionalTime(endedAt); err != nil {
			return nil, err
		}
		attempts = append(attempts, value)
	}
	return attempts, rows.Err()
}

func endAttempt(
	ctx context.Context, tx *sql.Tx, id string, attempt int,
	status AttemptStatus, reason string, at time.Time,
) error {
	if attempt <= 0 {
		// Nothing claimed this task, so there is no attempt row to close. That is
		// the ordinary shape of a canceled queued task.
		return nil
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE task_attempts SET status = ?, reason = ?, ended_at = ?
		WHERE task_id = ? AND attempt = ? AND status = ?`,
		status, nullable(reason), timestamp(at), id, attempt, AttemptRunning,
	)
	return err
}

func attemptStatusFor(state State) AttemptStatus {
	switch state {
	case StateCompleted:
		return AttemptCompleted
	case StateCanceled:
		return AttemptCanceled
	case StateWaiting:
		return AttemptWaiting
	default:
		return AttemptFailed
	}
}

// failureReason keeps failure_reason for the states that mean something failed.
// The column doubles as the reason an operator reads, so writing a requeue
// reason into a queued row would make a healthy retry look like a failure.
func failureReason(state State, reason string) string {
	if state == StateFailed || state == StateCanceled {
		return reason
	}
	return ""
}

func normalizedExecutors(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, duplicate := seen[trimmed]; duplicate {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

// validateExecution normalizes the executable half of a new task. A task with no
// executor keeps max_attempts at one so that the column never suggests a retry
// nobody will perform.
func validateExecution(value *Task) error {
	value.Executor = strings.TrimSpace(value.Executor)
	if value.Executor == "" {
		if value.MaxAttempts > 1 {
			return errors.New("a task without an executor cannot have retries")
		}
		value.MaxAttempts = 1
		return nil
	}
	switch value.Executor {
	case ExecutorAgentTurn, ExecutorWorkflowRun, ExecutorShellCommand:
	default:
		return fmt.Errorf("unknown task executor %q", value.Executor)
	}
	if value.MaxAttempts < 0 {
		return errors.New("task max attempts cannot be negative")
	}
	if value.MaxAttempts == 0 {
		value.MaxAttempts = 1
	}
	return nil
}
