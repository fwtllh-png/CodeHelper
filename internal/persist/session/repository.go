// Package session provides typed access to durable workspaces and sessions.
package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

var (
	ErrNotFound          = errors.New("session record not found")
	ErrInvalidTransition = errors.New("invalid session state transition")
)

type Status string

const (
	StatusOpen   Status = "open"
	StatusClosed Status = "closed"
)

type Workspace struct {
	ID          string
	RootPath    string
	DisplayName string
	Metadata    json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Session struct {
	ID          string
	WorkspaceID string
	Status      Status
	Metadata    json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ClosedAt    *time.Time
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func NewSQLiteRepository(store *sqlitestate.Store) *Repository {
	if store == nil {
		return &Repository{}
	}
	return NewRepository(store.DB())
}

func (r *Repository) CreateWorkspace(ctx context.Context, workspace Workspace) (Workspace, error) {
	if r.db == nil {
		return Workspace{}, errors.New("session repository database is required")
	}
	if workspace.ID == "" || workspace.RootPath == "" {
		return Workspace{}, errors.New("workspace id and root path are required")
	}
	now := time.Now().UTC()
	if workspace.CreatedAt.IsZero() {
		workspace.CreatedAt = now
	}
	if workspace.UpdatedAt.IsZero() {
		workspace.UpdatedAt = workspace.CreatedAt
	}
	metadata, err := normalizedJSON(workspace.Metadata)
	if err != nil {
		return Workspace{}, fmt.Errorf("workspace metadata: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO workspaces(id, root_path, display_name, metadata_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		workspace.ID, workspace.RootPath, workspace.DisplayName, metadata,
		timestamp(workspace.CreatedAt), timestamp(workspace.UpdatedAt),
	)
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace: %w", err)
	}
	workspace.Metadata = metadata
	return workspace, nil
}

func (r *Repository) Create(ctx context.Context, value Session) (Session, error) {
	if r.db == nil {
		return Session{}, errors.New("session repository database is required")
	}
	if value.ID == "" || value.WorkspaceID == "" {
		return Session{}, errors.New("session id and workspace id are required")
	}
	if value.Status == "" {
		value.Status = StatusOpen
	}
	if value.Status != StatusOpen && value.Status != StatusClosed {
		return Session{}, fmt.Errorf("unsupported session status %q", value.Status)
	}
	now := time.Now().UTC()
	if value.CreatedAt.IsZero() {
		value.CreatedAt = now
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.CreatedAt
	}
	if value.Status == StatusClosed && value.ClosedAt == nil {
		closedAt := value.UpdatedAt
		value.ClosedAt = &closedAt
	}
	metadata, err := normalizedJSON(value.Metadata)
	if err != nil {
		return Session{}, fmt.Errorf("session metadata: %w", err)
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO sessions(
			id, workspace_id, status, metadata_json, created_at, updated_at, closed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.WorkspaceID, value.Status, metadata,
		timestamp(value.CreatedAt), timestamp(value.UpdatedAt), nullableTime(value.ClosedAt),
	)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	value.Metadata = metadata
	return value, nil
}

func (r *Repository) Get(ctx context.Context, id string) (Session, error) {
	if r.db == nil {
		return Session{}, errors.New("session repository database is required")
	}
	var value Session
	var metadata, createdAt, updatedAt string
	var closedAt sql.NullString
	err := r.db.QueryRowContext(ctx, `
		SELECT id, workspace_id, status, metadata_json, created_at, updated_at, closed_at
		FROM sessions WHERE id = ?`, id,
	).Scan(
		&value.ID, &value.WorkspaceID, &value.Status, &metadata,
		&createdAt, &updatedAt, &closedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("get session: %w", err)
	}
	value.Metadata = json.RawMessage(metadata)
	if value.CreatedAt, err = parseTime(createdAt); err != nil {
		return Session{}, err
	}
	if value.UpdatedAt, err = parseTime(updatedAt); err != nil {
		return Session{}, err
	}
	if closedAt.Valid {
		parsed, parseErr := parseTime(closedAt.String)
		if parseErr != nil {
			return Session{}, parseErr
		}
		value.ClosedAt = &parsed
	}
	return value, nil
}

func (r *Repository) Close(ctx context.Context, id string, at time.Time) (Session, error) {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE sessions
		SET status = ?, updated_at = ?, closed_at = ?
		WHERE id = ? AND status = ?`,
		StatusClosed, timestamp(at), timestamp(at), id, StatusOpen,
	)
	if err != nil {
		return Session{}, fmt.Errorf("close session: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Session{}, err
	}
	if affected == 0 {
		current, getErr := r.Get(ctx, id)
		if getErr != nil {
			return Session{}, getErr
		}
		if current.Status == StatusClosed {
			return current, nil
		}
		return Session{}, ErrInvalidTransition
	}
	return r.Get(ctx, id)
}

// Filter selects sessions for List (B5 session index).
type Filter struct {
	WorkspaceID string
	Status      Status
	Limit       int
}

// List returns sessions newest-first for resume/search UX.
func (r *Repository) List(ctx context.Context, filter Filter) ([]Session, error) {
	if r.db == nil {
		return nil, errors.New("session repository database is required")
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	query := `
		SELECT id, workspace_id, status, metadata_json, created_at, updated_at, closed_at
		FROM sessions`
	args := make([]any, 0, 3)
	where := make([]string, 0, 2)
	if filter.WorkspaceID != "" {
		where = append(where, "workspace_id = ?")
		args = append(args, filter.WorkspaceID)
	}
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if len(where) > 0 {
		query += " WHERE " + where[0]
		for _, clause := range where[1:] {
			query += " AND " + clause
		}
	}
	query += " ORDER BY updated_at DESC LIMIT ?"
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var value Session
		var metadata []byte
		var createdAt, updatedAt string
		var closedAt sql.NullString
		if err := rows.Scan(
			&value.ID, &value.WorkspaceID, &value.Status, &metadata,
			&createdAt, &updatedAt, &closedAt,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		value.Metadata = json.RawMessage(metadata)
		if value.CreatedAt, err = parseTime(createdAt); err != nil {
			return nil, err
		}
		if value.UpdatedAt, err = parseTime(updatedAt); err != nil {
			return nil, err
		}
		if closedAt.Valid {
			parsed, parseErr := parseTime(closedAt.String)
			if parseErr != nil {
				return nil, parseErr
			}
			value.ClosedAt = &parsed
		}
		out = append(out, value)
	}
	return out, rows.Err()
}

func normalizedJSON(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var decoded any
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func timestamp(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return timestamp(*value)
}

func parseTime(value string) (time.Time, error) {
	result, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse persisted timestamp: %w", err)
	}
	return result, nil
}
