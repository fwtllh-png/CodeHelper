package state

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// AgentSpawnEdge is the projected durable child row for restart List.
type AgentSpawnEdge struct {
	WorkspaceRoot  string
	SessionID      string
	ParentID       string
	ChildID        string
	Status         string
	Role           string
	Profile        string
	Stance         string
	Depth          int
	Worktree       string
	LastMessage    string
	SourceSequence protocol.Cursor
	UpdatedAt      time.Time
}

// ListAgentChildren returns projected spawn edges for parentID ("" = session roots).
func (s *Store) ListAgentChildren(
	ctx context.Context, workspaceRoot, parentID string,
) ([]AgentSpawnEdge, error) {
	if workspaceRoot == "" {
		return nil, fmt.Errorf("agent graph workspace root is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	rows, err := s.sqlite.DB().QueryContext(ctx, `
		SELECT workspace_root, session_id, parent_agent_id, child_agent_id,
		       status, role, profile, stance,
		       depth, worktree, last_message, source_sequence, updated_at
		FROM agent_spawn_edges
		WHERE workspace_root = ? AND parent_agent_id = ?
		ORDER BY child_agent_id`, workspaceRoot, parentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentSpawnEdge
	for rows.Next() {
		var edge AgentSpawnEdge
		var updated string
		var seq int64
		if err := rows.Scan(
			&edge.WorkspaceRoot, &edge.SessionID, &edge.ParentID, &edge.ChildID,
			&edge.Status, &edge.Role, &edge.Profile,
			&edge.Stance, &edge.Depth, &edge.Worktree, &edge.LastMessage, &seq, &updated,
		); err != nil {
			return nil, err
		}
		edge.SourceSequence = protocol.Cursor(seq)
		if edge.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
			return nil, err
		}
		out = append(out, edge)
	}
	return out, rows.Err()
}

// AppendAgentEvent allocates the next durable sequence and appends an agent.* event.
func (s *Store) AppendAgentEvent(ctx context.Context, data protocol.EventData) error {
	if data == nil {
		return fmt.Errorf("agent event data is required")
	}
	last, err := s.LastSequence(ctx)
	if err != nil {
		return err
	}
	itemID := protocol.ItemID(fmt.Sprintf("item_agent_%d", last+1))
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: last + 1, OperationID: "op_agent_graph",
		ThreadID: "thread_agent_graph", TurnID: "turn_agent_graph", ItemID: itemID,
	}, data)
	if err != nil {
		return err
	}
	return s.Append(ctx, event)
}

func projectAgentGraphTx(ctx context.Context, tx *sql.Tx, event protocol.Event) error {
	switch data := event.Data.(type) {
	case *protocol.AgentSpawnedData:
		now := timestamp(event.CreatedAt)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO agent_spawn_edges(
				workspace_root, session_id, parent_agent_id, child_agent_id,
				status, role, profile, stance,
				depth, worktree, last_message, updated_at, source_sequence
			) VALUES (?, ?, ?, ?, 'pending_init', ?, ?, ?, ?, ?, '', ?, ?)
			ON CONFLICT(workspace_root, child_agent_id) DO UPDATE SET
				session_id=excluded.session_id,
				parent_agent_id=excluded.parent_agent_id,
				role=excluded.role, profile=excluded.profile, stance=excluded.stance,
				depth=excluded.depth, worktree=excluded.worktree,
				updated_at=excluded.updated_at, source_sequence=excluded.source_sequence`,
			data.WorkspaceRoot, data.SessionID, data.ParentID, data.AgentID,
			data.Role, data.Profile, data.Stance,
			data.Depth, data.Worktree, now, int64(event.Sequence),
		)
		return err
	case *protocol.AgentStatusData:
		_, err := tx.ExecContext(ctx, `
			UPDATE agent_spawn_edges
			SET status = ?, last_message = CASE
				WHEN ? != '' THEN ? ELSE last_message END,
			    updated_at = ?, source_sequence = ?
			WHERE workspace_root = ? AND child_agent_id = ?`,
			data.Status, data.Message, data.Message,
			timestamp(event.CreatedAt), int64(event.Sequence),
			data.WorkspaceRoot, data.AgentID,
		)
		return err
	default:
		return nil
	}
}
