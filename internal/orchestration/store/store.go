// Package store persists WorkGraph aggregates, facts, command receipts, and
// effect outbox rows in one SQLite transaction.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/sqlkit"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrCommandConflict = errors.New("work graph command identity conflict")
	ErrSnapshotDrift   = errors.New("work graph snapshot differs from fact replay")
)

type Store struct {
	db *sql.DB
}

type Projection func(*sql.Tx, kernel.Result) error

type OutboxEntry struct {
	Effect    model.Effect
	Published bool
}

type Audit struct {
	RunID          protocol.RunID `json:"run_id"`
	SnapshotDigest string         `json:"snapshot_digest"`
	ReplayDigest   string         `json:"replay_digest"`
	Drift          bool           `json:"drift"`
	Revision       uint64         `json:"revision"`
	PendingEffects int            `json:"pending_effects"`
}

func Open(ctx context.Context, sqlite *sqlitestate.Store) (*Store, error) {
	if sqlite == nil || sqlite.DB() == nil {
		return nil, errors.New("work graph SQLite store is required")
	}
	value := &Store{db: sqlite.DB()}
	if err := value.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	return value, nil
}

func OpenDB(ctx context.Context, db *sql.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("work graph database is required")
	}
	value := &Store{db: db}
	if err := value.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	return value, nil
}

func (s *Store) EnsureSchema(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("work graph database is required")
	}
	_, err := s.db.ExecContext(ctx, workGraphSchema)
	if err != nil {
		return fmt.Errorf("create work graph schema: %w", err)
	}
	return nil
}

func (s *Store) Execute(
	ctx context.Context,
	command kernel.Command,
) (kernel.Result, error) {
	if s == nil || s.db == nil {
		return kernel.Result{}, errors.New("work graph database is required")
	}
	var result kernel.Result
	err := sqlkit.WithTx(ctx, s.db, nil, func(tx *sql.Tx) error {
		var executeErr error
		result, executeErr = s.ExecuteTx(ctx, tx, command, nil)
		return executeErr
	})
	return result, err
}

func (s *Store) ExecuteProjected(
	ctx context.Context,
	command kernel.Command,
	project Projection,
) (kernel.Result, error) {
	if s == nil || s.db == nil {
		return kernel.Result{}, errors.New("work graph database is required")
	}
	var result kernel.Result
	err := sqlkit.WithTx(ctx, s.db, nil, func(tx *sql.Tx) error {
		var executeErr error
		result, executeErr = s.ExecuteTx(ctx, tx, command, project)
		return executeErr
	})
	return result, err
}

func (s *Store) ExecuteTx(
	ctx context.Context,
	tx *sql.Tx,
	command kernel.Command,
	project Projection,
) (kernel.Result, error) {
	if s == nil || s.db == nil || tx == nil {
		return kernel.Result{}, errors.New("work graph store and transaction are required")
	}
	digest, err := commandDigest(command)
	if err != nil {
		return kernel.Result{}, err
	}
	duplicate, found, err := loadCommand(ctx, tx, command.ID, digest)
	if err != nil {
		return kernel.Result{}, err
	}
	if found {
		duplicate.Duplicate = true
		return duplicate, nil
	}
	current, found, err := loadGraph(ctx, tx, command.RunID)
	if err != nil {
		return kernel.Result{}, err
	}
	if !found {
		current = model.Empty(command.RunID)
	}
	existingEffects := make(map[protocol.EffectID]bool, len(current.Effects))
	for id := range current.Effects {
		existingEffects[id] = true
	}
	next, err := kernel.ReduceOwned(current, command)
	if err != nil {
		return kernel.Result{}, err
	}
	if err := persistTransition(
		ctx,
		tx,
		command,
		digest,
		existingEffects,
		next,
	); err != nil {
		return kernel.Result{}, err
	}
	if project != nil {
		if err := project(tx, next); err != nil {
			return kernel.Result{}, err
		}
	}
	return next, nil
}

func (s *Store) Load(
	ctx context.Context,
	runID protocol.RunID,
) (model.Graph, error) {
	if s == nil || s.db == nil {
		return model.Graph{}, errors.New("work graph database is required")
	}
	graph, found, err := loadGraph(ctx, s.db, runID)
	if err != nil {
		return model.Graph{}, err
	}
	if !found {
		return model.Graph{}, kernel.ErrNotFound
	}
	return graph, nil
}

