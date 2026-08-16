package task

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fairqueue"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/sqlkit"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (r *Repository) claimWorkGraphs(
	ctx context.Context,
	request ClaimRequest,
	executors []string,
	workspaceRoot string,
	now time.Time,
	limit int,
) ([]Task, error) {
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(executors)), ", ")
	query := `
		SELECT t.id, t.session_id
		FROM tasks t
		JOIN sessions s ON s.id = t.session_id
		JOIN workspaces w ON w.id = s.workspace_id
		WHERE t.state = ? AND t.executor IN (` + placeholders + `)
			AND w.root_path = ?
			AND (t.next_attempt_at IS NULL OR t.next_attempt_at <= ?)`
	args := []any{StateQueued}
	for _, executor := range executors {
		args = append(args, executor)
	}
	args = append(args, workspaceRoot, sqlkit.Timestamp(now))
	if request.SessionID != "" {
		query += ` AND t.session_id = ?`
		args = append(args, request.SessionID)
	}
	query += ` ORDER BY t.created_at, t.id LIMIT ?`
	candidateLimit := max(limit*64, 256)
	args = append(args, candidateLimit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	candidates, err := sqlkit.ScanAll(rows, func(row sqlkit.RowScanner) (fairqueue.Item, error) {
		var id, sessionID string
		item := fairqueue.Item{}
		err := row.Scan(&id, &sessionID)
		item.ID = id
		item.Workspace = workspaceRoot
		item.Session = sessionID
		item.Run = string(WorkGraphRunID(id))
		return item, err
	})
	if err != nil {
		return nil, err
	}
	selector := r.fair
	if selector == nil {
		selector = fairqueue.NewSelector()
	}
	ids := selector.Select(candidates, limit*2)
	claimed := make([]Task, 0, limit)
	for _, id := range ids {
		if len(claimed) >= limit {
			break
		}
		value, won, err := r.claimWorkGraph(
			ctx,
			id,
			request.Owner,
			request.Lease,
			now,
		)
		if err != nil {
			if errors.Is(err, kernel.ErrConflict) ||
				errors.Is(err, kernel.ErrInvalidTransition) {
				continue
			}
			return nil, err
		}
		if won {
			claimed = append(claimed, value)
		}
	}
	return claimed, nil
}

func (r *Repository) claimWorkGraph(
	ctx context.Context,
	taskID, owner string,
	lease time.Duration,
	now time.Time,
) (Task, bool, error) {
	graph, found, err := r.executableGraph(ctx, taskID)
	if err != nil || !found {
		return Task{}, false, err
	}
	node := graph.Nodes[executableNodeID]
	if node.State != protocol.NodeStateReady ||
		(node.RetryAt != nil && now.Before(*node.RetryAt)) {
		return Task{}, false, nil
	}
	epoch := graph.Run.Revision + 1
	authorityDigest, err := taskAuthorityDigest(
		graph.Run.Workspace,
		node.Execution,
	)
	if err != nil {
		return Task{}, false, err
	}
	attemptID := protocol.AttemptID(fmt.Sprintf(
		"attempt_%s_%d",
		taskID,
		epoch,
	))
	effectID := protocol.EffectID("effect_" + string(attemptID))
	expires := now.Add(lease)
	claimed, err := r.workGraphs.ExecuteProjected(ctx, kernel.Command{
		ID:    fmt.Sprintf("task:claim:%s:%d:%s", taskID, epoch, owner),
		Kind:  kernel.CommandClaimNode,
		RunID: graph.Run.ID, NodeID: executableNodeID,
		AttemptID: attemptID, EffectID: effectID,
		ExpectedRevision: graph.Run.Revision, At: now,
		LeaseOwner: owner, LeaseEpoch: epoch, LeaseExpiresAt: &expires,
		ExpectedAuthorityDigest: authorityDigest,
	}, func(tx *sql.Tx, result kernel.Result) error {
		if err := updateTaskProjection(ctx, tx, result.Graph, true); err != nil {
			return err
		}
		attempt := result.Graph.Attempts[attemptID]
		_, err := tx.ExecContext(ctx, `
			INSERT INTO task_attempts(
				task_id, attempt, owner, status, started_at
			) VALUES (?, ?, ?, ?, ?)`,
			taskID,
			attempt.Number,
			owner,
			AttemptRunning,
			sqlkit.Timestamp(now),
		)
		return err
	})
	if err != nil {
		return Task{}, false, err
	}
	bound, err := r.workGraphs.ExecuteProjected(ctx, kernel.Command{
		ID:    "task:bind:" + string(attemptID),
		Kind:  kernel.CommandBindExecution,
		RunID: graph.Run.ID, AttemptID: attemptID,
		ExpectedRevision: claimed.Graph.Run.Revision, At: now,
		LeaseOwner: owner, LeaseEpoch: epoch,
		Execution: &model.ExecutionRef{
			Kind: "worker", EffectID: effectID,
			ProcessID: owner + ":" + string(attemptID),
		},
	}, func(tx *sql.Tx, result kernel.Result) error {
		return updateTaskProjection(ctx, tx, result.Graph, false)
	})
	if err != nil {
		return Task{}, false, err
	}
	value, err := projectTask(bound.Graph)
	return value, err == nil, err
}

