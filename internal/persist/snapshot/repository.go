// Package snapshot persists versioned checkpoints in SQLite with content in CAS.
package snapshot

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const SchemaVersion = 1

var (
	ErrNotFound          = errors.New("snapshot not found")
	ErrIntegrity         = errors.New("snapshot integrity check failed")
	ErrUnsupportedSchema = errors.New("unsupported snapshot schema")
)

type IntegrityError struct {
	ID       string
	Expected string
	Actual   string
	Err      error
}

func (e *IntegrityError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("snapshot %q integrity check failed: %v", e.ID, e.Err)
	}
	return fmt.Sprintf(
		"snapshot %q content hash mismatch: expected %s, got %s",
		e.ID, e.Expected, e.Actual,
	)
}

func (e *IntegrityError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrIntegrity}
	}
	return []error{ErrIntegrity, e.Err}
}

type SchemaError struct {
	ID        string
	Found     int
	Supported int
}

func (e *SchemaError) Error() string {
	return fmt.Sprintf(
		"snapshot %q schema version %d is unsupported; supported version is %d",
		e.ID, e.Found, e.Supported,
	)
}

func (e *SchemaError) Unwrap() error { return ErrUnsupportedSchema }

type Snapshot struct {
	ID            string
	ThreadID      protocol.ThreadID
	TurnID        protocol.TurnID
	Cursor        protocol.Cursor
	Kind          string
	SchemaVersion int
	ContentHash   string
	Content       []byte
	Metadata      json.RawMessage
	CreatedAt     time.Time
}

type Repository struct {
	db      *sql.DB
	content *cas.Store
}

func NewRepository(db *sql.DB, content *cas.Store) *Repository {
	return &Repository{db: db, content: content}
}

func NewSQLiteRepository(store *sqlitestate.Store, content *cas.Store) *Repository {
	if store == nil {
		return &Repository{content: content}
	}
	return NewRepository(store.DB(), content)
}

func (r *Repository) Save(ctx context.Context, value Snapshot) (Snapshot, error) {
	if r.db == nil || r.content == nil {
		return Snapshot{}, errors.New("snapshot database and content store are required")
	}
	if value.ID == "" || value.ThreadID == "" || value.Kind == "" {
		return Snapshot{}, errors.New("snapshot id, thread id, and kind are required")
	}
	if value.SchemaVersion == 0 {
		value.SchemaVersion = SchemaVersion
	}
	if value.SchemaVersion != SchemaVersion {
		return Snapshot{}, &SchemaError{
			ID: value.ID, Found: value.SchemaVersion, Supported: SchemaVersion,
		}
	}
	metadata, err := normalizedObject(value.Metadata)
	if err != nil {
		return Snapshot{}, fmt.Errorf("snapshot metadata: %w", err)
	}
	value.Content = append([]byte(nil), value.Content...)
	value.ContentHash = hash(value.Content)
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now().UTC()
	}
	if err := r.content.Put(ctx, value.ContentHash, value.Content); err != nil {
		return Snapshot{}, fmt.Errorf("store snapshot content: %w", err)
	}
	inserted := false
	defer func() {
		if !inserted {
			_ = r.content.Release(context.Background(), value.ContentHash)
		}
	}()
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO snapshots(
			id, thread_id, turn_id, cursor, kind, content_hash,
			schema_version, metadata_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.ThreadID, nullableTurn(value.TurnID), value.Cursor, value.Kind,
		value.ContentHash, value.SchemaVersion, metadata, timestamp(value.CreatedAt),
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("persist snapshot: %w", err)
	}
	inserted = true
	value.Metadata = metadata
	return value, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Snapshot, error) {
	if r.db == nil || r.content == nil {
		return Snapshot{}, errors.New("snapshot database and content store are required")
	}
	return r.read(ctx, `
		SELECT id, thread_id, turn_id, cursor, kind, content_hash,
			schema_version, metadata_json, created_at
		FROM snapshots WHERE id = ?`, id)
}

func (r *Repository) Latest(
	ctx context.Context,
	threadID protocol.ThreadID,
	kind string,
) (Snapshot, error) {
	if r.db == nil || r.content == nil {
		return Snapshot{}, errors.New("snapshot database and content store are required")
	}
	if threadID == "" || kind == "" {
		return Snapshot{}, errors.New("snapshot thread id and kind are required")
	}
	return r.read(ctx, `
		SELECT id, thread_id, turn_id, cursor, kind, content_hash,
			schema_version, metadata_json, created_at
		FROM snapshots WHERE thread_id = ? AND kind = ?
		ORDER BY cursor DESC, created_at DESC LIMIT 1`, threadID, kind)
}

// Recover returns the latest verified checkpoint. Corruption and unsupported
// schemas are returned explicitly and are never treated as a missing snapshot.
func (r *Repository) Recover(
	ctx context.Context,
	threadID protocol.ThreadID,
	kind string,
) (Snapshot, error) {
	return r.Latest(ctx, threadID, kind)
}

func (r *Repository) read(ctx context.Context, query string, arguments ...any) (Snapshot, error) {
	var value Snapshot
	var turnID sql.NullString
	var metadata, createdAt string
	err := r.db.QueryRowContext(ctx, query, arguments...).Scan(
		&value.ID, &value.ThreadID, &turnID, &value.Cursor, &value.Kind,
		&value.ContentHash, &value.SchemaVersion, &metadata, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	if err != nil {
		return Snapshot{}, fmt.Errorf("read snapshot: %w", err)
	}
	value.TurnID = protocol.TurnID(turnID.String)
	value.Metadata = json.RawMessage(metadata)
	value.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Snapshot{}, &IntegrityError{ID: value.ID, Err: err}
	}
	if value.SchemaVersion != SchemaVersion {
		return Snapshot{}, &SchemaError{
			ID: value.ID, Found: value.SchemaVersion, Supported: SchemaVersion,
		}
	}
	content, err := r.content.Get(ctx, value.ContentHash)
	if err != nil {
		return Snapshot{}, &IntegrityError{ID: value.ID, Expected: value.ContentHash, Err: err}
	}
	actual := hash(content)
	if actual != value.ContentHash {
		return Snapshot{}, &IntegrityError{
			ID: value.ID, Expected: value.ContentHash, Actual: actual,
		}
	}
	value.Content = content
	return value, nil
}

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func normalizedObject(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var decoded map[string]any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func nullableTurn(value protocol.TurnID) any {
	if value == "" {
		return nil
	}
	return value
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
