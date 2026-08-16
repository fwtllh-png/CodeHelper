package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestStoreCommandDeduplicationCASAndRebuild(t *testing.T) {
	value := openStore(t)
	runID := protocol.RunID("run-store")
	submit := storeSubmit(runID)
	first, err := value.Execute(t.Context(), submit)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := value.Execute(t.Context(), submit)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate || duplicate.Graph.Run.Revision != 1 {
		t.Fatalf("duplicate result = %+v", duplicate)
	}
	conflict := submit
	conflict.RunID = "run-other"
	if _, err := value.Execute(t.Context(), conflict); !errors.Is(err, ErrCommandConflict) {
		t.Fatalf("command identity conflict = %v", err)
	}
	if _, err := value.Execute(t.Context(), kernel.Command{
		ID: "stale", Kind: kernel.CommandSkipNode, RunID: runID,
		NodeID: "node", ExpectedRevision: 99,
		At: time.Unix(501, 0).UTC(), Reason: "skip",
	}); !errors.Is(err, kernel.ErrConflict) {
		t.Fatalf("revision conflict = %v", err)
	}
	rebuilt, err := value.Rebuild(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Run.Revision != first.Graph.Run.Revision ||
		rebuilt.Nodes["node"].State != protocol.NodeStateReady {
		t.Fatalf("rebuilt graph = %+v", rebuilt)
	}
}

func TestStoreTerminalCommitAndOutboxAreAtomic(t *testing.T) {
	value := openStore(t)
	runID := protocol.RunID("run-terminal")
	graph := executeLifecycleUntilRunning(t, value, runID)
	if graph.Run.Revision != 3 {
		t.Fatalf("running revision = %d", graph.Run.Revision)
	}
	if _, err := value.Rebuild(t.Context(), runID); err != nil {
		t.Fatalf("rebuild running graph: %v", err)
	}
	if _, err := value.db.ExecContext(t.Context(), `
		CREATE TRIGGER fail_terminal_outbox
		BEFORE INSERT ON work_outbox
		WHEN NEW.kind = 'publish_run_terminal'
		BEGIN
			SELECT RAISE(ABORT, 'injected terminal outbox failure');
		END`); err != nil {
		t.Fatal(err)
	}
	settle := kernel.Command{
		ID: "settle", Kind: kernel.CommandSettleExecution,
		RunID: runID, AttemptID: "attempt", ExpectedRevision: 3,
		At:         time.Unix(504, 0).UTC(),
		LeaseOwner: "worker", LeaseEpoch: 1,
		Settlement: &kernel.SettlementData{State: protocol.NodeStateSucceeded},
	}
	if _, err := value.Execute(t.Context(), settle); err == nil {
		t.Fatal("injected terminal outbox failure was ignored")
	}
	stored, err := value.Load(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Run.Revision != 3 ||
		stored.Run.State != protocol.RunStateActive ||
		stored.Nodes["node"].State != protocol.NodeStateRunning {
		t.Fatalf("terminal transaction partially committed: %+v", stored)
	}
	if _, err := value.db.ExecContext(
		t.Context(),
		`DROP TRIGGER fail_terminal_outbox`,
	); err != nil {
		t.Fatal(err)
	}
	settled, err := value.Execute(t.Context(), settle)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Graph.Run.State != protocol.RunStateCompleted {
		t.Fatalf("settled run = %+v", settled.Graph.Run)
	}
	pending, err := value.PendingEffects(t.Context(), runID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 ||
		pending[0].Effect.Kind != model.EffectPublishTerminal {
		t.Fatalf("outbox = %+v", pending)
	}
	global, err := value.PendingTerminalEffects(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(global) != 1 || global[0].Effect.ID != pending[0].Effect.ID {
		t.Fatalf("global terminal outbox = %+v", global)
	}
	if _, err := value.Execute(t.Context(), kernel.Command{
		ID: "publish-terminal", Kind: kernel.CommandPublishEffect,
		RunID: runID, EffectID: pending[0].Effect.ID,
		ExpectedRevision: settled.Graph.Run.Revision,
		At:               time.Unix(404, 0).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	pending, err = value.PendingEffects(t.Context(), runID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("published outbox remains pending = %+v", pending)
	}
}

func TestStoreConcurrentRevisionHasOneWinner(t *testing.T) {
	value := openStore(t)
	runID := protocol.RunID("run-concurrent")
	if _, err := value.Execute(t.Context(), storeSubmit(runID)); err != nil {
		t.Fatal(err)
	}
	commands := []kernel.Command{
		{
			ID: "skip-a", Kind: kernel.CommandSkipNode, RunID: runID,
			NodeID: "node", ExpectedRevision: 1,
			At: time.Unix(510, 0).UTC(), Reason: "a",
		},
		{
			ID: "skip-b", Kind: kernel.CommandSkipNode, RunID: runID,
			NodeID: "node", ExpectedRevision: 1,
			At: time.Unix(511, 0).UTC(), Reason: "b",
		},
	}
	var wait sync.WaitGroup
	results := make(chan error, len(commands))
	for _, command := range commands {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := value.Execute(t.Context(), command)
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, kernel.ErrConflict):
			conflicts++
		default:
			t.Fatalf("concurrent command error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestAuditDetectsAndRepairsSnapshotDriftFromFacts(t *testing.T) {
	value := openStore(t)
	runID := protocol.RunID("run-repair")
	if _, err := value.Execute(t.Context(), storeSubmit(runID)); err != nil {
		t.Fatal(err)
	}
	graph, err := value.Load(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	graph.Run.Reason = "tampered projection"
	encoded, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := value.db.ExecContext(t.Context(), `
		UPDATE work_runs SET aggregate_json = ? WHERE run_id = ?`,
		encoded,
		runID,
	); err != nil {
		t.Fatal(err)
	}
	audit, err := value.Audit(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if !audit.Drift || audit.SnapshotDigest == audit.ReplayDigest {
		t.Fatalf("audit = %+v", audit)
	}
	factsBefore, err := value.Facts(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	commandsBefore := countStoreRows(t, value, "work_commands", runID)
	outboxBefore := countStoreRows(t, value, "work_outbox", runID)
	repaired, err := value.RepairSnapshot(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Drift || repaired.SnapshotDigest != repaired.ReplayDigest {
		t.Fatalf("repaired audit = %+v", repaired)
	}
	factsAfter, err := value.Facts(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(factsAfter) != len(factsBefore) {
		t.Fatalf("repair changed facts: before=%d after=%d", len(factsBefore), len(factsAfter))
	}
	if commandsAfter := countStoreRows(t, value, "work_commands", runID); commandsAfter != commandsBefore {
		t.Fatalf("repair changed command receipts: before=%d after=%d", commandsBefore, commandsAfter)
	}
	if outboxAfter := countStoreRows(t, value, "work_outbox", runID); outboxAfter != outboxBefore {
		t.Fatalf("repair changed outbox: before=%d after=%d", outboxBefore, outboxAfter)
	}
	loaded, err := value.Load(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Run.Reason != "" {
		t.Fatalf("repair retained projection drift: %+v", loaded.Run)
	}
}

func countStoreRows(t *testing.T, value *Store, table string, runID protocol.RunID) int {
	t.Helper()
	var count int
	if err := value.db.QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM "+table+" WHERE run_id = ?",
		runID,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func openStore(t *testing.T) *Store {
	t.Helper()
	sqlite, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	value, err := Open(t.Context(), sqlite)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func storeSubmit(runID protocol.RunID) kernel.Command {
	return kernel.Command{
		ID: "submit", Kind: kernel.CommandSubmit, RunID: runID,
		At: time.Unix(500, 0).UTC(),
		Submit: &kernel.SubmitData{
			Kind: model.RunKindWorkflow, Source: "test",
			SessionID: "session", RootThreadID: "thread",
			Nodes: []model.NodeSpec{{
				ID: "node", Kind: model.NodeKindAgentTurn,
			}},
		},
	}
}

func executeLifecycleUntilRunning(
	t *testing.T,
	value *Store,
	runID protocol.RunID,
) model.Graph {
	t.Helper()
	if _, err := value.Execute(t.Context(), storeSubmit(runID)); err != nil {
		t.Fatal(err)
	}
	if _, err := value.Execute(t.Context(), kernel.Command{
		ID: "claim", Kind: kernel.CommandClaimNode, RunID: runID,
		NodeID: "node", AttemptID: "attempt", EffectID: "effect",
		ExpectedRevision: 1, At: time.Unix(502, 0).UTC(),
		LeaseOwner: "worker", LeaseEpoch: 1,
		LeaseExpiresAt: storeTimePointer(time.Unix(562, 0).UTC()),
	}); err != nil {
		t.Fatal(err)
	}
	bound, err := value.Execute(t.Context(), kernel.Command{
		ID: "bind", Kind: kernel.CommandBindExecution, RunID: runID,
		AttemptID: "attempt", ExpectedRevision: 2,
		At:         time.Unix(503, 0).UTC(),
		LeaseOwner: "worker", LeaseEpoch: 1,
		Execution: &model.ExecutionRef{
			Kind: "process", EffectID: "effect", ProcessID: "process",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return bound.Graph
}

func storeTimePointer(value time.Time) *time.Time { return &value }
