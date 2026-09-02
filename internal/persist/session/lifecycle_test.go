package session_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/persist/session"
	sqlitestate "github.com/fwtllh-png/QCode/internal/persist/state/sqlite"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestCreateLifecycleAtomicallySeedsWorkspaceSessionAndThread(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	created, err := repository.CreateLifecycle(
		t.Context(),
		protocol.SessionCreateSeed{
			Version:   protocol.SessionLifecycleVersion,
			SessionID: "session-web", WorkspaceID: "workspace-web",
			WorkspaceRoot: "/workspace", WorkspaceLabel: "workspace",
			ThreadID: "thread-web", Title: "Web",
			Provider: "fixture", Model: "fixture", Isolation: "shared",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if created.SessionID != "session-web" ||
		created.ThreadID != "thread-web" ||
		created.WorkspaceRoot != "/workspace" ||
		created.Provider != "fixture" ||
		created.Model != "fixture" ||
		created.Revision != 1 {
		t.Fatalf("created lifecycle = %+v", created)
	}
	defaults := persistedProfile()
	defaults.Model = "fixture"
	profile, err := repository.EnsureProfile(
		t.Context(),
		"session-web",
		defaults,
	)
	if err != nil {
		t.Fatalf("initialize seeded lifecycle profile: %v", err)
	}
	if profile.Provider != "fixture" || profile.Model != "fixture" {
		t.Fatalf("initialized profile = %+v", profile)
	}
	var workspaces, sessions, threads int
	for query, target := range map[string]*int{
		"SELECT COUNT(*) FROM workspaces": &workspaces,
		"SELECT COUNT(*) FROM sessions":   &sessions,
		"SELECT COUNT(*) FROM threads":    &threads,
	} {
		if err := store.DB().QueryRowContext(t.Context(), query).Scan(target); err != nil {
			t.Fatal(err)
		}
	}
	if workspaces != 1 || sessions != 1 || threads != 1 {
		t.Fatalf(
			"seed counts workspace=%d session=%d thread=%d",
			workspaces,
			sessions,
			threads,
		)
	}
	var metadata string
	if err := store.DB().QueryRowContext(
		t.Context(),
		`SELECT metadata_json FROM sessions WHERE id = 'session-web'`,
	).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(metadata), &stored); err != nil {
		t.Fatal(err)
	}
	if stored["isolation"] != "" {
		t.Fatalf("shared isolation persisted as %#v, want legacy-compatible empty value", stored["isolation"])
	}
}

func TestLifecycleCanonicalizesWorkspaceAliases(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	repository := session.NewSQLiteRepository(store)
	created, err := repository.CreateLifecycle(
		t.Context(),
		protocol.SessionCreateSeed{
			Version:   protocol.SessionLifecycleVersion,
			SessionID: "session-alias", WorkspaceID: "workspace-alias",
			WorkspaceRoot: alias, WorkspaceLabel: "workspace",
			ThreadID: "thread-alias", Title: "Alias",
			Provider: "fixture", Model: "fixture", Isolation: "shared",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if created.WorkspaceRoot != physical {
		t.Fatalf("Workspace root = %q, want %q", created.WorkspaceRoot, physical)
	}
	list, err := repository.ListLifecycle(
		t.Context(),
		protocol.SessionListQuery{WorkspaceRoot: alias, Limit: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 ||
		list.Sessions[0].SessionID != created.SessionID {
		t.Fatalf("alias Workspace sessions = %+v", list.Sessions)
	}
}

func TestPresentationReadFenceBindsLifecycleThreadsAndEventWatermark(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	if _, err := repository.CreateLifecycle(
		t.Context(),
		protocol.SessionCreateSeed{
			Version:   protocol.SessionLifecycleVersion,
			SessionID: "session-fence", WorkspaceID: "workspace-fence",
			WorkspaceRoot: "/workspace", WorkspaceLabel: "workspace",
			ThreadID: "thread-fence", Title: "Fence",
			Provider: "fixture", Model: "fixture", Isolation: "shared",
		},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO turns(
			id, thread_id, ordinal, status, created_at, updated_at
		) VALUES ('turn-fence', 'thread-fence', 1, 'completed', ?, ?)`,
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO event_reservations(
			sequence, event_id, status, created_at, updated_at
		) VALUES (7, 'event-fence', 'abandoned', ?, ?)`,
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}

	fence, err := repository.PresentationReadFence(
		t.Context(),
		"session-fence",
	)
	if err != nil {
		t.Fatal(err)
	}
	if fence.ThroughSequence != 7 ||
		fence.Session.Revision != 1 ||
		fence.Session.ThreadID != "thread-fence" ||
		len(fence.ThreadIDs) != 1 ||
		fence.ThreadIDs[0] != "thread-fence" {
		t.Fatalf("presentation read fence = %+v", fence)
	}
	turnIDs, err := repository.TurnIDs(t.Context(), "session-fence")
	if err != nil {
		t.Fatal(err)
	}
	if len(turnIDs) != 1 || turnIDs[0] != "turn-fence" {
		t.Fatalf("turn ids = %v", turnIDs)
	}
}

func TestLifecycleProjectsBlockedTurn(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	if _, err := repository.CreateLifecycle(
		t.Context(),
		protocol.SessionCreateSeed{
			Version:        protocol.SessionLifecycleVersion,
			SessionID:      "session-blocked",
			WorkspaceID:    "workspace-blocked",
			WorkspaceRoot:  "/workspace",
			WorkspaceLabel: "workspace",
			ThreadID:       "thread-blocked",
			Title:          "Blocked",
			Provider:       "fixture",
			Model:          "fixture",
			Isolation:      "shared",
		},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO turns(
			id, thread_id, ordinal, status, created_at, updated_at
		) VALUES ('turn-blocked', 'thread-blocked', 1, 'blocked', ?, ?)`,
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}

	summary, err := repository.GetLifecycle(t.Context(), "session-blocked")
	if err != nil {
		t.Fatal(err)
	}
	if summary.Status != protocol.SessionStatusBlocked {
		t.Fatalf("session status = %s, want blocked", summary.Status)
	}
}

func TestLifecycleRejectsArchiveAndDeleteWithActiveTurn(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	if _, err := repository.CreateLifecycle(
		t.Context(),
		protocol.SessionCreateSeed{
			Version:   protocol.SessionLifecycleVersion,
			SessionID: "session-active", WorkspaceID: "workspace-active",
			WorkspaceRoot: "/workspace", WorkspaceLabel: "workspace",
			ThreadID: "thread-active", Title: "Active",
			Provider: "fixture", Model: "fixture", Isolation: "shared",
		},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO turns(
			id, thread_id, ordinal, status, created_at, updated_at
		) VALUES ('turn-active', 'thread-active', 1, 'active', ?, ?)`,
		now,
		now,
	); err != nil {
		t.Fatal(err)
	}
	archived := true
	if _, err := repository.UpdateLifecycle(
		t.Context(),
		"session-active",
		1,
		protocol.SessionLifecyclePatch{Archived: &archived},
	); err == nil {
		t.Fatal("session with an active turn was archived")
	}
	if _, err := repository.DeleteLifecycle(
		t.Context(),
		"session-active",
		1,
	); err == nil {
		t.Fatal("session with an active turn was deleted")
	}
}

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
	now := time.Now().UTC()
	for _, value := range []struct {
		sessionID string
		threadID  string
		title     string
		metadata  string
	}{
		{
			"session-a", "thread-a", "Fix login",
			`{"transport":"web","isolation":"worktree","provider":"fixture","model":"model"}`,
		},
		{
			"session-b", "thread-b", "Review API",
			`{"transport":"web","provider":"fixture","model":"model"}`,
		},
	} {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(value.metadata), &metadata); err != nil {
			t.Fatal(err)
		}
		isolation, _ := metadata["isolation"].(string)
		if isolation == "" {
			isolation = "shared"
		}
		if _, err := repository.CreateLifecycle(
			t.Context(),
			protocol.SessionCreateSeed{
				Version:   protocol.SessionLifecycleVersion,
				SessionID: value.sessionID, WorkspaceID: "workspace",
				WorkspaceRoot: "/workspace", WorkspaceLabel: "fixture",
				ThreadID: protocol.ThreadID(value.threadID), Title: value.title,
				Provider: "fixture", Model: "model", Isolation: isolation,
			},
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
	if len(found.Sessions) != 1 ||
		found.Sessions[0].SessionID != "session-a" ||
		len(found.Matches) != 1 ||
		found.Matches[0].SessionID != "session-a" ||
		found.Matches[0].TurnID != "turn-a" ||
		found.Sessions[0].Status != protocol.SessionStatusCompleted {
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
	if len(visible.Sessions) != 1 ||
		visible.Sessions[0].SessionID != "session-b" {
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
	if _, err := repository.DiscardLifecycle(
		t.Context(),
		"session-a",
		3,
	); err != nil {
		t.Fatal(err)
	}
	remaining, err := repository.ListLifecycle(
		t.Context(),
		protocol.SessionListQuery{WorkspaceRoot: "/workspace"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining.Sessions) != 0 {
		t.Fatalf("discarded final session remained: %+v", remaining.Sessions)
	}
}

func TestLifecyclePersistsTheActiveForkThread(t *testing.T) {
	store, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := session.NewSQLiteRepository(store)
	if _, err := repository.CreateLifecycle(
		t.Context(),
		protocol.SessionCreateSeed{
			Version:   protocol.SessionLifecycleVersion,
			SessionID: "session", WorkspaceID: "workspace",
			WorkspaceRoot: "/workspace", WorkspaceLabel: "fixture",
			ThreadID: "root", Title: "Root",
			Provider: "fixture", Model: "model", Isolation: "shared",
		},
	); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := store.DB().ExecContext(t.Context(), `
		INSERT INTO threads(
			id, session_id, parent_thread_id, title, status,
			source_cursor, created_at, updated_at
		) VALUES (
			'child', 'session', 'root', 'Checkpoint Fork', 'open', 8, ?, ?
		)`,
		now, now,
	); err != nil {
		t.Fatal(err)
	}
	activated, err := repository.ActivateThread(
		t.Context(),
		"session",
		"child",
	)
	if err != nil {
		t.Fatal(err)
	}
	if activated.ThreadID != "child" ||
		activated.ParentThreadID != "root" ||
		activated.Title != "Checkpoint Fork" ||
		activated.Revision != 2 {
		t.Fatalf("activated lifecycle = %+v", activated)
	}
	reopened := session.NewSQLiteRepository(store)
	recovered, err := reopened.GetLifecycle(t.Context(), "session")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.ThreadID != "child" ||
		recovered.ParentThreadID != "root" {
		t.Fatalf("recovered active Thread = %+v", recovered)
	}
}
