// Package sqlite owns CodeHelper's durable relational state.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const SchemaVersion = 1

var (
	ErrCorrupt           = errors.New("sqlite database is corrupt")
	ErrUnsupportedSchema = errors.New("sqlite schema is newer than supported")
)

// CorruptionError reports database content that SQLite cannot safely read.
type CorruptionError struct {
	Path   string
	Detail string
	Err    error
}

func (e *CorruptionError) Error() string {
	if e.Detail != "" {
		return fmt.Sprintf("sqlite database %q is corrupt: %s", e.Path, e.Detail)
	}
	return fmt.Sprintf("sqlite database %q is corrupt: %v", e.Path, e.Err)
}

func (e *CorruptionError) Unwrap() []error {
	if e.Err == nil {
		return []error{ErrCorrupt}
	}
	return []error{ErrCorrupt, e.Err}
}

// SchemaVersionError is returned without migrating or otherwise writing the
// database when its schema is newer than this binary understands.
type SchemaVersionError struct {
	Found     int
	Supported int
}

func (e *SchemaVersionError) Error() string {
	return fmt.Sprintf("sqlite schema version %d is newer than supported version %d", e.Found, e.Supported)
}

func (e *SchemaVersionError) Unwrap() error { return ErrUnsupportedSchema }

// Options controls connection-local SQLite behavior.
type Options struct {
	// BusyTimeout is the maximum time SQLite waits for a locked database.
	// Zero selects the default of five seconds.
	BusyTimeout time.Duration
}

// Store is a concurrency-safe handle to the CodeHelper state database.
type Store struct {
	path      string
	db        *sql.DB
	closeOnce sync.Once
	closeErr  error
}

// Open opens path, creates the current schema when needed, and verifies database
// integrity. A newer schema is rejected before any write is attempted.
func Open(ctx context.Context, path string, options ...Options) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	if len(options) > 1 {
		return nil, errors.New("sqlite Open accepts at most one Options value")
	}
	opts := Options{}
	if len(options) == 1 {
		opts = options[0]
	}
	if opts.BusyTimeout < 0 {
		return nil, errors.New("sqlite busy timeout cannot be negative")
	}
	if opts.BusyTimeout == 0 {
		opts.BusyTimeout = 5 * time.Second
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve sqlite path: %w", err)
	}
	dsn := sqliteDSN(absolute, opts.BusyTimeout)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	// Keeping one live connection ensures every operation uses the connection
	// configured by the DSN pragmas. Separate Store handles still exercise
	// SQLite's normal cross-connection locking behavior.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{path: absolute, db: db}
	ok := false
	defer func() {
		if !ok {
			_ = db.Close()
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return nil, store.classify("open database", err)
	}

	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return nil, store.classify("read schema version", err)
	}
	if version > SchemaVersion {
		return nil, &SchemaVersionError{Found: version, Supported: SchemaVersion}
	}

	if err := store.enableWAL(ctx); err != nil {
		return nil, err
	}
	if version == 0 {
		if err := store.initializeSchema(ctx); err != nil {
			return nil, err
		}
	} else if version != SchemaVersion {
		return nil, fmt.Errorf("unsupported sqlite schema version %d", version)
	}
	if err := store.verifyPragmas(ctx, opts.BusyTimeout); err != nil {
		return nil, err
	}
	if err := store.verifyIntegrity(ctx); err != nil {
		return nil, err
	}

	ok = true
	return store, nil
}

func sqliteDSN(path string, busyTimeout time.Duration) string {
	u := &url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := u.Query()
	query.Add("_pragma", "foreign_keys(ON)")
	query.Add("_pragma", "busy_timeout("+strconv.FormatInt(busyTimeout.Milliseconds(), 10)+")")
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Store) enableWAL(ctx context.Context) error {
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil {
		return s.classify("enable WAL", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("enable WAL: SQLite selected journal mode %q", mode)
	}
	return nil
}

func (s *Store) initializeSchema(ctx context.Context) error {
	return s.WithTx(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, schemaV1); err != nil {
			return s.classify("create schema v1", err)
		}
		if _, err := tx.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
			return s.classify("record schema version", err)
		}
		return nil
	})
}