func (r *Repository) HeartbeatAttempt(
	ctx context.Context,
	id, owner string,
	epoch uint64,
	until time.Time,
) error {
	graph, attempt, err := r.activeTaskAttempt(ctx, id, owner, epoch)
	if err != nil {
		return err
	}
	_, err = r.workGraphs.ExecuteProjected(ctx, kernel.Command{
		ID:    fmt.Sprintf("task:heartbeat:%s:%d:%d", id, epoch, until.UnixNano()),
		Kind:  kernel.CommandHeartbeatAttempt,
		RunID: graph.Run.ID, AttemptID: attempt.ID,
		ExpectedRevision: graph.Run.Revision, At: time.Now().UTC(),
		LeaseOwner: owner, LeaseEpoch: epoch, LeaseExpiresAt: &until,
	}, func(tx *sql.Tx, result kernel.Result) error {
		return updateTaskProjection(ctx, tx, result.Graph, false)
	})
	return err
}

func (r *Repository) RecordAttemptExecution(
	ctx context.Context,
	id, owner string,
	epoch uint64,
	threadID, turnID string,
) error {
	graph, attempt, err := r.activeTaskAttempt(ctx, id, owner, epoch)
	if err != nil {
		return err
	}
	if attempt.Execution == nil {
		return errors.New("task attempt has no execution")
	}
	execution := *attempt.Execution
	execution.Kind = "turn"
	execution.ThreadID = protocol.ThreadID(threadID)
	execution.TurnID = protocol.TurnID(turnID)
	execution.ProcessID = ""
	_, err = r.workGraphs.ExecuteProjected(ctx, kernel.Command{
		ID: fmt.Sprintf(
			"task:execution:%s:%d:%s:%s",
			id,
			epoch,
			threadID,
			turnID,
		),
		Kind:  kernel.CommandBindExecution,
		RunID: graph.Run.ID, AttemptID: attempt.ID,
		ExpectedRevision: graph.Run.Revision, At: time.Now().UTC(),
		LeaseOwner: owner, LeaseEpoch: epoch, Execution: &execution,
	}, func(tx *sql.Tx, result kernel.Result) error {
		if err := updateTaskProjection(ctx, tx, result.Graph, false); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE task_attempts SET thread_id = ?, turn_id = ?
			WHERE task_id = ? AND attempt = ? AND owner = ? AND status = ?`,
			sqlkit.NullableString(threadID),
			sqlkit.NullableString(turnID),
			id,
			attempt.Number,
			owner,
			AttemptRunning,
		)
		return err
	})
	return err
}

func (r *Repository) SettleAttempt(
	ctx context.Context,
	id, owner string,
	epoch uint64,
	change Transition,
) (Task, error) {
	graph, attempt, err := r.activeTaskAttempt(ctx, id, owner, epoch)
	if err != nil {
		return Task{}, err
	}
	if change.At.IsZero() {
		change.At = time.Now().UTC()
	}
	var state protocol.NodeState
	switch change.State {
	case StateCompleted:
		state = protocol.NodeStateSucceeded
	case StateFailed:
		state = protocol.NodeStateFailed
	case StateCanceled:
		state = protocol.NodeStateCanceled
	case StateWaiting:
		state = protocol.NodeStateBlocked
	default:
		return Task{}, fmt.Errorf(
			"%w: cannot settle WorkGraph task as %s",
			ErrInvalidTransition,
			change.State,
		)
	}
	result, err := r.workGraphs.ExecuteProjected(ctx, kernel.Command{
		ID:    fmt.Sprintf("task:settle:%s:%d", id, epoch),
		Kind:  kernel.CommandSettleExecution,
		RunID: graph.Run.ID, AttemptID: attempt.ID,
		ExpectedRevision: graph.Run.Revision, At: change.At,
		LeaseOwner: owner, LeaseEpoch: epoch,
		Settlement: &kernel.SettlementData{
			State: state, Reason: change.Reason,
			Result: append(json.RawMessage(nil), change.Result...),
			PermissionDigests: append(
				[]string(nil),
				change.PermissionDigests...,
			),
		},
	}, func(tx *sql.Tx, result kernel.Result) error {
		if err := updateTaskProjection(ctx, tx, result.Graph, true); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE task_attempts
			SET status = ?, reason = ?, ended_at = ?
			WHERE task_id = ? AND attempt = ? AND owner = ? AND status = ?`,
			attemptStatusFor(change.State),
			sqlkit.NullableString(change.Reason),
			sqlkit.Timestamp(change.At),
			id,
			attempt.Number,
			owner,
			AttemptRunning,
		)
		return err
	})
	if err != nil {
		return Task{}, translateWorkGraphConflict(err, id)
	}
	return projectTask(result.Graph)
}

