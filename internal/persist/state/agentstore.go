package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (s *Store) ListAgentMessages(
	ctx context.Context, workspaceRoot, to string,
) ([]subagent.Message, error) {
	return s.listAgentMessages(ctx, workspaceRoot, to, false)
}

func (s *Store) ListUnpublishedAgentCompletions(
	ctx context.Context, workspaceRoot string,
) ([]subagent.Message, error) {
	return s.listAgentMessages(ctx, workspaceRoot, "", true)
}

func (s *Store) listAgentMessages(
	ctx context.Context, workspaceRoot, to string, unpublished bool,
) ([]subagent.Message, error) {
	if workspaceRoot == "" {
		return nil, fmt.Errorf("agent mailbox workspace root is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	query := `
		SELECT id, sequence, from_agent_id, to_agent_id, kind, payload_ref,
		       body, trigger_turn, created_at, delivered_at
		FROM agent_messages
		WHERE workspace_root = ?`
	args := []any{workspaceRoot}
	if unpublished {
		query += ` AND kind = 'completion' AND published_at IS NULL`
	} else if to != "" {
		query += ` AND to_agent_id = ? AND delivered_at IS NULL`
		args = append(args, to)
	}
	query += ` ORDER BY to_agent_id, sequence`
	rows, err := s.sqlite.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var messages []subagent.Message
	for rows.Next() {
		var message subagent.Message
		var kind, created string
		var delivered sql.NullString
		if err := rows.Scan(
			&message.ID, &message.Sequence, &message.From, &message.To,
			&kind, &message.PayloadRef, &message.Body, &message.TriggerTurn,
			&created, &delivered,
		); err != nil {
			return nil, err
		}
		message.Kind = subagent.MessageKind(kind)
		if message.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		if delivered.Valid {
			value, parseErr := time.Parse(time.RFC3339Nano, delivered.String)
			if parseErr != nil {
				return nil, parseErr
			}
			message.DeliveredAt = &value
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) LoadAgentResult(
	ctx context.Context, workspaceRoot, agentID string,
) (subagent.Result, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return subagent.Result{}, false, ErrClosed
	}
	var raw []byte
	err := s.sqlite.DB().QueryRowContext(ctx, `
		SELECT result_json FROM agent_results
		WHERE workspace_root = ? AND agent_id = ?`,
		workspaceRoot, agentID,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return subagent.Result{}, false, nil
	}
	if err != nil {
		return subagent.Result{}, false, err
	}
	var result subagent.Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return subagent.Result{}, false, err
	}
	return result, true, nil
}

func (s *Store) LoadAgentBudget(
	ctx context.Context, workspaceRoot string,
) (subagent.BudgetLedger, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return subagent.BudgetLedger{}, ErrClosed
	}
	var ledger subagent.BudgetLedger
	err := s.sqlite.DB().QueryRowContext(ctx, `
		SELECT COALESCE(SUM(reserved_tokens), 0),
		       COALESCE(SUM(spent_tokens), 0),
		       COALESCE(SUM(reserved_microunits), 0),
		       COALESCE(SUM(spent_microunits), 0),
		       COALESCE(SUM(reserved_slots), 0)
		FROM agent_budget_ledger WHERE workspace_root = ?`,
		workspaceRoot,
	).Scan(
		&ledger.ReservedTokens, &ledger.SpentTokens,
		&ledger.ReservedMicros, &ledger.SpentMicros, &ledger.ReservedSlots,
	)
	return ledger, err
}

func (s *Store) PlanAgentReconciliation(
	ctx context.Context, workspaceRoot string,
) ([]subagent.GraphTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	rows, err := s.sqlite.DB().QueryContext(ctx, `
		SELECT agent_id, path, thread_id, turn_id, status, revision
		FROM agent_nodes
		WHERE workspace_root = ?
		  AND status IN ('requested', 'starting', 'running', 'waiting')
		ORDER BY depth, agent_id`, workspaceRoot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type node struct {
		id, path, threadID, turnID, status string
		revision                           uint64
	}
	var nodes []node
	for rows.Next() {
		var value node
		if err := rows.Scan(
			&value.id, &value.path, &value.threadID, &value.turnID,
			&value.status, &value.revision,
		); err != nil {
			return nil, err
		}
		nodes = append(nodes, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	var transitions []subagent.GraphTransition
	for _, value := range nodes {
		var turnID, turnStatus string
		err := s.sqlite.DB().QueryRowContext(ctx, `
			SELECT id, status FROM turns
			WHERE thread_id = ? ORDER BY ordinal DESC LIMIT 1`,
			value.threadID,
		).Scan(&turnID, &turnStatus)
		if err == sql.ErrNoRows {
			turnID, turnStatus = "", ""
		} else if err != nil {
			return nil, err
		}
		revision := value.revision
		appendTransition := func(status subagent.Status, reason string, result *subagent.Result) {
			transitions = append(transitions, subagent.GraphTransition{
				AgentID: value.id, Path: value.path,
				ExpectedRevision: revision, Status: status, TurnID: turnID,
				Message: reason, OperationID: fmt.Sprintf(
					"reconcile:%s:%d", value.id, revision+1,
				),
				Actor: "startup_reconciler", Reason: reason,
				Result: result, CreatedAt: time.Now().UTC(),
			})
			revision++
		}
		current := subagent.Status(value.status)
		if turnStatus == "active" {
			if current == subagent.StatusRequested {
				appendTransition(subagent.StatusStarting, "rebound accepted durable turn", nil)
				current = subagent.StatusStarting
			}
			if current == subagent.StatusStarting {
				appendTransition(subagent.StatusRunning, "rebound active durable turn", nil)
			}
			continue
		}
		reason := "no durable child turn survived restart"
		if turnID != "" {
			reason = fmt.Sprintf(
				"durable turn %s reached %s before agent result commit",
				turnID, turnStatus,
			)
		}
		result := &subagent.Result{
			AgentID: value.id, ThreadID: value.threadID, TurnID: turnID,
			Status: subagent.StatusFailed, Summary: reason,
		}
		appendTransition(subagent.StatusFailed, reason, result)
	}
	return transitions, nil
}

func projectDurableAgentTx(ctx context.Context, tx *sql.Tx, event protocol.Event) error {
	switch data := event.Data.(type) {
	case *protocol.AgentSpawnedData:
		return projectAgentSpawnTx(ctx, tx, event, data)
	case *protocol.AgentStatusData:
		return projectAgentTransitionTx(ctx, tx, event, data)
	case *protocol.AgentMessageData:
		return projectAgentMessageTx(ctx, tx, event, data)
	default:
		return nil
	}
}

func projectAgentSpawnTx(
	ctx context.Context, tx *sql.Tx, event protocol.Event, data *protocol.AgentSpawnedData,
) error {
	edge := subagent.GraphEdge{
		ParentID: data.ParentID, ChildID: data.AgentID,
		Path: "/root/" + data.AgentID, ParentPath: "/root",
		Workspace: data.WorkspaceRoot, SessionID: data.SessionID,
		ThreadID: subagent.ThreadIDFor(data.AgentID),
		Status:   subagent.StatusRequested, Revision: 1,
		Role: subagent.Role(data.Role), Profile: data.Profile,
		Stance: subagent.Stance(data.Stance), Depth: data.Depth,
		Worktree: data.Worktree,
	}
	if len(data.Detail) > 0 {
		if err := json.Unmarshal(data.Detail, &edge); err != nil {
			return fmt.Errorf("decode agent spawn detail: %w", err)
		}
	}
	if edge.Path == "" || edge.Revision != 1 ||
		edge.Status != subagent.StatusRequested {
		return fmt.Errorf("invalid durable agent spawn for %s", data.AgentID)
	}
	now := timestamp(event.CreatedAt)
	result, err := tx.ExecContext(ctx, `
		INSERT OR IGNORE INTO agent_nodes(
			workspace_root, session_id, agent_id, path,
			parent_agent_id, parent_path, thread_id, turn_id,
			status, revision, role, profile, stance, depth,
			worktree, isolated, serialized, base_revision, task_name, last_message,
			operation_id, actor, reason, event_id, source_sequence, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, ?, '', ?, ?, ?)`,
		edge.Workspace, edge.SessionID, edge.ChildID, edge.Path,
		edge.ParentID, edge.ParentPath, edge.ThreadID, edge.TurnID,
		edge.Status, edge.Revision, edge.Role, edge.Profile, edge.Stance, edge.Depth,
		edge.Worktree, edge.Isolated, edge.Serialized, edge.BaseRev, edge.TaskName,
		"agent:"+edge.ChildID+":1", "delegation", string(event.ID),
		int64(event.Sequence), now,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var path string
		if err := tx.QueryRowContext(ctx, `
			SELECT path FROM agent_nodes
			WHERE workspace_root = ? AND agent_id = ?`,
			edge.Workspace, edge.ChildID,
		).Scan(&path); err != nil || path != edge.Path {
			return fmt.Errorf("agent spawn conflicts with existing node %s", edge.ChildID)
		}
		return nil
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_budget_ledger(
			workspace_root, agent_id, reserved_slots, source_sequence, updated_at
		) VALUES (?, ?, 1, ?, ?)`,
		edge.Workspace, edge.ChildID, int64(event.Sequence), now,
	)
	return err
}

func projectAgentTransitionTx(
	ctx context.Context, tx *sql.Tx, event protocol.Event, data *protocol.AgentStatusData,
) error {
	var transition subagent.GraphTransition
	if len(data.Detail) > 0 {
		if err := json.Unmarshal(data.Detail, &transition); err != nil {
			return fmt.Errorf("decode agent transition detail: %w", err)
		}
	}
	var current string
	var revision uint64
	var sourceSequence uint64
	var operationID string
	var path, parentID, sessionID string
	if err := tx.QueryRowContext(ctx, `
		SELECT status, revision, source_sequence, operation_id,
		       path, parent_agent_id, session_id
		FROM agent_nodes
		WHERE workspace_root = ? AND agent_id = ?`,
		data.WorkspaceRoot, data.AgentID,
	).Scan(
		&current, &revision, &sourceSequence, &operationID,
		&path, &parentID, &sessionID,
	); err != nil {
		return err
	}
	if uint64(event.Sequence) <= sourceSequence {
		return nil
	}
	if transition.AgentID == "" {
		transition = subagent.GraphTransition{
			AgentID: data.AgentID, ExpectedRevision: revision,
			Status: subagent.Status(data.Status), Message: data.Message,
			OperationID: fmt.Sprintf("legacy:%s:%d", data.AgentID, revision+1),
			Actor:       "legacy", CreatedAt: event.CreatedAt,
		}
	}
	if revision == transition.ExpectedRevision+1 &&
		operationID == transition.OperationID {
		return nil
	}
	if revision != transition.ExpectedRevision {
		return fmt.Errorf(
			"agent %s revision conflict: expected %d, actual %d",
			data.AgentID, transition.ExpectedRevision, revision,
		)
	}
	if !subagent.CanTransition(subagent.Status(current), transition.Status) {
		return fmt.Errorf(
			"agent %s cannot transition from %s to %s",
			data.AgentID, current, transition.Status,
		)
	}
	next := revision + 1
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_nodes SET
			status = ?, revision = ?, turn_id = CASE
				WHEN ? != '' THEN ? ELSE turn_id END,
			last_message = CASE WHEN ? != '' THEN ? ELSE last_message END,
			operation_id = ?, actor = ?, reason = ?, event_id = ?,
			source_sequence = ?, updated_at = ?
		WHERE workspace_root = ? AND agent_id = ? AND revision = ?`,
		transition.Status, next, transition.TurnID, transition.TurnID,
		transition.Message, transition.Message,
		transition.OperationID, transition.Actor, transition.Reason, string(event.ID),
		int64(event.Sequence), timestamp(event.CreatedAt),
		data.WorkspaceRoot, data.AgentID, revision,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("agent %s CAS transition failed", data.AgentID)
	}
	if err := projectAgentBudgetTransitionTx(
		ctx, tx, event, data.WorkspaceRoot,
		subagent.Status(current), transition,
	); err != nil {
		return err
	}
	if transition.Result != nil {
		raw, marshalErr := json.Marshal(transition.Result)
		if marshalErr != nil {
			return marshalErr
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO agent_results(
				workspace_root, agent_id, turn_id, result_json,
				receipt_ref, source_sequence, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(workspace_root, agent_id) DO UPDATE SET
				turn_id=excluded.turn_id, result_json=excluded.result_json,
				receipt_ref=excluded.receipt_ref,
				source_sequence=excluded.source_sequence, created_at=excluded.created_at`,
			data.WorkspaceRoot, data.AgentID, transition.Result.TurnID, raw,
			transition.Result.Context.Digest, int64(event.Sequence),
			timestamp(event.CreatedAt),
		)
		if err != nil {
			return err
		}
		if transition.CompletionMessage == nil {
			message, buildErr := recoveryCompletionMessageTx(
				ctx, tx, data.WorkspaceRoot, path, parentID, sessionID,
				next, *transition.Result, event.CreatedAt,
			)
			if buildErr != nil {
				return buildErr
			}
			transition.CompletionMessage = &message
		}
	}
	if transition.CompletionMessage != nil {
		return insertAgentMessageTx(
			ctx, tx, event, data.WorkspaceRoot, *transition.CompletionMessage, false,
		)
	}
	return nil
}

