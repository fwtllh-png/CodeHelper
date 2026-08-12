package session_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/session"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestRepositoryListNewestFirstAndFilters(t *testing.T) {
	store, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repo := session.NewSQLiteRepository(store)
	ctx := t.Context()
	ws, err := repo.CreateWorkspace(ctx, session.Workspace{ID: "ws", RootPath: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	older := time.Unix(100, 0).UTC()
	newer := time.Unix(200, 0).UTC()
	first, err := repo.Create(ctx, session.Session{
		ID: "s1", WorkspaceID: ws.ID, Status: session.StatusOpen,
		CreatedAt: older, UpdatedAt: older,
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(ctx, session.Session{
		ID: "s2", WorkspaceID: ws.ID, Status: session.StatusOpen,
		CreatedAt: newer, UpdatedAt: newer,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Close(ctx, first.ID, newer.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	listed, err := repo.List(ctx, session.Filter{WorkspaceID: ws.ID, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != first.ID {
		t.Fatalf("list = %+v, want closed/updated %s first", listed, first.ID)
	}
	openOnly, err := repo.List(ctx, session.Filter{
		WorkspaceID: ws.ID, Status: session.StatusOpen, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(openOnly) != 1 || openOnly[0].ID != second.ID {
		t.Fatalf("open filter = %+v", openOnly)
	}
}

func TestRepositoryFailsClosedOnMalformedStoredJSON(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	workspace, err := repository.CreateWorkspace(t.Context(), session.Workspace{
		ID: "workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Create(t.Context(), session.Session{
		ID: "malformed", WorkspaceID: workspace.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(
		t.Context(),
		"PRAGMA ignore_check_constraints = ON",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(
		t.Context(),
		"UPDATE sessions SET metadata_json = ? WHERE id = ?",
		`{"broken":`,
		"malformed",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(
		t.Context(),
		"PRAGMA ignore_check_constraints = OFF",
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Get(t.Context(), "malformed"); err == nil {
		t.Fatal("Get accepted malformed persisted session metadata")
	}
	if _, err := repository.List(
		t.Context(),
		session.Filter{WorkspaceID: workspace.ID},
	); err == nil {
		t.Fatal("List accepted malformed persisted session metadata")
	}
}

func TestRepositoryContractDuplicateCancelAndMissingSchema(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	workspace, err := repository.CreateWorkspace(t.Context(), session.Workspace{
		ID: "workspace", RootPath: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	value := session.Session{ID: "contract", WorkspaceID: workspace.ID}
	if _, err := repository.Create(t.Context(), value); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Create(t.Context(), value); err == nil {
		t.Fatal("duplicate session identity succeeded")
	}
	var storePath string
	if err := store.DB().QueryRowContext(
		t.Context(),
		"SELECT file FROM pragma_database_list WHERE name = 'main'",
	).Scan(&storePath); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := sqlitestate.Open(t.Context(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	repository = session.NewSQLiteRepository(reopened)
	if persisted, err := repository.Get(t.Context(), value.ID); err != nil ||
		persisted.ID != value.ID {
		t.Fatalf("session after restart = %+v, error = %v", persisted, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.Get(ctx, value.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Get error = %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := session.NewRepository(db).Get(t.Context(), value.ID); err == nil {
		t.Fatal("repository without schema succeeded")
	}
}