func (r *Repository) ReleaseAttempt(
	ctx context.Context,
	id, owner string,
	epoch uint64,
	reason string,
	at time.Time,
	delay time.Duration,
) (Task, error) {
	graph, attempt, err := r.activeTaskAttempt(ctx, id, owner, epoch)
	if err != nil {
		return Task{}, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	consume := reason != ReasonDraining
	var retryAt *time.Time
	if delay > 0 {
		value := at.Add(delay)
		retryAt = &value
	}
	result, err := r.workGraphs.ExecuteProjected(ctx, kernel.Command{
		ID:    fmt.Sprintf("task:release:%s:%d:%s", id, epoch, reason),
		Kind:  kernel.CommandReleaseAttempt,
		RunID: graph.Run.ID, AttemptID: attempt.ID,
		ExpectedRevision: graph.Run.Revision, At: at,
		LeaseOwner: owner, LeaseEpoch: epoch,
		Reason: reason, RetryAt: retryAt, ConsumeAttempt: consume,
	}, func(tx *sql.Tx, result kernel.Result) error {
		if err := updateTaskProjection(ctx, tx, result.Graph, true); err != nil {
			return err
		}
		if !consume {
			_, err := tx.ExecContext(ctx, `
				DELETE FROM task_attempts
				WHERE task_id = ? AND attempt = ? AND owner = ?`,
				id,
				attempt.Number,
				owner,
			)
			return err
		}
		status := AttemptInterrupted
		if reason == ReasonRetry {
			status = AttemptFailed
		}
		_, err := tx.ExecContext(ctx, `
			UPDATE task_attempts
			SET status = ?, reason = ?, ended_at = ?
			WHERE task_id = ? AND attempt = ? AND owner = ? AND status = ?`,
			status,
			reason,
			sqlkit.Timestamp(at),
			id,
			attempt.Number,
			owner,
			AttemptRunning,
		)
		return err
	})
	if err != nil {
		return Task{}, translateWorkGraphConflict(err, id)
	}
	return projectTask(result.Graph)
}

func (r *Repository) reclaimWorkGraphs(
	ctx context.Context,
	at time.Time,
	backoff Backoff,
) ([]Task, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, lease_owner
		FROM tasks
		WHERE state = ? AND executor IS NOT NULL
			AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?
		ORDER BY lease_expires_at, id`,
		StateRunning,
		sqlkit.Timestamp(at),
	)
	if err != nil {
		return nil, err
	}
	type expired struct{ id, owner string }
	values, err := sqlkit.ScanAll(rows, func(row sqlkit.RowScanner) (expired, error) {
		var value expired
		return value, row.Scan(&value.id, &value.owner)
	})
	if err != nil {
		return nil, err
	}
	var reclaimed []Task
	for _, value := range values {
		graph, attempt, err := r.activeTaskAttempt(ctx, value.id, value.owner, 0)
		if err != nil {
			if errors.Is(err, ErrClaimLost) {
				continue
			}
			return nil, err
		}
		projected, err := r.ReleaseAttempt(
			ctx,
			value.id,
			value.owner,
			attempt.LeaseEpoch,
			ReasonLeaseExpired,
			at,
			backoff.Delay(attempt.Number),
		)
		if err != nil {
			if errors.Is(err, ErrClaimLost) {
				continue
			}
			return nil, err
		}
		_ = graph
		reclaimed = append(reclaimed, projected)
	}
	return reclaimed, nil
}

func (r *Repository) activeTaskAttempt(
	ctx context.Context,
	id, owner string,
	epoch uint64,
) (model.Graph, model.Attempt, error) {
	graph, found, err := r.executableGraph(ctx, id)
	if err != nil {
		return model.Graph{}, model.Attempt{}, err
	}
	if !found {
		return model.Graph{}, model.Attempt{}, ErrNotFound
	}
	for _, attempt := range graph.Attempts {
		if attempt.NodeID != executableNodeID || attempt.State.Terminal() {
			continue
		}
		if attempt.LeaseOwner != owner || (epoch != 0 && attempt.LeaseEpoch != epoch) {
			return model.Graph{}, model.Attempt{}, fmt.Errorf(
				"%w: task %s",
				ErrClaimLost,
				id,
			)
		}
		return graph, attempt, nil
	}
	return model.Graph{}, model.Attempt{}, fmt.Errorf("%w: task %s", ErrClaimLost, id)
}

func (r *Repository) workGraphAttempts(
	ctx context.Context,
	id string,
) ([]Attempt, bool, error) {
	graph, found, err := r.executableGraph(ctx, id)
	if err != nil || !found {
		return nil, found, err
	}
	attempts := make([]Attempt, 0, len(graph.Attempts))
	for _, value := range graph.Attempts {
		if value.Reason == ReasonDraining {
			continue
		}
		status := AttemptRunning
		switch value.State {
		case protocol.AttemptStateSucceeded:
			status = AttemptCompleted
		case protocol.AttemptStateFailed, protocol.AttemptStateLeaseLost:
			status = AttemptFailed
		case protocol.AttemptStateCanceled:
			status = AttemptCanceled
		case protocol.AttemptStateInterrupted:
			status = AttemptInterrupted
		case protocol.AttemptStateIndeterminate:
			status = AttemptWaiting
		}
		attempt := Attempt{
			TaskID: id, Attempt: value.Number,
			Owner: value.LeaseOwner, Status: status,
			Reason: value.Reason, StartedAt: value.StartedAt,
			EndedAt: cloneTime(value.EndedAt),
			PermissionDigests: append(
				[]string(nil),
				value.PermissionDigests...,
			),
		}
		if value.Execution != nil {
			attempt.ThreadID = string(value.Execution.ThreadID)
			attempt.TurnID = string(value.Execution.TurnID)
		}
		attempts = append(attempts, attempt)
	}
	sortAttempts(attempts)
	return attempts, true, nil
}

func translateWorkGraphConflict(err error, id string) error {
	if errors.Is(err, kernel.ErrConflict) ||
		errors.Is(err, kernel.ErrInvalidTransition) {
		return fmt.Errorf("%w: task %s", ErrClaimLost, id)
	}
	return err
}

func sortAttempts(values []Attempt) {
	for left := 0; left < len(values); left++ {
		for right := left + 1; right < len(values); right++ {
			if values[right].Attempt < values[left].Attempt {
				values[left], values[right] = values[right], values[left]
			}
		}
	}
}
