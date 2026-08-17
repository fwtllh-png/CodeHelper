package semantic

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

var (
	ErrProjectionNotFound = errors.New("semantic projection not found")
	ErrProjectionCorrupt  = errors.New("semantic projection is corrupt")
	ErrSourceConflict     = errors.New("semantic projection source conflicts with stored cursor")
	ErrProjectionAhead    = errors.New("semantic projection is ahead of source")
)

const projectionSchema = `
CREATE TABLE IF NOT EXISTS observation_semantic_projection (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    reducer_version INTEGER NOT NULL,
    source_sequence INTEGER NOT NULL CHECK (source_sequence >= 0),
    source_digest TEXT NOT NULL,
    graph_digest TEXT NOT NULL,
    graph_json BLOB NOT NULL CHECK (json_valid(graph_json))
);
`

type Repository struct {
	db *sql.DB
}

type Snapshot struct {
	ReducerVersion uint32 `json:"reducer_version"`
	SourceSequence uint64 `json:"source_sequence"`
	SourceDigest   string `json:"source_digest"`
	GraphDigest    string `json:"graph_digest"`
	Graph          Graph  `json:"graph"`
}

type ProjectionLag struct {
	SourceSequence     uint64 `json:"source_sequence"`
	ProjectionSequence uint64 `json:"projection_sequence"`
	Behind             uint64 `json:"behind"`
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Ensure(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("semantic projection database is required")
	}
	_, err := r.db.ExecContext(ctx, projectionSchema)
	return err
}

func (r *Repository) RebuildJournal(
	ctx context.Context,
	root string,
	reducer Reducer,
) (Snapshot, error) {
	records, err := journal.ReadAll(root)
	if err != nil {
		return Snapshot{}, err
	}
	return r.Rebuild(ctx, records, reducer)
}

