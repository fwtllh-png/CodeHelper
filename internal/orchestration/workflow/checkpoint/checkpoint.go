// Package checkpoint persists workflow runs and their per-node outcomes so that
// a run interrupted by a crash or a restart can continue instead of repeating
// work that already happened (RFC-007 D8).
package checkpoint

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

var (
	ErrNotFound = errors.New("workflow run not found")
	// ErrSpecChanged refuses a resume whose spec no longer matches the one the run
	// started from. Continuing would apply a new graph to old node records, which
	// silently skips nodes that never ran.
	ErrSpecChanged = errors.New("workflow spec changed since the run started")
)

// Run is the durable header of one workflow execution.
type Run struct {
	ID        string
	SessionID string
	TaskID    string
	SpecHash  string
	Spec      workflow.Spec
	Status    workflow.RunStatus
	Goal      string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
	// Resumed reports that Ensure found an existing run rather than creating one.
	Resumed bool
}

// Outputs stores node output out of line, so the state database holds a handle
// rather than however much text a node produced. contentstore.Store satisfies it.
type Outputs interface {
	Put(ctx context.Context, handle string, data []byte) error
	Get(ctx context.Context, handle string) ([]byte, error)
}

// Repository reads and writes workflow_runs / workflow_nodes.
type Repository struct {
	db *sql.DB
	// outputs is optional. Without it a resumed run inherits node status but not
	// node output, which is the state this package shipped in first.
	outputs Outputs
}

func NewRepository(db *sql.DB, outputs Outputs) *Repository {
	return &Repository{db: db, outputs: outputs}
}

func NewSQLiteRepository(store *sqlitestate.Store, outputs Outputs) *Repository {
	if store == nil {
		return &Repository{}
	}
	return NewRepository(store.DB(), outputs)
}

// EnsureRequest describes the run a caller is about to execute.
type EnsureRequest struct {
	ID        string
	SessionID string
	TaskID    string
	Spec      workflow.Spec
	Now       time.Time
}

// Ensure creates the run row, or adopts an existing one after checking that the
// spec is the same. It is the fail-closed gate in front of resume.
func (r *Repository) Ensure(ctx context.Context, request EnsureRequest) (Run, error) {
	if r.db == nil {
		return Run{}, errors.New("workflow checkpoint database is required")
	}
	if request.ID == "" || request.SessionID == "" {
		return Run{}, errors.New("workflow run id and session id are required")
	}
	if err := request.Spec.Validate(); err != nil {
		return Run{}, err
	}
	specJSON, err := json.Marshal(request.Spec)
	if err != nil {
		return Run{}, fmt.Errorf("encode workflow spec: %w", err)
	}
	now := request.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	fingerprint := request.Spec.Fingerprint()

	existing, err := r.Get(ctx, request.ID)
	switch {
	case err == nil:
		if existing.SpecHash != fingerprint {
			return Run{}, fmt.Errorf("%w: run %s started from %s",
				ErrSpecChanged, request.ID, existing.SpecHash)
		}
		if _, err := r.db.ExecContext(ctx, `
			UPDATE workflow_runs SET status = ?, error = NULL, updated_at = ?
			WHERE id = ?`,
			workflow.RunRunning, timestamp(now), request.ID,
		); err != nil {
			return Run{}, fmt.Errorf("resume workflow run: %w", err)
		}
		existing.Status, existing.UpdatedAt, existing.Resumed = workflow.RunRunning, now, true
		return existing, nil
	case errors.Is(err, ErrNotFound):
	default:
		return Run{}, err
	}

	if _, err := r.db.ExecContext(ctx, `
		INSERT INTO workflow_runs(
			id, session_id, task_id, spec_hash, spec_json, status, goal,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ID, request.SessionID, nullable(request.TaskID), fingerprint,
		string(specJSON), workflow.RunRunning, request.Spec.Goal,
		timestamp(now), timestamp(now),
	); err != nil {
		return Run{}, fmt.Errorf("create workflow run: %w", err)
	}
	return Run{
		ID: request.ID, SessionID: request.SessionID, TaskID: request.TaskID,
		SpecHash: fingerprint, Spec: request.Spec, Status: workflow.RunRunning,
		Goal: request.Spec.Goal, CreatedAt: now, UpdatedAt: now,
	}, nil
}

// Get returns one run header, including the spec it started from.
func (r *Repository) Get(ctx context.Context, id string) (Run, error) {
	if r.db == nil {
		return Run{}, errors.New("workflow checkpoint database is required")
	}
	var (
		run       Run
		taskID    sql.NullString
		failure   sql.NullString
		specJSON  string
		createdAt string
		updatedAt string
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT id, session_id, task_id, spec_hash, spec_json, status, goal, error,
			created_at, updated_at
		FROM workflow_runs WHERE id = ?`, id,
	).Scan(
		&run.ID, &run.SessionID, &taskID, &run.SpecHash, &specJSON, &run.Status,
		&run.Goal, &failure, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return Run{}, fmt.Errorf("read workflow run: %w", err)
	}
	run.TaskID, run.Error = taskID.String, failure.String
	if err := json.Unmarshal([]byte(specJSON), &run.Spec); err != nil {
		return Run{}, fmt.Errorf("decode workflow spec: %w", err)
	}
	if run.CreatedAt, err = parseTime(createdAt); err != nil {
		return Run{}, err
	}
	if run.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Run{}, err
	}
	return run, nil
}

