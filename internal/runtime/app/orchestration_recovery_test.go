package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	threadstate "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/thread"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestPersistentRuntimeReplaysAcceptedWorkGraphOperation(t *testing.T) {
	store, err := state.Open(t.Context(), state.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })
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
		RunID: "run-recovery", Kind: "workflow", Source: "host",
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
	lifecycle := threadstate.NewLifecycle(store)
	if _, err := lifecycle.Accept(
		t.Context(),
		operation,
		"",
		canonical,
	); err != nil {
		t.Fatal(err)
	}
	workGraphs, err := orchestrationstore.Open(t.Context(), store.SQLite())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := app.PrepareRuntimeWithRecovery(t.Context(), app.Options{
		EventStore: store, ContentStore: store.Content(),
		Lifecycle:     lifecycle,
		TerminalStore: turnstate.NewSQLiteRepository(store.SQLite()),
		Orchestration: workGraphs,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var status string
	for {
		graph, loadErr := workGraphs.Load(t.Context(), "run-recovery")
		if loadErr == nil {
			if graph.Run.Revision != 1 {
				t.Fatalf("recovered graph = %+v", graph)
			}
			status = ""
			if err := store.SQLite().DB().QueryRowContext(
				t.Context(),
				`SELECT status FROM operations WHERE id = ?`,
				operation.ID,
			).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status == "committed" {
				break
			}
		} else if !errors.Is(loadErr, kernel.ErrNotFound) {
			t.Fatalf("load recovered work graph: %v", loadErr)
		}
		if time.Now().After(deadline) {
			t.Fatalf("accepted work graph operation was not replayed: %v", loadErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "committed" {
		t.Fatalf("recovered operation status = %q", status)
	}
}