func (r *Repository) Rebuild(
	ctx context.Context,
	records []journal.Record,
	reducer Reducer,
) (Snapshot, error) {
	if err := r.Ensure(ctx); err != nil {
		return Snapshot{}, err
	}
	envelopes := make([]observation.Envelope, len(records))
	var sourceSequence uint64
	for index, record := range records {
		if record.Sequence != record.Envelope.Sequence ||
			(index != 0 && record.Sequence <= records[index-1].Sequence) {
			return Snapshot{}, fmt.Errorf(
				"%w: journal record %d",
				ErrSourceConflict,
				index,
			)
		}
		envelopes[index] = record.Envelope
		sourceSequence = record.Sequence
	}
	sourceDigest := journalSourceDigest(records)
	graph, err := reducer.Reduce(ctx, envelopes)
	if err != nil {
		return Snapshot{}, err
	}
	graphJSON, err := CanonicalJSON(graph)
	if err != nil {
		return Snapshot{}, err
	}
	sum := sha256.Sum256(graphJSON)
	graphDigest := hex.EncodeToString(sum[:])
	snapshot := Snapshot{
		ReducerVersion: Version,
		SourceSequence: sourceSequence,
		SourceDigest:   sourceDigest,
		GraphDigest:    graphDigest,
		Graph:          graph,
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var currentSequence uint64
	var currentSource, currentGraph string
	err = tx.QueryRowContext(ctx, `
		SELECT source_sequence, source_digest, graph_digest
		FROM observation_semantic_projection WHERE singleton = 1`,
	).Scan(&currentSequence, &currentSource, &currentGraph)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return Snapshot{}, err
	case currentSequence > sourceSequence:
		return Snapshot{}, fmt.Errorf(
			"%w: projection=%d source=%d",
			ErrProjectionAhead,
			currentSequence,
			sourceSequence,
		)
	case currentSequence == sourceSequence &&
		currentSource != sourceDigest:
		return Snapshot{}, fmt.Errorf(
			"%w: sequence %d",
			ErrSourceConflict,
			sourceSequence,
		)
	case currentSequence == sourceSequence &&
		currentGraph != graphDigest:
		return Snapshot{}, fmt.Errorf(
			"%w: reducer version %d is not deterministic at sequence %d",
			ErrSourceConflict,
			Version,
			sourceSequence,
		)
	case currentSequence == sourceSequence &&
		currentGraph == graphDigest:
		return snapshot, tx.Commit()
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO observation_semantic_projection(
			singleton, reducer_version, source_sequence, source_digest,
			graph_digest, graph_json
		) VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(singleton) DO UPDATE SET
			reducer_version = excluded.reducer_version,
			source_sequence = excluded.source_sequence,
			source_digest = excluded.source_digest,
			graph_digest = excluded.graph_digest,
			graph_json = excluded.graph_json`,
		Version,
		sourceSequence,
		sourceDigest,
		graphDigest,
		graphJSON,
	)
	if err != nil {
		return Snapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

func (r *Repository) Load(ctx context.Context) (Snapshot, error) {
	if err := r.Ensure(ctx); err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	var encoded []byte
	err := r.db.QueryRowContext(ctx, `
		SELECT reducer_version, source_sequence, source_digest,
			graph_digest, graph_json
		FROM observation_semantic_projection WHERE singleton = 1`,
	).Scan(
		&snapshot.ReducerVersion,
		&snapshot.SourceSequence,
		&snapshot.SourceDigest,
		&snapshot.GraphDigest,
		&encoded,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrProjectionNotFound
	}
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.ReducerVersion != Version {
		return Snapshot{}, fmt.Errorf(
			"%w: reducer version %d",
			ErrProjectionCorrupt,
			snapshot.ReducerVersion,
		)
	}
	if len(snapshot.SourceDigest) != sha256.Size*2 {
		return Snapshot{}, fmt.Errorf(
			"%w: source digest is invalid",
			ErrProjectionCorrupt,
		)
	}
	if _, err := hex.DecodeString(snapshot.SourceDigest); err != nil {
		return Snapshot{}, fmt.Errorf(
			"%w: source digest is invalid",
			ErrProjectionCorrupt,
		)
	}
	sum := sha256.Sum256(encoded)
	if hex.EncodeToString(sum[:]) != snapshot.GraphDigest {
		return Snapshot{}, fmt.Errorf(
			"%w: graph digest mismatch",
			ErrProjectionCorrupt,
		)
	}
	if err := json.Unmarshal(encoded, &snapshot.Graph); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %v", ErrProjectionCorrupt, err)
	}
	if snapshot.Graph.Version != Version ||
		snapshot.Graph.LastSequence != snapshot.SourceSequence {
		return Snapshot{}, fmt.Errorf(
			"%w: graph cursor mismatch",
			ErrProjectionCorrupt,
		)
	}
	return snapshot, nil
}

func (r *Repository) Lag(
	ctx context.Context,
	sourceSequence uint64,
) (ProjectionLag, error) {
	snapshot, err := r.Load(ctx)
	if errors.Is(err, ErrProjectionNotFound) {
		return ProjectionLag{
			SourceSequence: sourceSequence,
			Behind:         sourceSequence,
		}, nil
	}
	if err != nil {
		return ProjectionLag{}, err
	}
	if snapshot.SourceSequence > sourceSequence {
		return ProjectionLag{}, fmt.Errorf(
			"%w: projection=%d source=%d",
			ErrProjectionAhead,
			snapshot.SourceSequence,
			sourceSequence,
		)
	}
	return ProjectionLag{
		SourceSequence:     sourceSequence,
		ProjectionSequence: snapshot.SourceSequence,
		Behind:             sourceSequence - snapshot.SourceSequence,
	}, nil
}

type ContentReader interface {
	Get(context.Context, string) ([]byte, error)
}

type ContentVerifier struct {
	Reader ContentReader
}

func (v ContentVerifier) Verify(
	ctx context.Context,
	reference observation.PayloadRef,
) error {
	if v.Reader == nil {
		return errors.New("payload content reader is unavailable")
	}
	digest := strings.TrimPrefix(reference.Digest, "sha256:")
	content, err := v.Reader.Get(ctx, digest)
	if err != nil {
		return err
	}
	if uint64(len(content)) != reference.StoredBytes {
		return fmt.Errorf(
			"payload stored bytes are %d, want %d",
			len(content),
			reference.StoredBytes,
		)
	}
	return nil
}

func journalSourceDigest(records []journal.Record) string {
	digest := sha256.New()
	var sequence [8]byte
	for _, record := range records {
		binary.BigEndian.PutUint64(sequence[:], record.Sequence)
		_, _ = digest.Write(sequence[:])
		_, _ = digest.Write([]byte(record.SHA256))
		_, _ = digest.Write([]byte(record.PreviousSHA256))
	}
	return hex.EncodeToString(digest.Sum(nil))
}
