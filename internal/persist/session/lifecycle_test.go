package session_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/session"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestLifecycleListSearchUpdateAndDeleteProtection(t *testing.T) {
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
		ID: "workspace", RootPath: "/workspace", DisplayName: "fixture",
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, value := range []struct {
		sessionID string
		threadID  string
		title     string
		metadata  string
	}{
		{
			"session-a", "thread-a", "Fix login",
			`{"transport":"acp","isolation":"worktree","provider":"fixture","model":"model"}`,
		},
		{
			"session-b", "thread-b", "Review API",
			`{"transport":"acp","provider":"fixture","model":"model"}`,
		},
	} {
		if _, err := repository.Create(t.Context(), session.Session{
			ID: value.sessionID, WorkspaceID: workspace.ID,
			Metadata:  json.RawMessage(value.metadata),
			CreatedAt: now, UpdatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(t.Context(), `
			INSERT INTO threads(
				id, session_id, title, status, created_at, updated_at
			) VALUES (?, ?, ?, 'open', ?, ?)`,
			value.threadID, value.sessionID, value.title,
			now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO turns(
			id, thread_id, ordinal, status, created_at, updated_at, completed_at
		) VALUES ('turn-a', 'thread-a', 1, 'completed', ?, ?, ?)`,
		now.Format(time.RFC3339Nano),
		now.Add(time.Minute).Format(time.RFC3339Nano),
		now.Add(time.Minute).Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO items(
			id, turn_id, ordinal, kind, payload_json, created_at, updated_at
		) VALUES (
			'item-a', 'turn-a', 1, 'message',
			'{"text":"payment-sentinel"}', ?, ?
		)`,
		now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	); err != nil {
		t.Fatal(err)
	}

	found, err := repository.ListLifecycle(t.Context(), session.LifecycleQuery{
		WorkspaceRoot: "/workspace", Query: "PAYMENT-SENTINEL", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].SessionID != "session-a" ||
		found[0].MatchTurnID != "turn-a" ||
		found[0].Status != protocol.SessionStatusCompleted {
		t.Fatalf("search result = %+v", found)
	}

	title := "Fix authentication"
	pinned, archived := true, true
	updated, err := repository.UpdateLifecycle(
		t.Context(),
		"session-a",
		1,
		protocol.SessionLifecyclePatch{
			Title: &title, Pinned: &pinned, Archived: &archived,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Title != title ||
		!updated.Pinned || !updated.Archived {
		t.Fatalf("updated lifecycle = %+v", updated)
	}
	if _, err := repository.UpdateLifecycle(
		t.Context(),
		"session-a",
		1,
		protocol.SessionLifecyclePatch{Pinned: &pinned},
	); !errors.Is(err, session.ErrLifecycleRevisionConflict) {
		t.Fatalf("stale lifecycle update error = %v", err)
	}
	visible, err := repository.ListLifecycle(t.Context(), session.LifecycleQuery{
		WorkspaceRoot: "/workspace", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 1 || visible[0].SessionID != "session-b" {
		t.Fatalf("visible sessions = %+v", visible)
	}

	archived = false
	updated, err = repository.UpdateLifecycle(
		t.Context(),
		"session-a",
		2,
		protocol.SessionLifecyclePatch{Archived: &archived},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 3 || updated.Archived {
		t.Fatalf("restored lifecycle = %+v", updated)
	}
	if _, err := repository.DeleteLifecycle(
		t.Context(),
		"session-b",
		1,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.DeleteLifecycle(
		t.Context(),
		"session-a",
		3,
	); err == nil {
		t.Fatal("last open session was deleted")
	}
}
