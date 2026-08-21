package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type ContextRebaseRepository struct {
	store     *state.Store
	turnFacts *turnstate.Store
	now       func() time.Time
}

func NewContextRebaseRepository(
	store *state.Store,
) *ContextRebaseRepository {
	var facts *turnstate.Store
	if store != nil {
		facts = turnstate.NewSQLiteRepository(store.SQLite())
	}
	return &ContextRebaseRepository{
		store: store, turnFacts: facts, now: time.Now,
	}
}

func (r *ContextRebaseRepository) CommitContextRebase(
	ctx context.Context,
	envelope sessiondelta.ContextRebaseEnvelope,
) error {
	return r.commitContextRebase(ctx, envelope, nil)
}

func (r *ContextRebaseRepository) CommitContextRebaseWithFacts(
	ctx context.Context,
	envelope sessiondelta.ContextRebaseEnvelope,
	batch turnkernel.DomainFactBatch,
) error {
	if batch.TurnID != string(envelope.TurnID) ||
		batch.ExpectedNext == 0 || len(batch.Facts) == 0 {
		return errors.New("context rebase domain fact batch is invalid")
	}
	return r.commitContextRebase(ctx, envelope, &batch)
}

func (r *ContextRebaseRepository) CommitCurrentContext(
	ctx context.Context,
	commit sessiondelta.CurrentContextCommit,
) error {
	if r == nil || r.store == nil {
		return errors.New("current context store is unavailable")
	}
	if err := commit.Validate(); err != nil {
		return err
	}
	previous, found, err := r.latestManifest(ctx, commit.ThreadID)
	if err != nil {
		return err
	}
	var prior *sessiondelta.ContextManifest
	if found {
		prior = &previous
	}
	manifest, err := sessiondelta.BuildContextManifest(
		ctx,
		r.store.Content(),
		commit.ThreadID,
		commit.TurnID,
		commit.Snapshot,
		prior,
		commit.ManifestLimits,
	)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		r.releaseNewRefs(context.Background(), prior, manifest)
		return err
	}
	inserted := false
	err = r.store.SQLite().Transaction(ctx, func(tx *sql.Tx) error {
		if commit.ParentThreadID != "" {
			var parentSession string
			if err := tx.QueryRowContext(
				ctx,
				`SELECT session_id FROM threads WHERE id = ?`,
				commit.ParentThreadID,
			).Scan(&parentSession); err != nil {
				return err
			}
			if parentSession != commit.SessionID {
				return errors.New("current context Fork Session is inconsistent")
			}
			now := r.now().UTC().Format(time.RFC3339Nano)
			if _, err := tx.ExecContext(
				ctx,
				`INSERT INTO threads(
					id, session_id, parent_thread_id, title, status,
					source_cursor, created_at, updated_at
				) VALUES (?, ?, ?, ?, 'open', ?, ?, ?)
				ON CONFLICT(id) DO NOTHING`,
				commit.ThreadID,
				commit.SessionID,
				commit.ParentThreadID,
				commit.Title,
				commit.SourceCursor,
				now,
				now,
			); err != nil {
				return err
			}
			var (
				childSession string
				childParent  sql.NullString
				childTitle   string
				sourceCursor uint64
			)
			if err := tx.QueryRowContext(
				ctx,
				`SELECT session_id, parent_thread_id, title, source_cursor
				 FROM threads WHERE id = ?`,
				commit.ThreadID,
			).Scan(
				&childSession,
				&childParent,
				&childTitle,
				&sourceCursor,
			); err != nil {
				return err
			}
			if childSession != commit.SessionID ||
				childParent.String != string(commit.ParentThreadID) ||
				childTitle != commit.Title ||
				sourceCursor != uint64(commit.SourceCursor) {
				return errors.New("current context Fork Thread is inconsistent")
			}
		}
		var committedDigest string
		committedErr := tx.QueryRowContext(
			ctx,
			`SELECT envelope_digest FROM context_rebases
			 WHERE compaction_id = ?`,
			commit.ID,
		).Scan(&committedDigest)
		switch {
		case committedErr == nil:
			if committedDigest != commit.Snapshot.Digest {
				return errors.New("current context commit digest conflict")
			}
			var currentID string
			if err := tx.QueryRowContext(
				ctx,
				`SELECT compaction_id FROM context_current WHERE thread_id = ?`,
				commit.ThreadID,
			).Scan(&currentID); err != nil {
				return err
			}
			if currentID != commit.ID {
				return errors.New("current context commit is no longer current")
			}
			return nil
		case !errors.Is(committedErr, sql.ErrNoRows):
			return committedErr
		}
		var revision uint64
		currentErr := tx.QueryRowContext(
			ctx,
			`SELECT revision FROM context_current WHERE thread_id = ?`,
			commit.ThreadID,
		).Scan(&revision)
		if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
			return currentErr
		}
		if currentErr == nil && revision >= commit.Snapshot.Revision {
			return errors.New("current context revision conflict")
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO context_rebases(
				compaction_id, thread_id, turn_id, revision,
				envelope_digest, manifest_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			commit.ID,
			commit.ThreadID,
			commit.TurnID,
			commit.Snapshot.Revision,
			commit.Snapshot.Digest,
			string(raw),
			r.now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return err
		}
		inserted = true
		_, err = tx.ExecContext(
			ctx,
			`INSERT INTO context_current(
				thread_id, compaction_id, epoch, revision, updated_at
			) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(thread_id) DO UPDATE SET
				compaction_id = excluded.compaction_id,
				epoch = excluded.epoch,
				revision = excluded.revision,
				updated_at = excluded.updated_at`,
			commit.ThreadID,
			commit.ID,
			commit.Snapshot.Epoch,
			commit.Snapshot.Revision,
			r.now().UTC().Format(time.RFC3339Nano),
		)
		return err
	})
	if err != nil {
		r.releaseNewRefs(context.Background(), prior, manifest)
		return err
	}
	if !inserted {
		r.releaseNewRefs(context.Background(), prior, manifest)
	}
	return nil
}