// Settle records the run's terminal status.
func (r *Repository) Settle(
	ctx context.Context, id string, status workflow.RunStatus, failure string, at time.Time,
) error {
	if r.db == nil {
		return errors.New("workflow checkpoint database is required")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	outcome, err := r.db.ExecContext(ctx, `
		UPDATE workflow_runs SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, nullable(failure), timestamp(at), id,
	)
	if err != nil {
		return fmt.Errorf("settle workflow run: %w", err)
	}
	affected, err := outcome.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return nil
}

// LoadNodes implements workflow.Checkpoint.
func (r *Repository) LoadNodes(
	ctx context.Context, runID string,
) (map[string]workflow.NodeRecord, error) {
	if r.db == nil {
		return nil, errors.New("workflow checkpoint database is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT node_id, status, attempt, output_handle, reason, started_at, ended_at
		FROM workflow_nodes WHERE run_id = ?`, runID,
	)
	if err != nil {
		return nil, fmt.Errorf("load workflow nodes: %w", err)
	}
	defer rows.Close()
	records := make(map[string]workflow.NodeRecord)
	for rows.Next() {
		var (
			record    workflow.NodeRecord
			handle    sql.NullString
			reason    sql.NullString
			startedAt sql.NullString
			endedAt   sql.NullString
		)
		if err := rows.Scan(
			&record.ID, &record.Status, &record.Attempt,
			&handle, &reason, &startedAt, &endedAt,
		); err != nil {
			return nil, err
		}
		record.OutputHandle, record.Reason = handle.String, reason.String
		if record.Content, err = r.readOutput(ctx, record.OutputHandle); err != nil {
			return nil, err
		}
		if record.StartedAt, err = optionalTime(startedAt); err != nil {
			return nil, err
		}
		if record.EndedAt, err = optionalTime(endedAt); err != nil {
			return nil, err
		}
		records[record.ID] = record
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

// NodeStarted implements workflow.Checkpoint.
func (r *Repository) NodeStarted(
	ctx context.Context, runID string, record workflow.NodeRecord,
) error {
	return r.writeNode(ctx, runID, record, false)
}

// NodeSettled implements workflow.Checkpoint.
func (r *Repository) NodeSettled(
	ctx context.Context, runID string, record workflow.NodeRecord,
) error {
	return r.writeNode(ctx, runID, record, true)
}

func (r *Repository) writeNode(
	ctx context.Context, runID string, record workflow.NodeRecord, terminal bool,
) error {
	if r.db == nil {
		return errors.New("workflow checkpoint database is required")
	}
	if runID == "" || record.ID == "" {
		return errors.New("workflow node needs a run id and a node id")
	}
	if terminal && !record.Status.Terminal() {
		return fmt.Errorf("workflow node %q settled as %q, which is not terminal",
			record.ID, record.Status)
	}
	var ended any
	if terminal {
		at := record.EndedAt
		if at.IsZero() {
			at = time.Now().UTC()
		}
		ended = timestamp(at)
	}
	started := record.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	if record.OutputHandle == "" && record.Content != "" {
		handle, err := r.storeOutput(ctx, record.Content)
		if err != nil {
			return err
		}
		record.OutputHandle = handle
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO workflow_nodes(
			run_id, node_id, status, attempt, output_handle, reason, started_at, ended_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, node_id) DO UPDATE SET
			status = excluded.status,
			attempt = excluded.attempt,
			output_handle = COALESCE(excluded.output_handle, workflow_nodes.output_handle),
			reason = excluded.reason,
			started_at = COALESCE(workflow_nodes.started_at, excluded.started_at),
			ended_at = excluded.ended_at`,
		runID, record.ID, record.Status, record.Attempt,
		nullable(record.OutputHandle), nullable(record.Reason),
		timestamp(started), ended,
	)
	if err != nil {
		return fmt.Errorf("write workflow node: %w", err)
	}
	return nil
}

// storeOutput writes node output to the handle store. A run without one keeps
// the old behaviour rather than losing the node record over its output.
func (r *Repository) storeOutput(ctx context.Context, content string) (string, error) {
	if r.outputs == nil {
		return "", nil
	}
	data := []byte(content)
	handle := contentstore.StableHandle("workflow", data)
	if err := r.outputs.Put(ctx, handle, data); err != nil {
		return "", fmt.Errorf("store workflow node output: %w", err)
	}
	return handle, nil
}

// readOutput resolves a handle back to node output. A handle whose bytes are gone
// is reported as no output rather than as a failure: the node status is the part
// resume depends on, and losing the text does not make the record wrong.
func (r *Repository) readOutput(ctx context.Context, handle string) (string, error) {
	if handle == "" || r.outputs == nil {
		return "", nil
	}
	data, err := r.outputs.Get(ctx, handle)
	if err != nil {
		if errors.Is(err, contentstore.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("read workflow node output: %w", err)
	}
	return string(data), nil
}

// Nodes lists this run's node records in node id order, for CLI and panels.
func (r *Repository) Nodes(ctx context.Context, runID string) ([]workflow.NodeRecord, error) {
	records, err := r.LoadNodes(ctx, runID)
	if err != nil {
		return nil, err
	}
	ordered := make([]workflow.NodeRecord, 0, len(records))
	for _, record := range records {
		ordered = append(ordered, record)
	}
	sortRecords(ordered)
	return ordered, nil
}
