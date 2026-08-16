package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type failFirstEffectAck struct {
	*orchestrationstore.Store
	failed bool
}

func (c *failFirstEffectAck) Execute(
	ctx context.Context,
	command kernel.Command,
) (kernel.Result, error) {
	if command.Kind == kernel.CommandPublishEffect && !c.failed {
		c.failed = true
		return kernel.Result{}, errors.New("injected effect ack failure")
	}
	return c.Store.Execute(ctx, command)
}

func TestRuntimeWorkGraphOperationsAndEvents(t *testing.T) {
	sqlite, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	workGraphs, err := orchestrationstore.Open(t.Context(), sqlite)
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{Orchestration: workGraphs})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	submit, err := protocol.NewOperation(&protocol.SubmitRunPayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "item",
		RunID: "run-runtime", Kind: string(model.RunKindWorkflow),
		Source: "host", SessionID: "session", RootThreadID: "thread",
		Nodes: []protocol.RunNodeSpec{{
			ID: "node", Kind: string(model.NodeKindAgentTurn),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), submit); err != nil {
		t.Fatal(err)
	}
	var started, ready, active bool
	deadline := time.After(5 * time.Second)
	for !started || !ready || !active {
		select {
		case event := <-events:
			switch data := event.Data.(type) {
			case *protocol.RunStartedData:
				started = data.Run.RunID == "run-runtime" && data.Revision == 1
			case *protocol.NodeStatusData:
				ready = data.Node.NodeID == "node" &&
					data.State == protocol.NodeStateReady &&
					data.Revision == 1
			case *protocol.RunStatusData:
				active = data.Run.RunID == "run-runtime" &&
					data.State == protocol.RunStateActive
			}
		case <-deadline:
			t.Fatalf(
				"submit events missing: started=%v ready=%v active=%v",
				started,
				ready,
				active,
			)
		}
	}
	for runtime.Snapshot(t.Context()).OperationsProcessed < 1 {
		time.Sleep(time.Millisecond)
	}
	beforeReplay := runtime.Snapshot(t.Context()).LastSequence
	runtime.dispatch(acceptedOperation{operation: submit})
	if afterReplay := runtime.Snapshot(t.Context()).LastSequence; afterReplay != beforeReplay {
		t.Fatalf(
			"stable WorkGraph replay advanced events from %d to %d",
			beforeReplay,
			afterReplay,
		)
	}
	skip, err := protocol.NewOperation(&protocol.SkipNodePayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "skip",
		RunID: "run-runtime", NodeID: "node",
		ExpectedRevision: 1, Reason: "not needed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), skip); err != nil {
		t.Fatal(err)
	}
	completed := false
	deadline = time.After(5 * time.Second)
	for !completed {
		select {
		case event := <-events:
			if data, ok := event.Data.(*protocol.RunCompletedData); ok {
				completed = data.Run.RunID == "run-runtime" &&
					data.Revision == 2
			}
		case <-deadline:
			t.Fatal("run.completed event missing")
		}
	}
	graph, err := workGraphs.Rebuild(t.Context(), "run-runtime")
	if err != nil {
		t.Fatal(err)
	}
	if graph.Run.State != protocol.RunStateCompleted ||
		graph.Run.Revision != 3 {
		t.Fatalf("stored graph = %+v", graph)
	}
	pending, err := workGraphs.PendingEffects(t.Context(), "run-runtime", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending terminal outbox = %+v", pending)
	}
}

func TestRuntimeWorkGraphEffectPublishIsStableAcrossAckFailure(t *testing.T) {
	sqlite, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlite.Close() })
	workGraphs, err := orchestrationstore.Open(t.Context(), sqlite)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(900, 0).UTC()
	submitted, err := workGraphs.Execute(t.Context(), kernel.Command{
		ID: "submit", Kind: kernel.CommandSubmit, RunID: "run-ack-window",
		At: now, Submit: &kernel.SubmitData{
			Kind: model.RunKindWorkflow, Source: "test",
			SessionID: "session", RootThreadID: "thread",
			Nodes: []model.NodeSpec{{
				ID: "node", Kind: model.NodeKindPhase,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workGraphs.Execute(t.Context(), kernel.Command{
		ID: "skip", Kind: kernel.CommandSkipNode, RunID: "run-ack-window",
		NodeID: "node", ExpectedRevision: submitted.Graph.Run.Revision,
		At: now.Add(time.Second), Reason: "done",
	}); err != nil {
		t.Fatal(err)
	}
	controller := &failFirstEffectAck{Store: workGraphs}
	runtime := NewRuntime(Options{Orchestration: controller})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	before := runtime.Snapshot(t.Context()).LastSequence
	if err := runtime.DrainWorkGraphEffects(t.Context()); err == nil {
		t.Fatal("first drain succeeded despite injected ack failure")
	}
	afterPublish := runtime.Snapshot(t.Context()).LastSequence
	if afterPublish != before+1 {
		t.Fatalf("published event sequence = %d, want %d", afterPublish, before+1)
	}
	if err := runtime.DrainWorkGraphEffects(t.Context()); err != nil {
		t.Fatal(err)
	}
	if afterRetry := runtime.Snapshot(t.Context()).LastSequence; afterRetry != afterPublish {
		t.Fatalf(
			"stable retry advanced event sequence from %d to %d",
			afterPublish,
			afterRetry,
		)
	}
	pending, err := workGraphs.PendingEffects(
		t.Context(),
		"run-ack-window",
		10,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("terminal outbox remains pending = %+v", pending)
	}
}