func recoveryCompletionMessageTx(
	ctx context.Context,
	tx *sql.Tx,
	workspace, path, parentID, sessionID string,
	revision uint64,
	result subagent.Result,
	createdAt time.Time,
) (subagent.Message, error) {
	target := parentID
	if target == "" {
		target = "root"
	}
	var sequence uint64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(sequence), 0) + 1 FROM agent_messages
		WHERE workspace_root = ? AND to_agent_id = ?`,
		workspace, target,
	).Scan(&sequence); err != nil {
		return subagent.Message{}, err
	}
	paths := result.WritePaths()
	if len(paths) > 64 {
		paths = paths[:64]
	}
	envelope := subagent.CompletionEnvelope{
		AgentPath: path, Status: result.Status, Summary: result.Digest(),
		ResultRef: fmt.Sprintf(
			"agent-result://%s/%s/%d", sessionID, result.AgentID, revision,
		),
		ReceiptRef:   result.Context.Digest,
		ChangedPaths: paths,
		Verification: result.Verification, Usage: result.Usage,
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return subagent.Message{}, err
	}
	return subagent.Message{
		ID:       fmt.Sprintf("message-%s-%d", target, sequence),
		Sequence: sequence, From: result.AgentID, To: target,
		Kind: subagent.MessageCompletion, PayloadRef: envelope.ResultRef,
		Body: body, CreatedAt: createdAt,
	}, nil
}

func projectAgentBudgetTransitionTx(
	ctx context.Context,
	tx *sql.Tx,
	event protocol.Event,
	workspace string,
	from subagent.Status,
	transition subagent.GraphTransition,
) error {
	slotDelta := 0
	switch {
	case !subagent.OccupiesSlot(from) && subagent.OccupiesSlot(transition.Status):
		slotDelta = 1
	case subagent.OccupiesSlot(from) && !subagent.OccupiesSlot(transition.Status):
		slotDelta = -1
	}
	var spentTokens, spentMicros uint64
	if transition.Result != nil {
		spentTokens = transition.Result.Usage.Tokens()
		spentMicros = transition.Result.Usage.CostMicrounits
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE agent_budget_ledger SET
			reserved_slots = reserved_slots + ?,
			spent_tokens = spent_tokens + ?,
			spent_microunits = spent_microunits + ?,
			released = CASE WHEN reserved_slots + ? = 0 THEN 1 ELSE 0 END,
			source_sequence = ?, updated_at = ?
		WHERE workspace_root = ? AND agent_id = ?`,
		slotDelta, spentTokens, spentMicros, slotDelta,
		int64(event.Sequence), timestamp(event.CreatedAt),
		workspace, transition.AgentID,
	)
	return err
}

