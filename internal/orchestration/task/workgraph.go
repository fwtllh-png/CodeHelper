package task

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	"github.com/fwtllh-png/CodeHelper/internal/persist/sqlkit"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const executableNodeID protocol.NodeID = "node_task"

type Submission struct {
	RunID     protocol.RunID
	CommandID string
	Source    string
	At        time.Time
}

func WorkGraphRunID(taskID string) protocol.RunID {
	return protocol.RunID("run_task_" + taskID)
}

func (r *Repository) createExecutable(
	ctx context.Context,
	value Task,
) (Task, error) {
	if r.workGraphs == nil {
		return Task{}, firstError(
			r.workGraphErr,
			errors.New("task WorkGraph store is unavailable"),
		)
	}
	command, err := compileExecutable(ctx, r.db, value, Submission{
		RunID: WorkGraphRunID(value.ID), CommandID: "task:create:" + value.ID,
		Source: "task", At: value.CreatedAt,
	})
	if err != nil {
		return Task{}, err
	}
	result, err := r.workGraphs.ExecuteProjected(
		ctx,
		command,
		func(tx *sql.Tx, result kernel.Result) error {
			return insertTaskProjection(ctx, tx, value, result.Graph)
		},
	)
	if err != nil {
		return Task{}, err
	}
	return projectTask(result.Graph)
}

func SubmitExecutableTx(
	ctx context.Context,
	tx *sql.Tx,
	workGraphs *orchestrationstore.Store,
	value Task,
	submission Submission,
) (Task, error) {
	if tx == nil || workGraphs == nil {
		return Task{}, errors.New("task submission transaction and WorkGraph store are required")
	}
	command, err := compileExecutable(ctx, tx, value, submission)
	if err != nil {
		return Task{}, err
	}
	result, err := workGraphs.ExecuteTx(
		ctx,
		tx,
		command,
		func(tx *sql.Tx, result kernel.Result) error {
			return insertTaskProjection(ctx, tx, value, result.Graph)
		},
	)
	if err != nil {
		return Task{}, err
	}
	return projectTask(result.Graph)
}

func compileExecutable(
	ctx context.Context,
	query queryable,
	value Task,
	submission Submission,
) (kernel.Command, error) {
	if value.Executor == "" {
		return kernel.Command{}, errors.New("executable task requires an executor")
	}
	runID := submission.RunID
	if runID == "" {
		runID = WorkGraphRunID(value.ID)
	}
	commandID := strings.TrimSpace(submission.CommandID)
	if commandID == "" {
		commandID = "task:create:" + value.ID
	}
	source := strings.TrimSpace(submission.Source)
	if source == "" {
		source = "task"
	}
	at := submission.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	var workspace string
	if err := query.QueryRowContext(ctx, `
		SELECT w.root_path
		FROM sessions s
		JOIN workspaces w ON w.id = s.workspace_id
		WHERE s.id = ?`,
		value.SessionID,
	).Scan(&workspace); err != nil {
		return kernel.Command{}, fmt.Errorf("resolve task workspace: %w", err)
	}
	rootThreadID := protocol.ThreadID(value.ThreadID)
	if rootThreadID == "" {
		rootThreadID = protocol.ThreadID("thread_task_" + value.SessionID)
	}
	runKind := model.RunKindBackground
	nodeKind := model.NodeKindProcess
	switch value.Executor {
	case ExecutorAgentTurn:
		runKind, nodeKind = model.RunKindAgentTask, model.NodeKindAgentTurn
	case ExecutorWorkflowRun:
		runKind, nodeKind = model.RunKindWorkflow, model.NodeKindProcess
	}
	if source == "automation" {
		runKind = model.RunKindAutomation
	}
	execution := &model.ExecutionSpec{
		TaskID: value.ID, TaskKind: value.Kind,
		ThreadID: value.ThreadID, TurnID: value.TurnID,
		Executor:    value.Executor,
		Payload:     append(json.RawMessage(nil), value.Payload...),
		MaxAttempts: value.MaxAttempts,
	}
	authorityDigest, err := taskAuthorityDigest(workspace, execution)
	if err != nil {
		return kernel.Command{}, err
	}
	return kernel.Command{
		ID: commandID, Kind: kernel.CommandSubmit,
		RunID: runID, At: at,
		Submit: &kernel.SubmitData{
			Kind: runKind, Source: source,
			SessionID: value.SessionID, Workspace: workspace,
			RootThreadID:    rootThreadID,
			AuthorityDigest: authorityDigest,
			Nodes: []model.NodeSpec{{
				ID: executableNodeID, Kind: nodeKind,
				AuthorityDigest: authorityDigest, Execution: execution,
			}},
		},
	}, nil
}