func (s *Store) verifyPragmas(ctx context.Context, timeout time.Duration) error {
	var foreignKeys int
	if err := s.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return s.classify("verify foreign keys", err)
	}
	if foreignKeys != 1 {
		return errors.New("sqlite foreign key enforcement is disabled")
	}
	var busyTimeout int64
	if err := s.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return s.classify("verify busy timeout", err)
	}
	if busyTimeout != timeout.Milliseconds() {
		return fmt.Errorf("sqlite busy timeout is %dms, want %dms", busyTimeout, timeout.Milliseconds())
	}
	return nil
}

func (s *Store) verifyIntegrity(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return s.classify("run integrity check", err)
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return s.classify("read integrity check", err)
		}
		if result != "ok" {
			return &CorruptionError{Path: s.path, Detail: result}
		}
	}
	if err := rows.Err(); err != nil {
		return s.classify("run integrity check", err)
	}
	return nil
}

func (s *Store) classify(operation string, err error) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "database disk image is malformed") ||
		strings.Contains(message, "file is not a database") ||
		strings.Contains(message, "database corruption") {
		return &CorruptionError{Path: s.path, Err: err}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// DB exposes the underlying handle for typed queries and prepared statements.
// Callers should prefer Transaction or WithTx for multi-statement writes.
func (s *Store) DB() *sql.DB { return s.db }

// BeginTx starts a transaction with the requested database/sql options.
func (s *Store) BeginTx(ctx context.Context, options *sql.TxOptions) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, options)
	if err != nil {
		return nil, s.classify("begin transaction", err)
	}
	return tx, nil
}

// Transaction executes fn atomically with default transaction options.
func (s *Store) Transaction(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.WithTx(ctx, nil, fn)
}

// WithTx executes fn atomically. It commits only when fn returns nil.
func (s *Store) WithTx(ctx context.Context, options *sql.TxOptions, fn func(*sql.Tx) error) (err error) {
	if fn == nil {
		return errors.New("sqlite transaction callback is required")
	}
	tx, err := s.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
		if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = errors.Join(err, fmt.Errorf("rollback transaction: %w", rollbackErr))
			}
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return s.classify("commit transaction", err)
	}
	return nil
}

// Close closes the database. It is safe to call more than once.
func (s *Store) Close() error {
	s.closeOnce.Do(func() {
		s.closeErr = s.db.Close()
	})
	return s.closeErr
}

