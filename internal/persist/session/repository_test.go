package session_test

import (
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
