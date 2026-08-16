package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/sqlkit"
)

const (
	ExecutorAgentTurn    = "agent_turn"
	ExecutorWorkflowRun  = "workflow_run"
	ExecutorShellCommand = "shell_command"
)

var (
	ErrClaimLost = errors.New("task claim is held by another owner")
)

type AttemptStatus string

const (
	AttemptRunning     AttemptStatus = "running"
	AttemptCompleted   AttemptStatus = "completed"
	AttemptFailed      AttemptStatus = "failed"
	AttemptCanceled    AttemptStatus = "canceled"
	AttemptInterrupted AttemptStatus = "interrupted"
	AttemptWaiting     AttemptStatus = "waiting"
)

const (
	ReasonLeaseExpired = "lease_expired"
	ReasonDraining     = "draining"
	ReasonRetry        = "retry"
	ReasonInterrupted  = "interrupted"
)

type Attempt struct {
	TaskID            string
	Attempt           int
	Owner             string
	ThreadID          string
	TurnID            string
	Status            AttemptStatus
	Reason            string
	StartedAt         time.Time
	EndedAt           *time.Time
	PermissionDigests []string
}

type Backoff struct {
	Base time.Duration
	Max  time.Duration
}

func (b Backoff) Delay(attempt int) time.Duration {
	base := b.Base
	if base <= 0 {
		base = 15 * time.Second
	}
	maximum := b.Max
	if maximum <= 0 {
		maximum = 10 * time.Minute
	}
	delay := base
	for index := 1; index < attempt && delay < maximum; index++ {
		delay *= 2
	}
	if delay > maximum {
		delay = maximum
	}
	return delay
}

type ClaimRequest struct {
	Owner         string
	Executors     []string
	Lease         time.Duration
	Limit         int
	Now           time.Time
	SessionID     string
	WorkspaceRoot string
}

// Claim selects projection rows, then lets WorkGraph revision CAS choose the
// winner. The tasks table is never updated independently.
func (r *Repository) Claim(
	ctx context.Context,
	request ClaimRequest,
) ([]Task, error) {
	if r.db == nil {
		return nil, errors.New("task repository database is required")
	}
	if r.workGraphErr != nil {
		return nil, r.workGraphErr
	}
	if r.workGraphs == nil {
		return nil, errors.New("task WorkGraph store is required")
	}
	request.Owner = strings.TrimSpace(request.Owner)
	if request.Owner == "" {
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
	normalizedRoot, err := NormalizeWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve task claim workspace root: %w", err)
	}
	limit := request.Limit
	if limit <= 0 {
		limit = 1
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return r.claimWorkGraphs(
		ctx,
		request,
		executors,
		normalizedRoot,
		now,
		limit,
	)
}

func (r *Repository) Reclaim(
	ctx context.Context,
	at time.Time,
	backoff Backoff,
) ([]Task, error) {
	if r.db == nil {
		return nil, errors.New("task repository database is required")
	}
	if r.workGraphErr != nil {
		return nil, r.workGraphErr
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	return r.reclaimWorkGraphs(ctx, at.UTC(), backoff)
}

func (r *Repository) Attempts(
	ctx context.Context,
	id string,
) ([]Attempt, error) {
	if attempts, found, err := r.workGraphAttempts(ctx, id); err != nil {
		return nil, err
	} else if found {
		return attempts, nil
	}
	// Legacy rows remain readable as a projection compatibility surface.
	rows, err := r.db.QueryContext(ctx, `
		SELECT task_id, attempt, owner, COALESCE(thread_id, ''),
			COALESCE(turn_id, ''), status, COALESCE(reason, ''),
			started_at, ended_at
		FROM task_attempts
		WHERE task_id = ?
		ORDER BY attempt`,
		id,
	)
	if err != nil {
		return nil, err
	}
	return sqlkit.ScanAll(rows, scanAttempt)
}

func scanAttempt(row sqlkit.RowScanner) (Attempt, error) {
	var value Attempt
	var startedAt string
	var endedAt sql.NullString
	if err := row.Scan(
		&value.TaskID,
		&value.Attempt,
		&value.Owner,
		&value.ThreadID,
		&value.TurnID,
		&value.Status,
		&value.Reason,
		&startedAt,
		&endedAt,
	); err != nil {
		return Attempt{}, err
	}
	var err error
	value.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return Attempt{}, err
	}
	value.EndedAt, err = optionalTime(endedAt)
	return value, err
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

func normalizedExecutors(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || seen[trimmed] {
			continue
		}
		seen[trimmed] = true
		result = append(result, trimmed)
	}
	return result
}

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