const schemaV1 = `
CREATE TABLE workspaces (
    id TEXT PRIMARY KEY,
    root_path TEXT NOT NULL UNIQUE,
    display_name TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (json_valid(metadata_json))
);

CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    status TEXT NOT NULL,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at TEXT,
    CHECK (json_valid(metadata_json))
);
CREATE INDEX sessions_workspace_created ON sessions(workspace_id, created_at);

CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    parent_thread_id TEXT REFERENCES threads(id) ON DELETE SET NULL,
    source_cursor INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX threads_session_created ON threads(session_id, created_at);

CREATE TABLE operations (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    idempotency_key TEXT,
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    request_json TEXT NOT NULL,
    response_json TEXT,
    error_json TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (json_valid(request_json)),
    CHECK (response_json IS NULL OR json_valid(response_json)),
    CHECK (error_json IS NULL OR json_valid(error_json)),
    UNIQUE (session_id, idempotency_key)
);
CREATE INDEX operations_session_created ON operations(session_id, created_at);

CREATE TABLE turns (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    operation_id TEXT REFERENCES operations(id) ON DELETE SET NULL,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    status TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    completed_at TEXT,
    UNIQUE (thread_id, ordinal)
);
CREATE INDEX turns_thread_created ON turns(thread_id, created_at);
CREATE UNIQUE INDEX IF NOT EXISTS turns_one_active_per_thread
ON turns(thread_id) WHERE status = 'active';

CREATE TABLE items (
    id TEXT PRIMARY KEY,
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    kind TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (json_valid(payload_json)),
    UNIQUE (turn_id, ordinal)
);

CREATE TABLE event_reservations (
    sequence INTEGER PRIMARY KEY CHECK (sequence > 0),
    event_id TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL CHECK (status IN ('reserved', 'committed', 'abandoned')),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX event_reservations_status_sequence ON event_reservations(status, sequence);

CREATE TABLE event_index (
    sequence INTEGER PRIMARY KEY CHECK (sequence > 0),
    event_id TEXT NOT NULL UNIQUE,
    thread_id TEXT,
    turn_id TEXT,
    item_id TEXT,
    kind TEXT NOT NULL,
    log_offset INTEGER NOT NULL CHECK (log_offset >= 0),
    log_length INTEGER NOT NULL CHECK (log_length > 0),
    sha256 TEXT NOT NULL CHECK (length(sha256) = 64),
    created_at TEXT NOT NULL
);
CREATE INDEX event_index_thread_sequence ON event_index(thread_id, sequence);
CREATE INDEX event_index_turn_sequence ON event_index(turn_id, sequence);

CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    thread_id TEXT REFERENCES threads(id) ON DELETE CASCADE,
    turn_id TEXT REFERENCES turns(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    state TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    result_json TEXT,
    lease_owner TEXT,
    lease_expires_at TEXT,
    executor TEXT,
    attempt INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 1,
    next_attempt_at TEXT,
    heartbeat_at TEXT,
    lifecycle_sequence INTEGER NOT NULL DEFAULT 1 CHECK (lifecycle_sequence > 0),
    failure_reason TEXT,
    terminal_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (json_valid(payload_json)),
    CHECK (result_json IS NULL OR json_valid(result_json))
);
CREATE INDEX tasks_state_updated ON tasks(state, updated_at);
CREATE INDEX tasks_claimable ON tasks(state, executor, next_attempt_at);
CREATE INDEX tasks_lease ON tasks(state, lease_expires_at);

CREATE TABLE snapshots (
    id TEXT PRIMARY KEY,
    thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    turn_id TEXT REFERENCES turns(id) ON DELETE SET NULL,
    cursor INTEGER NOT NULL CHECK (cursor >= 0),
    kind TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    CHECK (json_valid(metadata_json)),
    UNIQUE (thread_id, cursor, kind)
);
CREATE INDEX snapshots_thread_cursor ON snapshots(thread_id, cursor DESC);

CREATE TABLE usage (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    thread_id TEXT REFERENCES threads(id) ON DELETE SET NULL,
    turn_id TEXT REFERENCES turns(id) ON DELETE SET NULL,
    sample INTEGER NOT NULL DEFAULT 0,
    event_sequence INTEGER,
    source_sequence INTEGER NOT NULL DEFAULT 0,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    input_tokens INTEGER NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens INTEGER NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    reasoning_tokens INTEGER NOT NULL DEFAULT 0 CHECK (reasoning_tokens >= 0),
    cached_tokens INTEGER NOT NULL DEFAULT 0 CHECK (cached_tokens >= 0),
    cost_microunits INTEGER NOT NULL DEFAULT 0 CHECK (cost_microunits >= 0),
    cost_known INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    UNIQUE (turn_id, sample)
);
CREATE INDEX usage_session_created ON usage(session_id, created_at);
CREATE INDEX usage_turn ON usage(turn_id);

` + taskLifecycleSchema + usageContextSchema + automationSchema +
	agentTopologySchema + repositoryIndexSchema + backgroundExecutionSchema +
	traceSchema + providerCapabilitySchema + `
`

const taskLifecycleSchema = `
CREATE TABLE IF NOT EXISTS task_lifecycle (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    state TEXT NOT NULL,
    reason TEXT,
    created_at TEXT NOT NULL,
    PRIMARY KEY (task_id, sequence)
);
CREATE INDEX IF NOT EXISTS task_lifecycle_task_sequence
ON task_lifecycle(task_id, sequence);
`

