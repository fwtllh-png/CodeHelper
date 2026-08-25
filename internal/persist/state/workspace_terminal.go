package state

import (
	"context"
	"errors"
	"strings"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

// WorkspaceTerminalStore scopes terminal outbox recovery while delegating
// turn-specific operations to the shared SQLite repository.
type WorkspaceTerminalStore struct {
	*turnstate.Store
	database      *sqlitestate.Store
	workspaceRoot string
}

func NewWorkspaceTerminalStore(
	database *sqlitestate.Store,
	workspaceRoot string,
) *WorkspaceTerminalStore {
	if strings.TrimSpace(workspaceRoot) != "" {
		workspaceRoot = physicalWorkspaceRoot(workspaceRoot)
	}
	return &WorkspaceTerminalStore{
		Store:    turnstate.NewSQLiteRepository(database),
		database: database, workspaceRoot: workspaceRoot,
	}
}

func (s *WorkspaceTerminalStore) PendingTerminalProjections(
	ctx context.Context,
) ([]turnkernel.PendingTerminalProjection, error) {
	if s.workspaceRoot == "" {
		return s.Store.PendingTerminalProjections(ctx)
	}
	rows, err := s.database.DB().QueryContext(ctx, `
		SELECT DISTINCT outbox.turn_id
		FROM turn_terminal_outbox AS outbox
		JOIN turns AS turn ON turn.id = outbox.turn_id
		JOIN threads AS thread ON thread.id = turn.thread_id
		JOIN sessions AS session ON session.id = thread.session_id
		JOIN workspaces AS workspace ON workspace.id = session.workspace_id
		WHERE outbox.published = 0 AND workspace.root_path = ?
		ORDER BY outbox.turn_id`,
		s.workspaceRoot,
	)
	if err != nil {
		return nil, err
	}
	var turnIDs []string
	for rows.Next() {
		var turnID string
		if err := rows.Scan(&turnID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		turnIDs = append(turnIDs, turnID)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	projections := make([]turnkernel.PendingTerminalProjection, 0, len(turnIDs))
	for _, turnID := range turnIDs {
		envelope, _, err := s.LoadTerminal(ctx, turnID)
		if err != nil {
			return nil, err
		}
		entries, err := s.PendingOutbox(ctx, turnID)
		if err != nil {
			return nil, err
		}
		if len(entries) != 0 {
			projections = append(
				projections,
				turnkernel.PendingTerminalProjection{
					Envelope: envelope,
					Entries:  entries,
				},
			)
		}
	}
	return projections, nil
}