func (s *Store) Rebuild(
	ctx context.Context,
	runID protocol.RunID,
) (model.Graph, error) {
	var rebuilt model.Graph
	err := sqlkit.WithTx(
		ctx,
		s.db,
		&sql.TxOptions{ReadOnly: true},
		func(tx *sql.Tx) error {
			snapshot, found, err := loadGraph(ctx, tx, runID)
			if err != nil {
				return err
			}
			if !found {
				return kernel.ErrNotFound
			}
			rebuilt, err = replayGraph(ctx, tx, runID)
			if err != nil {
				return err
			}
			snapshotDigest, err := graphDigest(snapshot)
			if err != nil {
				return err
			}
			replayDigest, err := graphDigest(rebuilt)
			if err != nil {
				return err
			}
			if snapshotDigest != replayDigest {
				return ErrSnapshotDrift
			}
			return nil
		},
	)
	return rebuilt, err
}

type graphReader interface {
	queryer
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func replayGraph(
	ctx context.Context,
	query graphReader,
	runID protocol.RunID,
) (model.Graph, error) {
	rows, err := query.QueryContext(ctx, `
		SELECT fact_json
		FROM work_facts
		WHERE run_id = ?
		ORDER BY sequence`,
		runID,
	)
	if err != nil {
		return model.Graph{}, err
	}
	defer rows.Close()
	rebuilt := model.Empty(runID)
	for rows.Next() {
		var encoded []byte
		if err := rows.Scan(&encoded); err != nil {
			return model.Graph{}, err
		}
		var fact kernel.Fact
		if err := json.Unmarshal(encoded, &fact); err != nil {
			return model.Graph{}, fmt.Errorf("decode work graph fact: %w", err)
		}
		if err := kernel.ApplyFact(&rebuilt, fact); err != nil {
			return model.Graph{}, err
		}
	}
	if err := rows.Err(); err != nil {
		return model.Graph{}, err
	}
	if err := rebuilt.Validate(); err != nil {
		return model.Graph{}, fmt.Errorf("rebuilt work graph is invalid: %w", err)
	}
	return rebuilt, nil
}

func (s *Store) List(ctx context.Context, limit int) ([]model.Graph, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("work graph database is required")
	}
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("work graph list limit must be between 1 and 1000")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT aggregate_json
		FROM work_runs
		ORDER BY updated_at DESC, run_id
		LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, err
	}
	return sqlkit.ScanAll(rows, func(row sqlkit.RowScanner) (model.Graph, error) {
		var encoded []byte
		if err := row.Scan(&encoded); err != nil {
			return model.Graph{}, err
		}
		var graph model.Graph
		if err := json.Unmarshal(encoded, &graph); err != nil {
			return model.Graph{}, err
		}
		if err := graph.Validate(); err != nil {
			return model.Graph{}, err
		}
		return graph, nil
	})
}

func (s *Store) Audit(
	ctx context.Context,
	runID protocol.RunID,
) (Audit, error) {
	var audit Audit
	err := sqlkit.WithTx(
		ctx,
		s.db,
		&sql.TxOptions{ReadOnly: true},
		func(tx *sql.Tx) error {
			var err error
			audit, err = auditGraph(ctx, tx, runID)
			return err
		},
	)
	return audit, err
}

// RepairSnapshot replaces only the rebuildable aggregate cache. Ordered facts,
// command receipts, and the effect outbox remain untouched.
func (s *Store) RepairSnapshot(
	ctx context.Context,
	runID protocol.RunID,
) (Audit, error) {
	var audit Audit
	err := sqlkit.WithTx(ctx, s.db, nil, func(tx *sql.Tx) error {
		var currentRevision uint64
		if err := tx.QueryRowContext(
			ctx,
			`SELECT revision FROM work_runs WHERE run_id = ?`,
			runID,
		).Scan(&currentRevision); errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", kernel.ErrNotFound, runID)
		} else if err != nil {
			return err
		}
		rebuilt, err := replayGraph(ctx, tx, runID)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(rebuilt)
		if err != nil {
			return err
		}
		updated, err := tx.ExecContext(ctx, `
			UPDATE work_runs
			SET revision = ?, state = ?, aggregate_json = ?, updated_at = ?
			WHERE run_id = ? AND revision = ?`,
			rebuilt.Run.Revision,
			rebuilt.Run.State,
			encoded,
			sqlkit.Timestamp(rebuilt.Run.UpdatedAt),
			runID,
			currentRevision,
		)
		if err != nil {
			return err
		}
		if err := sqlkit.RequireAffected(updated, 1); err != nil {
			return fmt.Errorf("%w: %v", kernel.ErrConflict, err)
		}
		audit, err = auditGraph(ctx, tx, runID)
		return err
	})
	return audit, err
}

