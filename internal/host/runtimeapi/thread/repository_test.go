package thread

import (
	"path/filepath"
	"testing"
	"time"

	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestLifecycleAcceptsWorkGraphOperationWithoutTurnRow(t *testing.T) {
	store, err := state.Open(t.Context(), state.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(t.Context()) })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace', '/workspace', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session', 'workspace', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, title, status, created_at, updated_at)
		 VALUES ('thread', 'session', 'chat', 'open', ?, ?)`,
	} {
		if _, err := store.SQLite().DB().ExecContext(
			t.Context(),
			statement,
			now,
			now,
		); err != nil {
			t.Fatal(err)
		}
	}
	operation, err := protocol.NewOperation(&protocol.SubmitRunPayload{
		ThreadID: "thread", TurnID: "control-turn", ItemID: "control-item",
		RunID: "run", Kind: "workflow", Source: "host",
		SessionID: "session", RootThreadID: "thread",
		Nodes: []protocol.RunNodeSpec{{ID: "node", Kind: "agent_turn"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := app.CanonicalOperationPayload(operation)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := NewLifecycle(store).Accept(
		t.Context(),
		operation,
		"",
		canonical,
	)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.OperationID != operation.ID || accepted.Duplicate {
		t.Fatalf("acceptance = %+v", accepted)
	}
	var turnCount, itemCount int
	if err := store.SQLite().DB().QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM turns`,
	).Scan(&turnCount); err != nil {
		t.Fatal(err)
	}
	if err := store.SQLite().DB().QueryRowContext(
		t.Context(),
		`SELECT COUNT(*) FROM items`,
	).Scan(&itemCount); err != nil {
		t.Fatal(err)
	}
	if turnCount != 0 || itemCount != 0 {
		t.Fatalf("work graph operation created turns=%d items=%d", turnCount, itemCount)
	}
}

func TestCreateSeedReusesWorkspaceRoot(t *testing.T) {
	store, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := NewRepository(store.DB())
	for _, value := range []struct {
		workspace string
		session   string
		thread    string
	}{
		{"workspace-one", "session-one", "thread-one"},
		{"workspace-two", "session-two", "thread-two"},
	} {
		if _, err := repository.CreateSeed(
			t.Context(),
			sessionstate.Workspace{ID: value.workspace, RootPath: "/workspace"},
			sessionstate.Session{ID: value.session, WorkspaceID: value.workspace},
			Thread{ID: protocol.ThreadID(value.thread), SessionID: value.session},
		); err != nil {
			t.Fatal(err)
		}
	}
	var workspaceCount int
	if err := store.DB().QueryRowContext(
		t.Context(), `SELECT COUNT(*) FROM workspaces`,
	).Scan(&workspaceCount); err != nil {
		t.Fatal(err)
	}
	if workspaceCount != 1 {
		t.Fatalf("workspace count = %d, want 1", workspaceCount)
	}
	var distinctWorkspaceCount int
	if err := store.DB().QueryRowContext(t.Context(), `
		SELECT COUNT(DISTINCT workspace_id) FROM sessions`,
	).Scan(&distinctWorkspaceCount); err != nil {
		t.Fatal(err)
	}
	if distinctWorkspaceCount != 1 {
		t.Fatalf(
			"session workspace count = %d, want 1",
			distinctWorkspaceCount,
		)
	}
	if err := repository.Rename(t.Context(), "thread-two", "修复登录问题"); err != nil {
		t.Fatal(err)
	}
	renamed, err := repository.Get(t.Context(), "thread-two")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "修复登录问题" {
		t.Fatalf("renamed title = %q", renamed.Title)
	}
}

func TestListIsBoundedAndWorkspaceScoped(t *testing.T) {
	store, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace-a', '/workspace/a', ?, ?)`,
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace-b', '/workspace/b', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session-a', 'workspace-a', 'open', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session-b', 'workspace-b', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, title, status, created_at, updated_at)
		 VALUES ('thread-a', 'session-a', 'a', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, title, status, created_at, updated_at)
		 VALUES ('thread-b', 'session-b', 'b', 'open', ?, ?)`,
	} {
		if _, err := store.DB().ExecContext(t.Context(), statement, now, now); err != nil {
			t.Fatal(err)
		}
	}
	repository := NewRepository(store.DB())
	values, err := repository.List(t.Context(), Filter{WorkspaceRoot: "/workspace/a"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].ID != "thread-a" {
		t.Fatalf("workspace-scoped threads = %+v", values)
	}
	if _, err := repository.GetInWorkspace(
		t.Context(), "thread-b", "/workspace/a",
	); err != ErrNotFound {
		t.Fatalf("foreign workspace get error = %v", err)
	}
	if _, err := repository.List(t.Context(), Filter{}, 1001); err == nil {
		t.Fatal("oversized thread list succeeded")
	}
}

func TestHistoryCursorBoundsNewestTurns(t *testing.T) {
	store, err := sqlitestate.Open(t.Context(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	statements := []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace', '/workspace', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session', 'workspace', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, title, status, created_at, updated_at)
		 VALUES ('thread', 'session', 'chat', 'open', ?, ?)`,
		`INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		 VALUES ('turn-1', 'thread', 1, 'completed', ?, ?)`,
		`INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		 VALUES ('turn-2', 'thread', 2, 'completed', ?, ?)`,
	}
	for _, statement := range statements {
		if _, err := store.DB().ExecContext(t.Context(), statement, now, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, value := range []struct {
		sequence int
		eventID  string
		turnID   string
	}{
		{5, "event-5", "turn-1"},
		{10, "event-10", "turn-2"},
	} {
		if _, err := store.DB().ExecContext(t.Context(), `
			INSERT INTO event_index(
				sequence, event_id, thread_id, turn_id, kind,
				log_offset, log_length, sha256, created_at
			) VALUES (?, ?, 'thread', ?, 'turn.started', 0, 1, ?, ?)`,
			value.sequence, value.eventID, value.turnID,
			"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			now,
		); err != nil {
			t.Fatal(err)
		}
	}
	repository := NewRepository(store.DB())
	cursor, err := repository.HistoryCursor(t.Context(), "thread", 1)
	if err != nil {
		t.Fatal(err)
	}
	if cursor != 9 {
		t.Fatalf("HistoryCursor() = %d, want 9", cursor)
	}
}

func TestRecoverRequeuesCommittedStartForActiveTurnWithoutTerminal(t *testing.T) {
	store, err := state.Open(t.Context(), state.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(t.Context()) })
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace', '/workspace', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session', 'workspace', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, title, status, created_at, updated_at)
		 VALUES ('thread', 'session', 'chat', 'open', ?, ?)`,
		`INSERT INTO operations(
			id, session_id, kind, status, request_json, created_at, updated_at
		 ) VALUES ('operation', 'session', 'turn.start', 'committed', '{}', ?, ?)`,
		`INSERT INTO turns(
			id, thread_id, operation_id, ordinal, status, created_at, updated_at
		 ) VALUES ('turn', 'thread', 'operation', 1, 'active', ?, ?)`,
	} {
		if _, err := store.SQLite().DB().ExecContext(
			t.Context(), statement, now, now,
		); err != nil {
			t.Fatal(err)
		}
	}

	recovery, err := NewLifecycle(store).Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	pending, ok := recovery.PendingOperations["operation"]
	if !ok || pending.SessionID != "session" ||
		string(pending.Canonical) != "{}" {
		t.Fatalf("pending operations = %+v, want interrupted start", recovery.PendingOperations)
	}
}