func (r *ContextRebaseRepository) DeleteCurrentContext(
	ctx context.Context,
	threadID protocol.ThreadID,
	commitID string,
	deleteThread bool,
) error {
	if r == nil || r.store == nil || threadID == "" || commitID == "" {
		return errors.New("current context deletion identity is incomplete")
	}
	manifest, found, err := r.latestManifest(ctx, threadID)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	err = r.store.SQLite().Transaction(ctx, func(tx *sql.Tx) error {
		var currentID string
		if err := tx.QueryRowContext(
			ctx,
			`SELECT compaction_id FROM context_current WHERE thread_id = ?`,
			threadID,
		).Scan(&currentID); err != nil {
			return err
		}
		if currentID != commitID {
			return errors.New("current context deletion conflict")
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM context_current WHERE thread_id = ?`,
			threadID,
		); err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM context_rebases WHERE compaction_id = ?`,
			commitID,
		); err != nil {
			return err
		}
		if deleteThread {
			_, err := tx.ExecContext(
				ctx,
				`DELETE FROM threads WHERE id = ?`,
				threadID,
			)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, ref := range manifestRefs(manifest) {
		_ = r.store.Content().Release(context.Background(), ref.Handle)
	}
	return nil
}

func (r *ContextRebaseRepository) commitContextRebase(
	ctx context.Context,
	envelope sessiondelta.ContextRebaseEnvelope,
	batch *turnkernel.DomainFactBatch,
) error {
	if r == nil || r.store == nil {
		return errors.New("context rebase store is unavailable")
	}
	if batch != nil && r.turnFacts == nil {
		return errors.New("context rebase domain fact store is unavailable")
	}
	if err := envelope.Validate(); err != nil {
		return err
	}
	previous, found, err := r.latestManifest(ctx, envelope.ThreadID)
	if err != nil {
		return err
	}
	var prior *sessiondelta.ContextManifest
	if found {
		prior = &previous
	}
	manifest, err := sessiondelta.BuildContextManifest(
		ctx,
		r.store.Content(),
		envelope.ThreadID,
		envelope.TurnID,
		envelope.Snapshot,
		prior,
		envelope.ManifestLimits,
	)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	inserted := false
	err = r.store.SQLite().Transaction(ctx, func(tx *sql.Tx) error {
		var committedDigest string
		committedErr := tx.QueryRowContext(
			ctx,
			`SELECT envelope_digest FROM context_rebases
			 WHERE compaction_id = ?`,
			envelope.CompactionID,
		).Scan(&committedDigest)
		switch {
		case committedErr == nil:
			if committedDigest != envelope.Digest {
				return errors.New("context rebase digest conflict")
			}
			if err := r.requireCurrentRebase(
				ctx,
				tx,
				envelope,
			); err != nil {
				return err
			}
			if batch != nil {
				return r.turnFacts.AppendDomainFactsIdempotentTx(
					ctx,
					tx,
					batch.TurnID,
					batch.ExpectedNext,
					batch.Facts,
				)
			}
			return nil
		case !errors.Is(committedErr, sql.ErrNoRows):
			return committedErr
		}
		var revision uint64
		currentErr := tx.QueryRowContext(
			ctx,
			`SELECT revision FROM context_current WHERE thread_id = ?`,
			envelope.ThreadID,
		).Scan(&revision)
		if currentErr != nil && !errors.Is(currentErr, sql.ErrNoRows) {
			return currentErr
		}
		if currentErr == nil && revision >= envelope.Snapshot.Revision {
			return errors.New("context rebase revision conflict")
		}
		if _, insertErr := tx.ExecContext(
			ctx,
			`INSERT INTO context_rebases(
				compaction_id, thread_id, turn_id, revision,
				envelope_digest, manifest_json, created_at
			) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			envelope.CompactionID,
			envelope.ThreadID,
			envelope.TurnID,
			envelope.Snapshot.Revision,
			envelope.Digest,
			string(raw),
			r.now().UTC().Format(time.RFC3339Nano),
		); insertErr != nil {
			return insertErr
		}
		inserted = true
		_, updateErr := tx.ExecContext(
			ctx,
			`INSERT INTO context_current(
				thread_id, compaction_id, epoch, revision, updated_at
			) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(thread_id) DO UPDATE SET
				compaction_id = excluded.compaction_id,
				epoch = excluded.epoch,
				revision = excluded.revision,
				updated_at = excluded.updated_at`,
			envelope.ThreadID,
			envelope.CompactionID,
			envelope.Snapshot.Epoch,
			envelope.Snapshot.Revision,
			r.now().UTC().Format(time.RFC3339Nano),
		)
		if updateErr != nil {
			return updateErr
		}
		if batch != nil {
			return r.turnFacts.AppendDomainFactsIdempotentTx(
				ctx,
				tx,
				batch.TurnID,
				batch.ExpectedNext,
				batch.Facts,
			)
		}
		return nil
	})
	if err != nil {
		r.releaseNewRefs(context.Background(), prior, manifest)
		return err
	}
	if !inserted {
		r.releaseNewRefs(context.Background(), prior, manifest)
	}
	return nil
}

type contextRebaseQueryer interface {
	QueryRowContext(
		context.Context,
		string,
		...any,
	) *sql.Row
}

func (r *ContextRebaseRepository) requireCurrentRebase(
	ctx context.Context,
	queryer contextRebaseQueryer,
	envelope sessiondelta.ContextRebaseEnvelope,
) error {
	var compactionID string
	if err := queryer.QueryRowContext(
		ctx,
		`SELECT compaction_id FROM context_current WHERE thread_id = ?`,
		envelope.ThreadID,
	).Scan(&compactionID); err != nil {
		return err
	}
	if compactionID != envelope.CompactionID {
		return errors.New("context rebase is no longer current")
	}
	return nil
}

func (r *ContextRebaseRepository) LatestContextSnapshot(
	ctx context.Context,
	threadID protocol.ThreadID,
) (sessiondelta.ContextSnapshot, bool, error) {
	if r == nil || r.store == nil {
		return sessiondelta.ContextSnapshot{}, false,
			errors.New("context rebase store is unavailable")
	}
	manifest, found, err := r.latestManifest(ctx, threadID)
	if err != nil || !found {
		return sessiondelta.ContextSnapshot{}, found, err
	}
	snapshot, err := sessiondelta.LoadContextManifest(
		ctx,
		r.store.Content(),
		manifest,
	)
	if err != nil {
		return sessiondelta.ContextSnapshot{}, false,
			fmt.Errorf("load current context manifest: %w", err)
	}
	return snapshot, true, nil
}

func (r *ContextRebaseRepository) latestManifest(
	ctx context.Context,
	threadID protocol.ThreadID,
) (sessiondelta.ContextManifest, bool, error) {
	var raw string
	err := r.store.SQLite().DB().QueryRowContext(
		ctx,
		`SELECT r.manifest_json
		 FROM context_current c
		 JOIN context_rebases r ON r.compaction_id = c.compaction_id
		 WHERE c.thread_id = ?`,
		threadID,
	).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return sessiondelta.ContextManifest{}, false, nil
	}
	if err != nil {
		return sessiondelta.ContextManifest{}, false, err
	}
	var manifest sessiondelta.ContextManifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		return sessiondelta.ContextManifest{}, false, err
	}
	if err := manifest.Validate(); err != nil {
		return sessiondelta.ContextManifest{}, false, err
	}
	return manifest, true, nil
}

func (r *ContextRebaseRepository) releaseNewRefs(
	ctx context.Context,
	previous *sessiondelta.ContextManifest,
	current sessiondelta.ContextManifest,
) {
	existing := make(map[string]struct{})
	if previous != nil {
		for _, ref := range manifestRefs(*previous) {
			existing[ref.Handle] = struct{}{}
		}
	}
	for _, ref := range manifestRefs(current) {
		if _, reused := existing[ref.Handle]; reused {
			continue
		}
		_ = r.store.Content().Release(ctx, ref.Handle)
	}
}

func manifestRefs(
	manifest sessiondelta.ContextManifest,
) []sessiondelta.ContentRef {
	result := []sessiondelta.ContentRef{manifest.History.BaseRef}
	result = append(result, manifest.History.TailRefs...)
	for _, owner := range []sessiondelta.OwnerManifest{
		manifest.Working,
		manifest.Evidence,
		manifest.Failures,
		manifest.Plan,
	} {
		result = append(result, owner.BaseRef)
		result = append(result, owner.DeltaRefs...)
	}
	return result
}
