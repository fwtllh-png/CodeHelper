package wire

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	threadstate "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/thread"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type persistentTestEngine struct {
	starts      atomic.Int64
	sideEffects atomic.Int64
	block       bool
}

func (e *persistentTestEngine) StartTurn(
	ctx context.Context,
	payload *protocol.StartTurnPayload,
	sink app.EngineSink,
) error {
	e.starts.Add(1)
	if err := sink.Emit(&protocol.TurnStartedData{Provider: "test", Model: "test"}); err != nil {
		return err
	}
	e.sideEffects.Add(1)
	if e.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return sink.Emit(&protocol.TurnCompletedData{Text: payload.Prompt})
}

func (*persistentTestEngine) CancelTurn(
	context.Context, *protocol.CancelTurnPayload, app.EngineSink,
) error {
	return nil
}

func (*persistentTestEngine) SteerTurn(
	context.Context, *protocol.SteerTurnPayload, app.EngineSink,
) error {
	return app.ErrOperationUnsupported
}

func (*persistentTestEngine) DecideApproval(
	context.Context, *protocol.ApprovalDecisionPayload, app.EngineSink,
) error {
	return app.ErrOperationUnsupported
}

func (*persistentTestEngine) ReplyInput(
	context.Context, *protocol.InputReplyPayload, app.EngineSink,
) error {
	return app.ErrOperationUnsupported
}

func (*persistentTestEngine) CompactThread(
	context.Context, *protocol.CompactThreadPayload, app.EngineSink,
) error {
	return app.ErrOperationUnsupported
}

func (*persistentTestEngine) ForkThread(
	context.Context, *protocol.ForkThreadPayload, app.EngineSink,
) error {
	return app.ErrOperationUnsupported
}

func (*persistentTestEngine) RevertTurn(
	context.Context, *protocol.RevertTurnPayload, app.EngineSink,
) error {
	return app.ErrOperationUnsupported
}

