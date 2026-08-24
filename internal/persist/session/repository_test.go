package session_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/persist/session"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

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
	if err := repository.EnsureSeed(
		t.Context(),
		"malformed",
		t.TempDir(),
	); err != nil {
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
}

func TestRepositoryGetSurvivesRestartAndHonorsCancellation(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	const sessionID = "contract"
	if err := repository.EnsureSeed(t.Context(), sessionID, t.TempDir()); err != nil {
		t.Fatal(err)
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
	if persisted, err := repository.Get(t.Context(), sessionID); err != nil ||
		persisted.ID != sessionID {
		t.Fatalf("session after restart = %+v, error = %v", persisted, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := repository.Get(ctx, sessionID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Get error = %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := session.NewRepository(db).Get(t.Context(), sessionID); err == nil {
		t.Fatal("repository without schema succeeded")
	}
}
