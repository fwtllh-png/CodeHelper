package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type startupTerminalLifecycle struct{}

func (startupTerminalLifecycle) Recover(context.Context) (RecoveryState, error) {
	return RecoveryState{}, nil
}

func (startupTerminalLifecycle) Accept(
	_ context.Context,
	operation protocol.Operation,
	_ string,
	_ json.RawMessage,
) (Acceptance, error) {
	return Acceptance{OperationID: operation.ID}, nil
}

func (startupTerminalLifecycle) Project(
	context.Context,
	protocol.Event,
) error {
	return nil
}

func (startupTerminalLifecycle) Commit(
	context.Context,
	CommitReceipt,
) error {
	return nil
}

type startupFailureEngine struct{ testEngine }

func (*startupFailureEngine) StartTurn(
	context.Context,
	*protocol.StartTurnPayload,
	EngineSink,
) error {
	return errors.New("engine construction failed")
}

type startupTerminalFactEngine struct {
	testEngine
	store turnkernel.TerminalEnvelopeStore
}

func (e *startupTerminalFactEngine) StartTurn(
	ctx context.Context,
	payload *protocol.StartTurnPayload,
	_ EngineSink,
) error {
	coordinator, err := turnkernel.NewTurnCoordinator(
		string(payload.TurnID),
		turnkernel.NewState(payload.Intent, "act", 1),
		e.store,
		turnkernel.NewDurableEffectDispatcher(),
	)
	if err != nil {
		return err
	}
	for _, command := range []turnkernel.Command{
		turnkernel.StartTurn{},
		turnkernel.PreparationFinished{},
		turnkernel.TerminalRequested{
			FailureCode: "conflict", FailureMessage: "journal start failed",
		},
		turnkernel.FinishTerminal{},
	} {
		if err := coordinator.Submit(ctx, command); err != nil {
			return err
		}
	}
	return errors.New("journal start failed")
}

type startupCancelEngine struct {
	testEngine
	started chan struct{}
}

func (e *startupCancelEngine) StartTurn(
	ctx context.Context,
	_ *protocol.StartTurnPayload,
	_ EngineSink,
) error {
	close(e.started)
	<-ctx.Done()
	return ctx.Err()
}

func (*startupCancelEngine) CancelTurn(
	context.Context,
	*protocol.CancelTurnPayload,
	EngineSink,
) error {
	return agentengine.ErrTurnCoordinatorNotActive
}

func newStartupTerminalRuntime(
	t *testing.T,
	engine Engine,
) (*Runtime, *c5AtomicTerminalStore) {
	t.Helper()
	store := &c5AtomicTerminalStore{
		MemoryTerminalEnvelopeStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
	}
	runtime, err := PrepareRuntimeWithRecovery(t.Context(), Options{
		Engine:        engine,
		EventStore:    NewMemoryEventStore(32),
		ContentStore:  NewMemoryContentStore(),
		TerminalStore: store,
		Lifecycle:     startupTerminalLifecycle{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	return runtime, store
}

func TestDurableStartupFailureCommitsTerminalEnvelope(t *testing.T) {
	runtime, store := newStartupTerminalRuntime(
		t,
		&startupFailureEngine{},
	)
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	operation := startOperation(t, 801)
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	if event := receiveEvent(t, events); event.Kind != protocol.EventExecutionReceipt {
		t.Fatalf("first event = %s", event.Kind)
	}
	terminal := receiveEvent(t, events)
	if terminal.Kind != protocol.EventTurnFailed {
		t.Fatalf("terminal = %s", terminal.Kind)
	}
	_, turnID, _ := protocol.OperationReferences(operation)
	envelope, _, err := store.LoadTerminal(t.Context(), string(turnID))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.FrozenState.Phase != turnkernel.PhaseFailed ||
		len(envelope.DomainFacts) != 4 ||
		runtime.Snapshot(t.Context()).ActiveTurns != 0 {
		t.Fatalf(
			"startup terminal phase=%s facts=%d active=%d",
			envelope.FrozenState.Phase,
			len(envelope.DomainFacts),
			runtime.Snapshot(t.Context()).ActiveTurns,
		)
	}
}

func TestDurableStartupFailurePublishesExistingTerminalFacts(t *testing.T) {
	store := &c5AtomicTerminalStore{
		MemoryTerminalEnvelopeStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
	}
	runtime, err := PrepareRuntimeWithRecovery(t.Context(), Options{
		Engine:        &startupTerminalFactEngine{store: store},
		EventStore:    NewMemoryEventStore(32),
		ContentStore:  NewMemoryContentStore(),
		TerminalStore: store,
		Lifecycle:     startupTerminalLifecycle{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	operation := startOperation(t, 803)
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	if event := receiveEvent(t, events); event.Kind != protocol.EventExecutionReceipt {
		t.Fatalf("first event = %s", event.Kind)
	}
	if terminal := receiveEvent(t, events); terminal.Kind != protocol.EventTurnFailed {
		t.Fatalf("terminal = %s", terminal.Kind)
	}
	_, turnID, _ := protocol.OperationReferences(operation)
	envelope, _, err := store.LoadTerminal(t.Context(), string(turnID))
	if err != nil {
		t.Fatal(err)
	}
	if envelope.FrozenState.Phase != turnkernel.PhaseFailed ||
		len(envelope.DomainFacts) < 4 ||
		runtime.Snapshot(t.Context()).ActiveTurns != 0 {
		t.Fatalf(
			"startup terminal phase=%s facts=%d active=%d",
			envelope.FrozenState.Phase,
			len(envelope.DomainFacts),
			runtime.Snapshot(t.Context()).ActiveTurns,
		)
	}
}

func TestCancelBeforeCoordinatorStartupCommitsCanceledTurn(t *testing.T) {
	engine := &startupCancelEngine{started: make(chan struct{})}
	runtime, _ := newStartupTerminalRuntime(t, engine)
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start := startOperation(t, 802)
	if err := runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	<-engine.started
	threadID, turnID, _ := protocol.OperationReferences(start)
	cancel, err := protocol.NewOperation(&protocol.CancelTurnPayload{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   "item-startup-cancel",
		Reason:   protocol.CancelReasonShutdown,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), cancel); err != nil {
		t.Fatal(err)
	}
	if event := receiveEvent(t, events); event.Kind != protocol.EventExecutionReceipt {
		t.Fatalf("first event = %s", event.Kind)
	}
	terminal := receiveEvent(t, events)
	if terminal.Kind != protocol.EventTurnCanceled {
		t.Fatalf("terminal = %s", terminal.Kind)
	}
	if terminal.OperationID != cancel.ID ||
		terminal.ItemID != "item-startup-cancel" {
		t.Fatalf(
			"cancel terminal operation=%s item=%s",
			terminal.OperationID,
			terminal.ItemID,
		)
	}
}