func projectAgentMessageTx(
	ctx context.Context, tx *sql.Tx, event protocol.Event, data *protocol.AgentMessageData,
) error {
	var message subagent.Message
	if err := json.Unmarshal(data.Body, &message); err != nil || message.ID == "" {
		message = subagent.Message{
			ID:       fmt.Sprintf("message-%s-%d", data.To, data.Sequence),
			Sequence: data.Sequence, From: data.From, To: data.To,
			Kind: subagent.MessageContext, Body: data.Body,
			CreatedAt: event.CreatedAt,
		}
	}
	if message.DeliveredAt != nil {
		result, err := tx.ExecContext(ctx, `
			UPDATE agent_messages SET
				published_at = COALESCE(published_at, ?),
				delivered_at = ?, source_sequence = ?
			WHERE workspace_root = ? AND id = ? AND delivered_at IS NULL`,
			timestamp(event.CreatedAt), timestamp(*message.DeliveredAt),
			int64(event.Sequence),
			data.WorkspaceRoot, message.ID,
		)
		if err != nil {
			return err
		}
		if affected, _ := result.RowsAffected(); affected == 0 {
			var delivered sql.NullString
			if err := tx.QueryRowContext(ctx, `
				SELECT delivered_at FROM agent_messages
				WHERE workspace_root = ? AND id = ?`,
				data.WorkspaceRoot, message.ID,
			).Scan(&delivered); err != nil || !delivered.Valid {
				return fmt.Errorf("agent message %s delivery is missing", message.ID)
			}
		}
		return nil
	}
	return insertAgentMessageTx(ctx, tx, event, data.WorkspaceRoot, message, true)
}

func insertAgentMessageTx(
	ctx context.Context,
	tx *sql.Tx,
	event protocol.Event,
	workspace string,
	message subagent.Message,
	published bool,
) error {
	var publishedAt any
	if published {
		publishedAt = timestamp(event.CreatedAt)
	}
	_, err := tx.ExecContext(ctx, `
		INSERT INTO agent_messages(
			workspace_root, id, sequence, from_agent_id, to_agent_id,
			kind, payload_ref, body, trigger_turn, created_at,
			published_at, delivered_at, source_sequence
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace_root, id) DO UPDATE SET
			published_at = COALESCE(agent_messages.published_at, excluded.published_at),
			source_sequence = MAX(agent_messages.source_sequence, excluded.source_sequence)`,
		workspace, message.ID, message.Sequence, message.From, message.To,
		message.Kind, message.PayloadRef, []byte(message.Body), message.TriggerTurn,
		timestamp(message.CreatedAt), publishedAt, nullableTimestamp(message.DeliveredAt),
		int64(event.Sequence),
	)
	return err
}

func nullableTimestamp(value *time.Time) any {
	if value == nil {
		return nil
	}
	return timestamp(*value)
}