func TestPersistentRuntimeRestartIsIdempotentAndKeepsOneTerminal(t *testing.T) {
	root := t.TempDir()
	store := seedPersistentState(t, root)
	engine := &persistentTestEngine{}
	runtime, err := NewPersistentRuntime(t.Context(), PersistentRuntimeOptions{
		Store: store, Engine: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	operation := persistentStartOperation(t, "turn-1", "item-1")
	if err := runtime.SubmitWithKey(t.Context(), operation, "request-1"); err != nil {
		t.Fatal(err)
	}
	waitForTerminal(t, events, operation.ID)
	closePersistentRuntime(t, runtime)

	reopened, err := state.Open(t.Context(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := NewPersistentRuntime(t.Context(), PersistentRuntimeOptions{
		Store: reopened, Engine: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	before := recovered.Snapshot(t.Context())
	if err := recovered.SubmitWithKey(t.Context(), operation, "request-1"); err != nil {
		t.Fatal(err)
	}
	after := recovered.Snapshot(t.Context())
	if after.LastSequence != before.LastSequence || after.OperationsProcessed != 0 {
		t.Fatalf("duplicate changed runtime state: before=%+v after=%+v", before, after)
	}
	if engine.starts.Load() != 1 || engine.sideEffects.Load() != 1 {
		t.Fatalf(
			"engine starts=%d side effects=%d, want one each",
			engine.starts.Load(), engine.sideEffects.Load(),
		)
	}

	conflict := operation
	conflict.Payload = &protocol.StartTurnPayload{
		ThreadID: "thread-1", TurnID: "turn-1", ItemID: "item-1", Prompt: "different",
	}
	if err := recovered.SubmitWithKey(t.Context(), conflict, "request-1"); !errors.Is(err, app.ErrOperationConflict) {
		t.Fatalf("conflicting operation error = %v, want ErrOperationConflict", err)
	}
	replayed, err := reopened.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	terminals := 0
	for _, event := range replayed {
		if event.TurnID == "turn-1" && protocol.IsTerminalEvent(event.Kind) {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("terminal events = %d, want exactly one", terminals)
	}
	var operationStatus string
	var receipt sql.NullString
	if err := reopened.SQLite().DB().QueryRowContext(t.Context(), `
		SELECT status, response_json FROM operations WHERE id = ?`, operation.ID,
	).Scan(&operationStatus, &receipt); err != nil {
		t.Fatal(err)
	}
	if operationStatus != string(threadstate.OperationCommitted) || !receipt.Valid {
		t.Fatalf("operation status=%q receipt=%+v", operationStatus, receipt)
	}
	closePersistentRuntime(t, recovered)
}

func TestPersistentRuntimeEnforcesOneActiveTurnPerThread(t *testing.T) {
	store := seedPersistentState(t, t.TempDir())
	engine := &persistentTestEngine{block: true}
	runtime, err := NewPersistentRuntime(t.Context(), PersistentRuntimeOptions{
		Store: store, Engine: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	first := persistentStartOperation(t, "turn-active", "item-active")
	if err := runtime.Submit(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	waitForKind(t, events, protocol.EventTurnStarted)

	second := persistentStartOperation(t, "turn-other", "item-other")
	if err := runtime.Submit(t.Context(), second); !errors.Is(err, threadstate.ErrActiveTurn) {
		t.Fatalf("second active turn error = %v, want ErrActiveTurn", err)
	}
	closePersistentRuntime(t, runtime)

	reopened, err := state.Open(t.Context(), state.Options{DataDir: store.Root()})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.CloseAll(context.Background())
	replayed, err := reopened.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	terminals := 0
	for _, event := range replayed {
		if event.TurnID == "turn-active" && protocol.IsTerminalEvent(event.Kind) {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("active turn terminal events = %d, want one", terminals)
	}
}

func TestPersistentRuntimeRestoresPendingWithoutReplayingEngine(t *testing.T) {
	root := t.TempDir()
	store := seedPersistentState(t, root)
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	operation := persistentStartOperation(t, "turn-pending", "item-pending")
	canonical, err := app.CanonicalOperationPayload(operation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Lifecycle.Accept(
		t.Context(), operation, "pending-key", canonical,
	); err != nil {
		t.Fatal(err)
	}
	approval := &protocol.ApprovalRequiredData{
		RequestID:       "approval-pending",
		CallID:          "call-pending",
		Tool:            "shell",
		Arguments:       json.RawMessage(`{"command":"true"}`),
		ArgumentsDigest: "digest",
		AllowedScopes:   []protocol.ApprovalScope{protocol.ApprovalScopeOnce},
		ExpiresAt:       time.Now().Add(time.Hour).UTC(),
	}
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: operation.ID,
		ThreadID: "thread-1", TurnID: "turn-pending", ItemID: "item-pending",
	}, approval)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if err := store.CloseAll(t.Context()); err != nil {
		t.Fatal(err)
	}

	reopened, err := state.Open(t.Context(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	engine := &persistentTestEngine{}
	runtime, err := NewPersistentRuntime(t.Context(), PersistentRuntimeOptions{
		Store: reopened, Engine: engine,
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runtime.Snapshot(t.Context())
	if snapshot.PendingApprovals != 1 || snapshot.PendingOperations != 1 ||
		snapshot.LastSequence != 1 {
		t.Fatalf("recovered snapshot = %+v", snapshot)
	}
	if err := runtime.SubmitWithKey(t.Context(), operation, "pending-key"); err != nil {
		t.Fatal(err)
	}
	if engine.starts.Load() != 0 || engine.sideEffects.Load() != 0 {
		t.Fatal("pending accepted operation was replayed into Engine")
	}
	if runtime.Snapshot(t.Context()).LastSequence != 1 {
		t.Fatal("pending duplicate emitted a new event")
	}
	closePersistentRuntime(t, runtime)
}

func TestPersistentRuntimeMarksInterruptedTaskFailed(t *testing.T) {
	root := t.TempDir()
	store := seedPersistentState(t, root)
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Tasks.Create(t.Context(), taskstate.Task{
		ID: "task-running", SessionID: "session-1", ThreadID: "thread-1", Kind: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Tasks.Update(t.Context(), "task-running", taskstate.Transition{
		State: taskstate.StateRunning,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.CloseAll(t.Context()); err != nil {
		t.Fatal(err)
	}

	reopened, err := state.Open(t.Context(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewPersistentRuntime(t.Context(), PersistentRuntimeOptions{
		Store: reopened, Engine: &persistentTestEngine{},
	})
	if err != nil {
		t.Fatal(err)
	}
	recoveredRepositories, err := NewPersistentRepositories(reopened)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := recoveredRepositories.Tasks.Get(t.Context(), "task-running")
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State != taskstate.StateFailed ||
		recovered.FailureReason != "interrupted" ||
		recovered.LifecycleSequence != 3 {
		t.Fatalf("recovered task = %+v", recovered)
	}
	closePersistentRuntime(t, runtime)
}

func TestPersistentRuntimeDoesNotRecoverAnotherWorkersLiveLease(t *testing.T) {
	root := t.TempDir()
	store := seedPersistentState(t, root)
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.Tasks.Create(t.Context(), taskstate.Task{
		ID: "task-leased", SessionID: "session-1", ThreadID: "thread-1",
		Kind: "agent", Executor: taskstate.ExecutorAgentTurn, MaxAttempts: 2,
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := repositories.Tasks.Claim(t.Context(), taskstate.ClaimRequest{
		Owner: "worker-live", Executors: []string{taskstate.ExecutorAgentTurn},
		WorkspaceRoot: filepath.Join(root, "workspace"), Lease: time.Hour, Limit: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(claimed))
	}

	runtime, err := NewPersistentRuntime(t.Context(), PersistentRuntimeOptions{
		Store: store, Engine: &persistentTestEngine{},
	})
	if err != nil {
		t.Fatal(err)
	}
	leased, err := repositories.Tasks.Get(t.Context(), "task-leased")
	if err != nil {
		t.Fatal(err)
	}
	if leased.State != taskstate.StateRunning ||
		leased.LeaseOwner != "worker-live" ||
		leased.LeaseExpiresAt == nil {
		t.Fatalf("opening a runtime stole the live lease: %+v", leased)
	}
	closePersistentRuntime(t, runtime)
}

func TestPersistentRepositoriesProjectThreadLifecycleEvents(t *testing.T) {
	store := seedPersistentState(t, t.TempDir())
	defer store.CloseAll(context.Background())
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	accept := func(operation protocol.Operation) {
		t.Helper()
		canonical, err := app.CanonicalOperationPayload(operation)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repositories.Lifecycle.Accept(
			t.Context(), operation, "", canonical,
		); err != nil {
			t.Fatal(err)
		}
	}
	appendAndProject := func(sequence protocol.Cursor, operation protocol.Operation, data protocol.EventData) {
		t.Helper()
		threadID, turnID, itemID := protocol.OperationReferences(operation)
		event, err := protocol.NewEvent(protocol.EventMeta{
			Sequence: sequence, OperationID: operation.ID,
			ThreadID: threadID, TurnID: turnID, ItemID: itemID,
		}, data)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Append(t.Context(), event); err != nil {
			t.Fatal(err)
		}
		if err := repositories.Lifecycle.Project(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}

	target := persistentStartOperation(t, "turn-target", "item-target")
	accept(target)
	appendAndProject(1, target, &protocol.TurnCompletedData{Text: "target"})

	current := persistentStartOperation(t, "turn-current", "item-current")
	accept(current)
	appendAndProject(2, current, &protocol.TurnStartedData{Provider: "test", Model: "test"})
	appendAndProject(3, current, &protocol.UsageData{
		InputTokens: 9, OutputTokens: 4, ReasoningTokens: 2,
	})

	fork, err := protocol.NewOperation(&protocol.ForkThreadPayload{
		ThreadID: "thread-1", TurnID: "turn-current",
		ItemID: "item-fork", NewThreadID: "thread-fork",
	})
	if err != nil {
		t.Fatal(err)
	}
	accept(fork)
	appendAndProject(4, fork, &protocol.ThreadForkedData{
		NewThreadID: "thread-fork", SourceCursor: 3,
	})

	compact, err := protocol.NewOperation(&protocol.CompactThreadPayload{
		ThreadID: "thread-1", TurnID: "turn-current", ItemID: "item-compact",
	})
	if err != nil {
		t.Fatal(err)
	}
	accept(compact)
	appendAndProject(5, compact, &protocol.ThreadCompactedData{Summary: "summary"})

	revert, err := protocol.NewOperation(&protocol.RevertTurnPayload{
		ThreadID: "thread-1", TurnID: "turn-current",
		ItemID: "item-revert", TargetTurnID: "turn-target",
	})
	if err != nil {
		t.Fatal(err)
	}
	accept(revert)
	appendAndProject(6, revert, &protocol.TurnRevertedData{
		TargetTurnID: "turn-target",
	})

	parent, err := repositories.Threads.Get(t.Context(), "thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if parent.LatestCursor != 6 {
		t.Fatalf("latest cursor = %d, want 6", parent.LatestCursor)
	}
	forked, err := repositories.Threads.Get(t.Context(), "thread-fork")
	if err != nil {
		t.Fatal(err)
	}
	if forked.ParentThreadID != "thread-1" || forked.SessionID != "session-1" {
		t.Fatalf("forked thread = %+v", forked)
	}
	if forked.SourceCursor != 3 {
		t.Fatalf("forked source cursor = %d, want 3", forked.SourceCursor)
	}
	targetTurn, err := repositories.Threads.GetTurn(t.Context(), "turn-target")
	if err != nil {
		t.Fatal(err)
	}
	if targetTurn.Status != threadstate.TurnCompleted {
		t.Fatalf("target turn status = %q", targetTurn.Status)
	}
	compactedItem, err := repositories.Threads.GetItem(t.Context(), "item-compact")
	if err != nil {
		t.Fatal(err)
	}
	if compactedItem.Kind != string(protocol.EventThreadCompacted) {
		t.Fatalf("compaction item kind = %q", compactedItem.Kind)
	}
	aggregates, err := repositories.Usage.QueryAggregates(t.Context(), usagestate.Query{
		SessionID: "session-1", TurnID: "turn-current",
		Provider: "test", Model: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 || aggregates[0].InputTokens != 9 ||
		aggregates[0].OutputTokens != 4 || aggregates[0].ReasoningTokens != 2 {
		t.Fatalf("projected usage = %+v", aggregates)
	}
}

func TestPersistentRecoveryIgnoresEventsFromDeletedSessions(t *testing.T) {
	store := seedPersistentState(t, t.TempDir())
	defer store.CloseAll(context.Background())
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "operation-deleted",
		ThreadID: "thread-1", TurnID: "turn-deleted", ItemID: "item-deleted",
	}, &protocol.TurnStartedData{Provider: "test", Model: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQLite().DB().ExecContext(
		t.Context(),
		"DELETE FROM sessions WHERE id = 'session-1'",
	); err != nil {
		t.Fatal(err)
	}
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repositories.Sessions.Create(t.Context(), sessionstate.Session{
		ID: "session-replacement", WorkspaceID: "workspace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repositories.Threads.Create(t.Context(), threadstate.Thread{
		ID: "thread-replacement", SessionID: "session-replacement",
	})
	if err != nil {
		t.Fatal(err)
	}
	recovery, err := repositories.Lifecycle.Recover(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if recovery.LastSequence != 1 {
		t.Fatalf("last sequence = %d, want 1", recovery.LastSequence)
	}
	var usageContexts int
	if err := store.SQLite().DB().QueryRowContext(
		t.Context(),
		"SELECT COUNT(*) FROM usage_turn_context",
	).Scan(&usageContexts); err != nil {
		t.Fatal(err)
	}
	if usageContexts != 0 {
		t.Fatalf("deleted Session usage contexts = %d, want 0", usageContexts)
	}
}

func seedPersistentState(t *testing.T, root string) *state.Store {
	t.Helper()
	store, err := state.Open(t.Context(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	repositories, err := NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repositories.Sessions.CreateWorkspace(t.Context(), sessionstate.Workspace{
		ID: "workspace-1", RootPath: filepath.Join(root, "workspace"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repositories.Sessions.Create(t.Context(), sessionstate.Session{
		ID: "session-1", WorkspaceID: "workspace-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repositories.Threads.Create(t.Context(), threadstate.Thread{
		ID: "thread-1", SessionID: "session-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func persistentStartOperation(
	t *testing.T,
	turnID protocol.TurnID,
	itemID protocol.ItemID,
) protocol.Operation {
	t.Helper()
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread-1", TurnID: turnID, ItemID: itemID, Prompt: "persist me",
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func waitForTerminal(
	t *testing.T,
	events <-chan protocol.Event,
	operationID protocol.OperationID,
) {
	t.Helper()
	for {
		event := waitForEvent(t, events)
		if event.OperationID == operationID && protocol.IsTerminalEvent(event.Kind) {
			return
		}
	}
}

func waitForKind(
	t *testing.T,
	events <-chan protocol.Event,
	kind protocol.EventKind,
) {
	t.Helper()
	for {
		if event := waitForEvent(t, events); event.Kind == kind {
			return
		}
	}
}

func waitForEvent(t *testing.T, events <-chan protocol.Event) protocol.Event {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatal("event stream closed")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for persistent runtime event")
		return protocol.Event{}
	}
}

func closePersistentRuntime(t *testing.T, runtime *app.Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