const usageContextSchema = `
CREATE TABLE IF NOT EXISTS usage_turn_context (
    turn_id TEXT PRIMARY KEY REFERENCES turns(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    thread_id TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    source_sequence INTEGER NOT NULL CHECK (source_sequence > 0),
    updated_at TEXT NOT NULL
);
`

const automationSchema = `
CREATE TABLE automations (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    thread_id TEXT REFERENCES threads(id) ON DELETE SET NULL,
    turn_id TEXT REFERENCES turns(id) ON DELETE SET NULL,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'paused', 'deleted')),
    rrule TEXT NOT NULL,
    timezone TEXT NOT NULL DEFAULT 'UTC' CHECK (timezone = 'UTC'),
    task_kind TEXT NOT NULL,
    task_payload_json TEXT NOT NULL DEFAULT '{}',
    task_executor TEXT,
    task_max_attempts INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    next_run_at TEXT,
    last_run_at TEXT,
    CHECK (json_valid(task_payload_json))
);
CREATE INDEX automations_status_next_run
ON automations(status, next_run_at);
CREATE INDEX automations_session_created
ON automations(session_id, created_at);

CREATE TABLE automation_runs (
    id TEXT PRIMARY KEY,
    version INTEGER NOT NULL DEFAULT 1 CHECK (version > 0),
    automation_id TEXT NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
    scheduled_for TEXT NOT NULL,
    trigger TEXT NOT NULL CHECK (trigger IN ('scheduled', 'manual')),
    status TEXT NOT NULL CHECK (
        status IN ('queued', 'running', 'waiting', 'failed', 'canceled', 'completed')
    ),
    task_id TEXT UNIQUE REFERENCES tasks(id) ON DELETE SET NULL,
    task_idempotency_key TEXT NOT NULL UNIQUE,
    thread_id TEXT REFERENCES threads(id) ON DELETE SET NULL,
    turn_id TEXT REFERENCES turns(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (automation_id, scheduled_for)
);
CREATE INDEX automation_runs_automation_scheduled
ON automation_runs(automation_id, scheduled_for DESC);
CREATE INDEX automation_runs_status_updated
ON automation_runs(status, updated_at);
`

const agentTopologySchema = `
CREATE TABLE agent_spawn_edges (
    workspace_root TEXT NOT NULL,
    session_id TEXT NOT NULL,
    parent_agent_id TEXT NOT NULL DEFAULT '',
    child_agent_id TEXT NOT NULL,
    status TEXT NOT NULL,
    role TEXT NOT NULL DEFAULT '',
    profile TEXT NOT NULL DEFAULT '',
    stance TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    worktree TEXT NOT NULL DEFAULT '',
    last_message TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    source_sequence INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_root, child_agent_id)
);
CREATE INDEX agent_spawn_edges_workspace_parent_status
ON agent_spawn_edges(workspace_root, parent_agent_id, status);
`

// Repository index rows are keyed by the canonical workspace root rather than a
// workspaces(id) reference: sessions that run without a persistent store keep
// their index in an ephemeral database that has no workspace rows at all, and
// one database may hold several roots.
const repositoryIndexSchema = `
CREATE TABLE repo_index_files (
    root_path TEXT NOT NULL,
    path TEXT NOT NULL,
    language TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    modified_unix_nano INTEGER NOT NULL DEFAULT 0,
    digest TEXT NOT NULL,
    symbol_count INTEGER NOT NULL DEFAULT 0,
    indexed_at TEXT NOT NULL,
    PRIMARY KEY (root_path, path)
);

CREATE TABLE repo_index_symbols (
    root_path TEXT NOT NULL,
    path TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    container TEXT NOT NULL DEFAULT '',
    line INTEGER NOT NULL,
    exported INTEGER NOT NULL DEFAULT 0,
    FOREIGN KEY (root_path, path)
        REFERENCES repo_index_files(root_path, path) ON DELETE CASCADE
);
CREATE INDEX repo_index_symbols_name ON repo_index_symbols(root_path, name);
CREATE INDEX repo_index_symbols_path ON repo_index_symbols(root_path, path);

CREATE TABLE repo_index_meta (
    root_path TEXT PRIMARY KEY,
    indexer_version INTEGER NOT NULL,
    source TEXT NOT NULL DEFAULT '',
    file_count INTEGER NOT NULL DEFAULT 0,
    symbol_count INTEGER NOT NULL DEFAULT 0,
    truncated INTEGER NOT NULL DEFAULT 0,
    refreshed_at TEXT NOT NULL
);
`