func auditGraph(
	ctx context.Context,
	query graphReader,
	runID protocol.RunID,
) (Audit, error) {
	snapshot, found, err := loadGraph(ctx, query, runID)
	if err != nil {
		return Audit{}, err
	}
	if !found {
		return Audit{}, kernel.ErrNotFound
	}
	rebuilt, err := replayGraph(ctx, query, runID)
	if err != nil {
		return Audit{}, err
	}
	snapshotDigest, err := graphDigest(snapshot)
	if err != nil {
		return Audit{}, err
	}
	replayDigest, err := graphDigest(rebuilt)
	if err != nil {
		return Audit{}, err
	}
	var pending int
	if err := query.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM work_outbox WHERE run_id = ? AND published = 0`,
		runID,
	).Scan(&pending); err != nil {
		return Audit{}, err
	}
	return Audit{
		RunID: runID, SnapshotDigest: snapshotDigest,
		ReplayDigest: replayDigest, Drift: snapshotDigest != replayDigest,
		Revision: rebuilt.Run.Revision, PendingEffects: pending,
	}, nil
}

func graphDigest(graph model.Graph) (string, error) {
	encoded, err := json.Marshal(graph)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (s *Store) Facts(
	ctx context.Context,
	runID protocol.RunID,
) ([]kernel.Fact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fact_json
		FROM work_facts
		WHERE run_id = ?
		ORDER BY sequence`,
		runID,
	)
	if err != nil {
		return nil, err
	}
	return sqlkit.ScanAll(rows, func(row sqlkit.RowScanner) (kernel.Fact, error) {
		var encoded []byte
		if err := row.Scan(&encoded); err != nil {
			return kernel.Fact{}, err
		}
		var fact kernel.Fact
		if err := json.Unmarshal(encoded, &fact); err != nil {
			return kernel.Fact{}, err
		}
		return fact, nil
	})
}

func (s *Store) PendingEffects(
	ctx context.Context,
	runID protocol.RunID,
	limit int,
) ([]OutboxEntry, error) {
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("work graph outbox limit must be between 1 and 1000")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT effect_json, published
		FROM work_outbox
		WHERE run_id = ? AND published = 0
		ORDER BY created_at, effect_id
		LIMIT ?`,
		runID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	return sqlkit.ScanAll(rows, func(row sqlkit.RowScanner) (OutboxEntry, error) {
		var encoded []byte
		var published bool
		if err := row.Scan(&encoded, &published); err != nil {
			return OutboxEntry{}, err
		}
		var effect model.Effect
		if err := json.Unmarshal(encoded, &effect); err != nil {
			return OutboxEntry{}, err
		}
		return OutboxEntry{Effect: effect, Published: published}, nil
	})
}

func (s *Store) PendingTerminalEffects(
	ctx context.Context,
	limit int,
) ([]OutboxEntry, error) {
	if limit <= 0 || limit > 1000 {
		return nil, errors.New("work graph outbox limit must be between 1 and 1000")
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT effect_json, published
		FROM work_outbox
		WHERE published = 0 AND kind = ?
		ORDER BY created_at, effect_id
		LIMIT ?`,
		model.EffectPublishTerminal,
		limit,
	)
	if err != nil {
		return nil, err
	}
	return sqlkit.ScanAll(rows, func(row sqlkit.RowScanner) (OutboxEntry, error) {
		var encoded []byte
		var published bool
		if err := row.Scan(&encoded, &published); err != nil {
			return OutboxEntry{}, err
		}
		var effect model.Effect
		if err := json.Unmarshal(encoded, &effect); err != nil {
			return OutboxEntry{}, err
		}
		return OutboxEntry{Effect: effect, Published: published}, nil
	})
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadGraph(
	ctx context.Context,
	query queryer,
	runID protocol.RunID,
) (model.Graph, bool, error) {
	var encoded []byte
	err := query.QueryRowContext(
		ctx,
		`SELECT aggregate_json FROM work_runs WHERE run_id = ?`,
		runID,
	).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Graph{}, false, nil
	}
	if err != nil {
		return model.Graph{}, false, err
	}
	var graph model.Graph
	if err := json.Unmarshal(encoded, &graph); err != nil {
		return model.Graph{}, false, fmt.Errorf("decode work graph aggregate: %w", err)
	}
	if err := graph.Validate(); err != nil {
		return model.Graph{}, false, fmt.Errorf("stored work graph is invalid: %w", err)
	}
	return graph, true, nil
}

func loadCommand(
	ctx context.Context,
	tx *sql.Tx,
	commandID, digest string,
) (kernel.Result, bool, error) {
	var storedDigest string
	var encoded []byte
	err := tx.QueryRowContext(ctx, `
		SELECT request_digest, result_json
		FROM work_commands
		WHERE command_id = ?`,
		commandID,
	).Scan(&storedDigest, &encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return kernel.Result{}, false, nil
	}
	if err != nil {
		return kernel.Result{}, false, err
	}
	if storedDigest != digest {
		return kernel.Result{}, false, ErrCommandConflict
	}
	var result kernel.Result
	if err := json.Unmarshal(encoded, &result); err != nil {
		return kernel.Result{}, false, err
	}
	return result, true, nil
}

