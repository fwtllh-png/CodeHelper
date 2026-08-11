package turnstate

import (
	"errors"
	"testing"
	"time"

	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
)

func TestC1ActiveTurnLeaseSerializesRecoveryAndExpires(t *testing.T) {
	database, err := sqlitestate.Open(
		t.Context(),
		t.TempDir()+"/state.db",
		sqlitestate.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	seedActiveTurn(t, database, "turn-lease", "thread-lease")

	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	store := NewSQLiteRepository(database)
	store.now = func() time.Time { return now }
	lease := 30 * time.Second

	first, err := store.ClaimActiveTurns(t.Context(), "owner-a", lease)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 || first[0].TurnID != "turn-lease" {
		t.Fatalf("first claim = %+v", first)
	}
	second, err := store.ClaimActiveTurns(t.Context(), "owner-b", lease)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("concurrent claim = %+v", second)
	}
	if err := store.ClaimTurn(
		t.Context(),
		"turn-lease",
		"owner-b",
		lease,
	); !errors.Is(err, ErrTurnLeaseHeld) {
		t.Fatalf("held lease error = %v", err)
	}

	now = now.Add(lease + time.Nanosecond)
	second, err = store.ClaimActiveTurns(t.Context(), "owner-b", lease)
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 || second[0].TurnID != "turn-lease" {
		t.Fatalf("expired claim = %+v", second)
	}
	if err := store.RenewTurns(
		t.Context(),
		"owner-a",
		[]string{"turn-lease"},
		lease,
	); !errors.Is(err, ErrTurnLeaseHeld) {
		t.Fatalf("stale owner renew error = %v", err)
	}
	if err := store.ReleaseTurns(
		t.Context(),
		"owner-b",
		[]string{"turn-lease"},
	); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimTurn(
		t.Context(),
		"turn-lease",
		"owner-a",
		lease,
	); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
}

func seedActiveTurn(
	t *testing.T,
	database *sqlitestate.Store,
	turnID string,
	threadID string,
) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO workspaces(
				id, root_path, display_name, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?)`,
			args: []any{"workspace-lease", t.TempDir(), "lease", now, now},
		},
		{
			query: `INSERT INTO sessions(
				id, workspace_id, status, created_at, updated_at
			) VALUES (?, ?, 'open', ?, ?)`,
			args: []any{"session-lease", "workspace-lease", now, now},
		},
		{
			query: `INSERT INTO threads(
				id, session_id, status, created_at, updated_at
			) VALUES (?, ?, 'open', ?, ?)`,
			args: []any{threadID, "session-lease", now, now},
		},
		{
			query: `INSERT INTO turns(
				id, thread_id, ordinal, status, created_at, updated_at
			) VALUES (?, ?, 1, 'active', ?, ?)`,
			args: []any{turnID, threadID, now, now},
		},
	} {
		if _, err := database.DB().ExecContext(
			t.Context(),
			statement.query,
			statement.args...,
		); err != nil {
			t.Fatal(err)
		}
	}
}