const backgroundExecutionSchema = `
CREATE TABLE task_attempts (
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    attempt INTEGER NOT NULL CHECK (attempt > 0),
    owner TEXT NOT NULL,
    thread_id TEXT,
    turn_id TEXT,
    status TEXT NOT NULL,
    reason TEXT,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    PRIMARY KEY (task_id, attempt)
);
CREATE INDEX task_attempts_owner ON task_attempts(owner, status);

CREATE TABLE workflow_runs (
    id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    task_id TEXT REFERENCES tasks(id) ON DELETE SET NULL,
    spec_hash TEXT NOT NULL,
    spec_json TEXT NOT NULL,
    status TEXT NOT NULL,
    goal TEXT NOT NULL DEFAULT '',
    error TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    CHECK (json_valid(spec_json))
);
CREATE INDEX workflow_runs_session_created ON workflow_runs(session_id, created_at);
CREATE INDEX workflow_runs_status_updated ON workflow_runs(status, updated_at);

CREATE TABLE workflow_nodes (
    run_id TEXT NOT NULL REFERENCES workflow_runs(id) ON DELETE CASCADE,
    node_id TEXT NOT NULL,
    status TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    output_handle TEXT,
    reason TEXT,
    started_at TEXT,
    ended_at TEXT,
    PRIMARY KEY (run_id, node_id)
);
CREATE INDEX workflow_nodes_run_status ON workflow_nodes(run_id, status);
`

// A local trace has one row per span, keyed by the turn it belongs to.
//
// A trace is exactly one turn, so turn_id is the correlation key and there is no
// separate trace id: a column that only ever repeated turn_id would be one more
// thing to keep in agreement with it. Run-level traces spanning several turns
// (RFC-008 §9.3) would add a parent id here, and until something reads one, not
// having it is the honest state.
//
// span_id is a per-turn counter rather than a random identifier so a trace reads
// in the order it happened and a test can name a span.
//
// Deleting the turn deletes its spans, which is how a deleted session takes its
// traces with it: turns cascade from threads, and threads from sessions. Nothing
// else expires them — span retention is the wider event-retention question
// (ROADMAP §8.5) and this table has the same gap the event log does.
const traceSchema = `
CREATE TABLE spans (
    turn_id TEXT NOT NULL REFERENCES turns(id) ON DELETE CASCADE,
    span_id INTEGER NOT NULL CHECK (span_id > 0),
    -- parent_span_id is null for the turn's root span.
    parent_span_id INTEGER,
    name TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    duration_ms INTEGER CHECK (duration_ms IS NULL OR duration_ms >= 0),
    -- status is ok / error / canceled, or open for a span whose own code path
    -- never closed it.
    status TEXT NOT NULL,
    attributes_json TEXT NOT NULL DEFAULT '{}',
    PRIMARY KEY (turn_id, span_id),
    CHECK (json_valid(attributes_json))
);
CREATE INDEX spans_turn_started ON spans(turn_id, started_at);
CREATE INDEX spans_name ON spans(name);
`

const providerCapabilitySchema = `
CREATE TABLE provider_capabilities (
    provider_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    capability TEXT NOT NULL,
    supported INTEGER NOT NULL CHECK (supported IN (0, 1)),
    source TEXT NOT NULL,
    detail TEXT,
    observed_at TEXT NOT NULL,
    PRIMARY KEY (provider_id, model_id, capability)
);
CREATE INDEX provider_capabilities_model ON provider_capabilities(provider_id, model_id);
`