func taskAuthorityDigest(
	workspace string,
	execution *model.ExecutionSpec,
) (string, error) {
	if execution == nil {
		return "", errors.New("task execution authority requires an execution spec")
	}
	encoded, err := json.Marshal(struct {
		Workspace string          `json:"workspace"`
		TaskKind  string          `json:"task_kind"`
		Executor  string          `json:"executor"`
		Payload   json.RawMessage `json:"payload"`
	}{
		Workspace: workspace, TaskKind: execution.TaskKind,
		Executor: execution.Executor, Payload: execution.Payload,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func insertTaskProjection(
	ctx context.Context,
	tx *sql.Tx,
	value Task,
	graph model.Graph,
) error {
	projected, err := projectTask(graph)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO tasks(
			id, session_id, thread_id, turn_id, kind, state, payload_json,
			result_json, lease_owner, lease_expires_at, executor, attempt,
			max_attempts, next_attempt_at, heartbeat_at, lifecycle_sequence,
			failure_reason, terminal_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		projected.ID,
		projected.SessionID,
		sqlkit.NullableString(projected.ThreadID),
		sqlkit.NullableString(projected.TurnID),
		projected.Kind,
		projected.State,
		string(projected.Payload),
		nullableJSON(projected.Result),
		sqlkit.NullableString(projected.LeaseOwner),
		sqlkit.NullableTime(projected.LeaseExpiresAt),
		projected.Executor,
		projected.Attempt,
		projected.MaxAttempts,
		sqlkit.NullableTime(projected.NextAttemptAt),
		sqlkit.NullableTime(projected.HeartbeatAt),
		projected.LifecycleSequence,
		sqlkit.NullableString(projected.FailureReason),
		sqlkit.NullableTime(projected.TerminalAt),
		sqlkit.Timestamp(projected.CreatedAt),
		sqlkit.Timestamp(projected.UpdatedAt),
	)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
		VALUES (?, 1, ?, NULL, ?)`,
		value.ID,
		StateQueued,
		sqlkit.Timestamp(projected.CreatedAt),
	)
	return err
}

func updateTaskProjection(
	ctx context.Context,
	tx *sql.Tx,
	graph model.Graph,
	appendLifecycle bool,
) error {
	projected, err := projectTask(graph)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE tasks SET state = ?, result_json = ?, lease_owner = ?,
			lease_expires_at = ?, attempt = ?, max_attempts = ?,
			next_attempt_at = ?, heartbeat_at = ?, lifecycle_sequence = ?,
			failure_reason = ?, terminal_at = ?, updated_at = ?
		WHERE id = ?`,
		projected.State,
		nullableJSON(projected.Result),
		sqlkit.NullableString(projected.LeaseOwner),
		sqlkit.NullableTime(projected.LeaseExpiresAt),
		projected.Attempt,
		projected.MaxAttempts,
		sqlkit.NullableTime(projected.NextAttemptAt),
		sqlkit.NullableTime(projected.HeartbeatAt),
		projected.LifecycleSequence,
		sqlkit.NullableString(projected.FailureReason),
		sqlkit.NullableTime(projected.TerminalAt),
		sqlkit.Timestamp(projected.UpdatedAt),
		projected.ID,
	)
	if err != nil {
		return err
	}
	if err := sqlkit.RequireAffected(result, 1); err != nil {
		return fmt.Errorf("update executable task projection: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE automation_runs
		SET status = ?, version = version + 1, updated_at = ?
		WHERE task_id = ?`,
		projected.State,
		sqlkit.Timestamp(projected.UpdatedAt),
		projected.ID,
	); err != nil {
		return err
	}
	if !appendLifecycle {
		return nil
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO task_lifecycle(task_id, sequence, state, reason, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		projected.ID,
		projected.LifecycleSequence,
		projected.State,
		sqlkit.NullableString(projected.Reason),
		sqlkit.Timestamp(projected.UpdatedAt),
	)
	return err
}

func projectTask(graph model.Graph) (Task, error) {
	node, exists := graph.Nodes[executableNodeID]
	if !exists || node.Execution == nil || node.Execution.TaskID == "" {
		return Task{}, errors.New("WorkGraph has no executable task node")
	}
	execution := node.Execution
	value := Task{
		ID: execution.TaskID, SessionID: graph.Run.SessionID,
		ThreadID: execution.ThreadID, TurnID: execution.TurnID,
		Kind: execution.TaskKind, State: taskState(node.State),
		LifecycleSequence: graph.Run.Revision,
		Payload:           append(json.RawMessage(nil), execution.Payload...),
		Result:            append(json.RawMessage(nil), node.Result...),
		Reason:            node.Reason, FailureReason: node.Reason,
		Executor: execution.Executor,
		Attempt:  node.AttemptsConsumed, MaxAttempts: execution.MaxAttempts,
		NextAttemptAt: cloneTime(node.RetryAt),
		CreatedAt:     node.CreatedAt, UpdatedAt: node.UpdatedAt,
	}
	var latest *model.Attempt
	for _, attempt := range graph.Attempts {
		if attempt.NodeID != node.ID {
			continue
		}
		if latest == nil || attempt.StartedAt.After(latest.StartedAt) ||
			(attempt.StartedAt.Equal(latest.StartedAt) &&
				attempt.LeaseEpoch > latest.LeaseEpoch) {
			copy := attempt
			latest = &copy
		}
	}
	if latest != nil {
		if !latest.State.Terminal() {
			value.Attempt = latest.Number
			value.LeaseOwner = latest.LeaseOwner
			value.LeaseEpoch = latest.LeaseEpoch
			value.LeaseExpiresAt = cloneTime(latest.LeaseExpiresAt)
			value.HeartbeatAt = cloneTime(latest.HeartbeatAt)
		}
		if latest.EndedAt != nil && isTerminal(value.State) {
			value.TerminalAt = cloneTime(latest.EndedAt)
		}
	}
	if isTerminal(value.State) && value.TerminalAt == nil {
		value.TerminalAt = timePointer(node.UpdatedAt)
	}
	if value.State != StateFailed && value.State != StateCanceled {
		value.FailureReason = ""
	}
	return value, nil
}

func taskState(state protocol.NodeState) State {
	switch state {
	case protocol.NodeStatePending, protocol.NodeStateReady:
		return StateQueued
	case protocol.NodeStateLeased, protocol.NodeStateRunning:
		return StateRunning
	case protocol.NodeStateWaiting, protocol.NodeStateBlocked:
		return StateWaiting
	case protocol.NodeStateSucceeded, protocol.NodeStateSkipped:
		return StateCompleted
	case protocol.NodeStateCanceled:
		return StateCanceled
	default:
		return StateFailed
	}
}

func (r *Repository) executableGraph(
	ctx context.Context,
	taskID string,
) (model.Graph, bool, error) {
	if r.workGraphs == nil {
		return model.Graph{}, false, r.workGraphErr
	}
	graph, err := r.workGraphs.Load(ctx, WorkGraphRunID(taskID))
	if errors.Is(err, kernel.ErrNotFound) {
		return model.Graph{}, false, nil
	}
	return graph, err == nil, err
}

func firstError(values ...error) error {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func timePointer(value time.Time) *time.Time { return &value }