func persistTransition(
	ctx context.Context,
	tx *sql.Tx,
	command kernel.Command,
	digest string,
	existingEffects map[protocol.EffectID]bool,
	next kernel.Result,
) error {
	aggregate, err := json.Marshal(next.Graph)
	if err != nil {
		return err
	}
	now := sqlkit.Timestamp(command.At)
	if command.ExpectedRevision == 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_runs(
				run_id, revision, state, aggregate_json, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)`,
			command.RunID,
			next.Graph.Run.Revision,
			next.Graph.Run.State,
			aggregate,
			now,
			now,
		); err != nil {
			return err
		}
	} else {
		updated, err := tx.ExecContext(ctx, `
			UPDATE work_runs
			SET revision = ?, state = ?, aggregate_json = ?, updated_at = ?
			WHERE run_id = ? AND revision = ?`,
			next.Graph.Run.Revision,
			next.Graph.Run.State,
			aggregate,
			now,
			command.RunID,
			command.ExpectedRevision,
		)
		if err != nil {
			return err
		}
		if err := sqlkit.RequireAffected(updated, 1); err != nil {
			return fmt.Errorf("%w: %v", kernel.ErrConflict, err)
		}
	}
	for _, fact := range next.Facts {
		encoded, err := json.Marshal(fact)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_facts(
				run_id, sequence, revision, command_id, kind, fact_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			command.RunID,
			fact.Sequence,
			fact.Revision,
			command.ID,
			fact.Kind,
			encoded,
			sqlkit.Timestamp(fact.At),
		); err != nil {
			return err
		}
	}
	for _, effect := range next.Effects {
		encoded, err := json.Marshal(effect)
		if err != nil {
			return err
		}
		if existingEffects[effect.ID] {
			published := effect.State == model.EffectDispatched
			updated, err := tx.ExecContext(ctx, `
				UPDATE work_outbox
				SET effect_json = ?, published = ?,
					published_at = CASE WHEN ? THEN ? ELSE published_at END
				WHERE effect_id = ?`,
				encoded,
				published,
				published,
				now,
				effect.ID,
			)
			if err != nil {
				return err
			}
			if err := sqlkit.RequireAffected(updated, 1); err != nil {
				return fmt.Errorf("update work graph outbox: %w", err)
			}
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO work_outbox(
				effect_id, run_id, node_id, attempt_id, kind,
				effect_json, published, created_at
			) VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
			effect.ID,
			effect.RunID,
			sqlkit.NullableString(string(effect.NodeID)),
			sqlkit.NullableString(string(effect.AttemptID)),
			effect.Kind,
			encoded,
			sqlkit.Timestamp(effect.CreatedAt),
		); err != nil {
			return err
		}
	}
	receipt, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO work_commands(
			command_id, run_id, request_digest, result_json, created_at
		) VALUES (?, ?, ?, ?, ?)`,
		command.ID,
		command.RunID,
		digest,
		receipt,
		now,
	); err != nil {
		return err
	}
	return nil
}

func commandDigest(command kernel.Command) (string, error) {
	encoded, err := json.Marshal(command)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

const workGraphSchema = `
CREATE TABLE IF NOT EXISTS work_runs (
    run_id TEXT PRIMARY KEY,
    revision INTEGER NOT NULL CHECK (revision > 0),
    state TEXT NOT NULL,
    aggregate_json TEXT NOT NULL CHECK (json_valid(aggregate_json)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS work_commands (
    command_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES work_runs(run_id) ON DELETE CASCADE,
    request_digest TEXT NOT NULL CHECK (length(request_digest) = 64),
    result_json TEXT NOT NULL CHECK (json_valid(result_json)),
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS work_commands_run_created
ON work_commands(run_id, created_at);

CREATE TABLE IF NOT EXISTS work_facts (
    run_id TEXT NOT NULL REFERENCES work_runs(run_id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    revision INTEGER NOT NULL CHECK (revision > 0),
    command_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    fact_json TEXT NOT NULL CHECK (json_valid(fact_json)),
    created_at TEXT NOT NULL,
    PRIMARY KEY (run_id, sequence)
);
CREATE INDEX IF NOT EXISTS work_facts_command
ON work_facts(command_id);

CREATE TABLE IF NOT EXISTS work_outbox (
    effect_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL REFERENCES work_runs(run_id) ON DELETE CASCADE,
    node_id TEXT,
    attempt_id TEXT,
    kind TEXT NOT NULL,
    effect_json TEXT NOT NULL CHECK (json_valid(effect_json)),
    published INTEGER NOT NULL DEFAULT 0 CHECK (published IN (0, 1)),
    created_at TEXT NOT NULL,
    published_at TEXT
);
CREATE INDEX IF NOT EXISTS work_outbox_pending
ON work_outbox(published, created_at, effect_id);
`
