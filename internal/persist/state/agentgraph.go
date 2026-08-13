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
	ParentPath     string
	ChildID        string
	Path           string
	ThreadID       string
	TurnID         string
	Status         string
	Revision       uint64
	Role           string
	Profile        string
	Stance         string
	Depth          int
	Worktree       string
	Isolated       bool
	Serialized     bool
	BaseRevision   string
	TaskName       string
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
		SELECT workspace_root, session_id, parent_agent_id, parent_path,
		       agent_id, path, thread_id, turn_id, status, revision,
		       role, profile, stance, depth, worktree, isolated, serialized, base_revision,
		       task_name, last_message, source_sequence, updated_at
		FROM agent_nodes
		WHERE workspace_root = ? AND parent_agent_id = ?
		ORDER BY agent_id`, workspaceRoot, parentID)
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
			&edge.WorkspaceRoot, &edge.SessionID, &edge.ParentID, &edge.ParentPath,
			&edge.ChildID, &edge.Path, &edge.ThreadID, &edge.TurnID,
			&edge.Status, &edge.Revision, &edge.Role, &edge.Profile,
			&edge.Stance, &edge.Depth, &edge.Worktree,
			&edge.Isolated, &edge.Serialized, &edge.BaseRevision,
			&edge.TaskName, &edge.LastMessage, &seq, &updated,
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
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	last, err := s.lastReserved(ctx)
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
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate durable agent event: %w", err)
	}
	return s.appendOneLocked(ctx, event)
}

func projectAgentGraphTx(ctx context.Context, tx *sql.Tx, event protocol.Event) error {
	return projectDurableAgentTx(ctx, tx, event)
}
