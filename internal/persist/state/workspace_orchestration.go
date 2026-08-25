package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

// WorkspaceOrchestrationStore limits terminal recovery to one Workspace.
type WorkspaceOrchestrationStore struct {
	*orchestrationstore.Store
	database      *sqlitestate.Store
	workspaceRoot string
}

func OpenWorkspaceOrchestrationStore(
	ctx context.Context,
	database *sqlitestate.Store,
	workspaceRoot string,
) (*WorkspaceOrchestrationStore, error) {
	store, err := orchestrationstore.Open(ctx, database)
	if err != nil {
		return nil, err
	}
	return &WorkspaceOrchestrationStore{
		Store: store, database: database, workspaceRoot: workspaceRoot,
	}, nil
}

func (s *WorkspaceOrchestrationStore) PendingTerminalEffects(
	ctx context.Context,
	limit int,
) ([]orchestrationstore.OutboxEntry, error) {
	if s.workspaceRoot == "" {
		return s.Store.PendingTerminalEffects(ctx, limit)
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("work graph outbox limit must be between 1 and 1000")
	}
	rows, err := s.database.DB().QueryContext(ctx, `
		SELECT outbox.effect_json, outbox.published
		FROM work_outbox AS outbox
		JOIN work_runs AS run ON run.run_id = outbox.run_id
		WHERE outbox.published = 0
			AND outbox.kind = ?
			AND json_extract(run.aggregate_json, '$.run.workspace') = ?
		ORDER BY outbox.created_at, outbox.effect_id
		LIMIT ?`,
		model.EffectPublishTerminal,
		s.workspaceRoot,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]orchestrationstore.OutboxEntry, 0)
	for rows.Next() {
		var encoded []byte
		var published bool
		if err := rows.Scan(&encoded, &published); err != nil {
			return nil, err
		}
		var effect model.Effect
		if err := json.Unmarshal(encoded, &effect); err != nil {
			return nil, err
		}
		result = append(result, orchestrationstore.OutboxEntry{
			Effect: effect, Published: published,
		})
	}
	return result, rows.Err()
}
