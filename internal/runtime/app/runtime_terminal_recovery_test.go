package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestC5RuntimeRecoversTerminalOutboxWithoutDuplicateEvent(t *testing.T) {
	envelope := c5TerminalEnvelope(t)
	terminalStore := &c5AtomicTerminalStore{
		MemoryTerminalEnvelopeStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
	}
	if _, err := terminalStore.CommitTerminal(
		t.Context(),
		envelope,
	); err != nil {
		t.Fatal(err)
	}
	eventStore := NewMemoryEventStore(16)
	receipt := envelope.Outbox[0]
	receiptData, err := decodeTerminalOutboxEntry(receipt)
	if err != nil {
		t.Fatal(err)
	}
	event, err := protocol.NewEventWithIdentity(
		protocol.EventMeta{
			Sequence:    1,
			OperationID: receipt.OperationID,
			ThreadID:    receipt.ThreadID,
			TurnID:      receipt.TurnID,
			ItemID:      receipt.ItemID,
		},
		receipt.EventID,
		time.Now(),
		receiptData,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := eventStore.Append(t.Context(), event); err != nil {
		t.Fatal(err)
	}

	runtime, err := NewRuntimeWithRecovery(t.Context(), Options{
		EventStore:    eventStore,
		TerminalStore: terminalStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	events, err := eventStore.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 ||
		events[0].ID != envelope.Outbox[0].EventID ||
		events[1].ID != envelope.Outbox[1].EventID ||
		events[0].Kind != protocol.EventExecutionReceipt ||
		events[1].Kind != protocol.EventTurnCompleted {
		t.Fatalf("recovered events = %+v", events)
	}
	pending, err := terminalStore.PendingOutbox(
		t.Context(),
		envelope.TurnID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("pending terminal outbox = %+v", pending)
	}
}

func TestC5RuntimeRecoveryIsRepeatableAfterOutboxDrain(t *testing.T) {
	envelope := c5TerminalEnvelope(t)
	terminalStore := turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	if _, err := terminalStore.CommitTerminal(
		t.Context(),
		envelope,
	); err != nil {
		t.Fatal(err)
	}
	eventStore := NewMemoryEventStore(16)
	first, err := NewRuntimeWithRecovery(t.Context(), Options{
		EventStore:    eventStore,
		TerminalStore: terminalStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(context.Background()) })
	second, err := NewRuntimeWithRecovery(t.Context(), Options{
		EventStore:    eventStore,
		TerminalStore: terminalStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close(context.Background()) })
	events, err := eventStore.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events after repeated recovery = %+v", events)
	}
}

func TestC5ConcurrentRecoveryProjectsOneEventPerOutboxEntry(t *testing.T) {
	envelope := c5TerminalEnvelope(t)
	terminalStore := turnkernel.NewMemoryTerminalEnvelopeStore(nil, nil)
	if _, err := terminalStore.CommitTerminal(
		t.Context(),
		envelope,
	); err != nil {
		t.Fatal(err)
	}
	eventStore := NewMemoryEventStore(16)
	var wait sync.WaitGroup
	errs := make(chan error, 2)
	runtimes := make(chan *Runtime, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			runtime, err := NewRuntimeWithRecovery(t.Context(), Options{
				EventStore:    eventStore,
				TerminalStore: terminalStore,
			})
			if err == nil {
				runtimes <- runtime
			}
			errs <- err
		}()
	}
	wait.Wait()
	close(errs)
	close(runtimes)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for runtime := range runtimes {
		runtime := runtime
		t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	}
	events, err := eventStore.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("concurrent recovery events = %+v", events)
	}
}

type c5RecoveryLifecycle struct {
	recovery RecoveryState
}

func TestC6DurableRuntimeRejectsImplicitMemoryStores(t *testing.T) {
	if _, err := NewRuntimeWithRecovery(t.Context(), Options{
		Lifecycle: &c5RecoveryLifecycle{},
	}); err == nil {
		t.Fatal("durable runtime accepted implicit memory stores")
	}
}

type c5AtomicTerminalStore struct {
	*turnkernel.MemoryTerminalEnvelopeStore
}

func (s *c5AtomicTerminalStore) CommitTerminalOperation(
	ctx context.Context,
	envelope turnkernel.TerminalEnvelope,
) (turnkernel.TerminalCommitMarker, error) {
	return s.CommitTerminal(ctx, envelope)
}

func (l *c5RecoveryLifecycle) Recover(
	context.Context,
) (RecoveryState, error) {
	return l.recovery, nil
}

func (*c5RecoveryLifecycle) Accept(
	context.Context,
	protocol.Operation,
	string,
	json.RawMessage,
) (Acceptance, error) {
	return Acceptance{}, errors.New("unexpected accept during recovery")
}

func (*c5RecoveryLifecycle) Project(
	context.Context,
	protocol.Event,
) error {
	return nil
}

func (*c5RecoveryLifecycle) Commit(
	context.Context,
	CommitReceipt,
) error {
	return nil
}

type c5RecoveryEngine struct {
	testEngine
	starts    atomic.Int32
	approvals atomic.Int32
	inputs    atomic.Int32
}

func (e *c5RecoveryEngine) StartTurn(
	_ context.Context,
	payload *protocol.StartTurnPayload,
	sink EngineSink,
) error {
	e.starts.Add(1)
	return sink.Emit(&protocol.TurnCompletedData{Text: payload.Prompt})
}

func (e *c5RecoveryEngine) RestorePendingApproval(
	PendingApproval,
) error {
	e.approvals.Add(1)
	return nil
}

func (e *c5RecoveryEngine) RestorePendingInput(PendingInput) error {
	e.inputs.Add(1)
	return nil
}

func TestC5RuntimePrimesRecoveredApprovalAndInputWaits(t *testing.T) {
	engine := &c5RecoveryEngine{}
	runtime, err := NewRuntimeWithRecovery(t.Context(), Options{
		Engine:       engine,
		EventStore:   NewMemoryEventStore(16),
		ContentStore: NewMemoryContentStore(),
		TerminalStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
		Lifecycle: &c5RecoveryLifecycle{recovery: RecoveryState{
			PendingApprovals: map[string]PendingApproval{
				"approval-1": {
					RequestID: "approval-1",
					Data: protocol.ApprovalRequiredData{
						RequestID: "approval-1",
						CallID:    "call-1",
						Tool:      "write",
						ExpiresAt: time.Now().Add(time.Minute),
					},
				},
			},
			PendingInputs: map[string]PendingInput{
				"input-1": {
					RequestID: "input-1",
					Data: protocol.InputRequiredData{
						RequestID: "input-1",
						CallID:    "call-2",
						Tool:      "request_user_input",
						Prompt:    "continue?",
						ExpiresAt: time.Now().Add(time.Minute),
					},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	if engine.approvals.Load() != 1 || engine.inputs.Load() != 1 {
		t.Fatalf(
			"restored approvals=%d inputs=%d",
			engine.approvals.Load(),
			engine.inputs.Load(),
		)
	}
}

func TestC5RuntimeDispatchesAcceptedTurnWithDomainFacts(t *testing.T) {
	operation := startOperation(t, 71)
	canonical, err := CanonicalOperationPayload(operation)
	if err != nil {
		t.Fatal(err)
	}
	_, turnID, _ := protocol.OperationReferences(operation)
	state := turnkernel.NewState(protocol.TurnIntentAnswer, "act", 1)
	transition, err := (turnkernel.Reducer{}).Apply(
		state,
		turnkernel.StartTurn{},
	)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := turnkernel.Digest(transition.State)
	if err != nil {
		t.Fatal(err)
	}
	terminalStore := &c5AtomicTerminalStore{
		MemoryTerminalEnvelopeStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
	}
	if err := terminalStore.AppendDomainFacts(
		t.Context(),
		string(turnID),
		1,
		[]turnkernel.DomainFact{{
			TurnID:      string(turnID),
			Sequence:    1,
			Command:     "start_turn",
			State:       transition.State,
			StateDigest: digest,
		}},
	); err != nil {
		t.Fatal(err)
	}
	engine := &c5RecoveryEngine{}
	runtime, err := NewRuntimeWithRecovery(t.Context(), Options{
		Engine:        engine,
		EventStore:    NewMemoryEventStore(16),
		ContentStore:  NewMemoryContentStore(),
		TerminalStore: terminalStore,
		Lifecycle: &c5RecoveryLifecycle{recovery: RecoveryState{
			PendingOperations: map[protocol.OperationID]PendingOperation{
				operation.ID: {
					ID:        operation.ID,
					Canonical: canonical,
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	waitForCondition(t, func() bool {
		return engine.starts.Load() == 1 &&
			runtime.Snapshot(t.Context()).PendingOperations == 0
	})
}

func TestC5RuntimeResumesStartedModelEffectThroughAgentEngine(t *testing.T) {
	operation := startOperation(t, 72)
	canonical, err := CanonicalOperationPayload(operation)
	if err != nil {
		t.Fatal(err)
	}
	_, turnID, _ := protocol.OperationReferences(operation)
	terminalStore := &c5AtomicTerminalStore{
		MemoryTerminalEnvelopeStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
	}
	coordinators, err := turnkernel.NewStoreCoordinatorRuntime(terminalStore)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := coordinators.Open(
		t.Context(),
		string(turnID),
		turnkernel.NewStateWithPolicy(
			protocol.TurnIntentAnswer,
			string(policy.ModeAct),
			1,
			turnkernel.Policy{},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []turnkernel.Command{
		turnkernel.StartTurn{},
		turnkernel.PreparationFinished{},
		turnkernel.ModelSampleRequested{SampleID: "sample-recovered"},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	started, err := handle.Dispatcher.Start(
		turnkernel.EffectSampleProvider,
		"sample-recovered",
	)
	if err != nil {
		t.Fatal(err)
	}
	if started.Attempt != 1 {
		t.Fatalf("started attempt = %d", started.Attempt)
	}
	if err := coordinators.Release(t.Context(), string(turnID)); err != nil {
		t.Fatal(err)
	}
	worker, err := newTestAgentEngine(agentengine.Options{
		Provider: &singleAnswerProvider{},
		Route:    runtimeTestRoute(t),
		Tools:    tool.NewRegistry(nil, nil),
		Security: policy.DefaultRuntime(
			policy.ModeAct,
			policy.PermissionBypass,
		),
		Workspace:              t.TempDir(),
		Metrics:                telemetry.NewMetrics(),
		MaxOutputTokens:        128,
		ProfileRevision:        1,
		TurnCoordinatorRuntime: coordinators,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntimeWithRecovery(t.Context(), Options{
		Engine:        AdaptEngine(worker),
		EventStore:    NewMemoryEventStore(16),
		ContentStore:  NewMemoryContentStore(),
		TerminalStore: terminalStore,
		Lifecycle: &c5RecoveryLifecycle{recovery: RecoveryState{
			PendingOperations: map[protocol.OperationID]PendingOperation{
				operation.ID: {
					ID:        operation.ID,
					Canonical: canonical,
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	waitForCondition(t, func() bool {
		return runtime.Snapshot(t.Context()).PendingOperations == 0
	})
	facts, err := terminalStore.LoadDomainFacts(
		t.Context(),
		string(turnID),
	)
	if err != nil {
		t.Fatal(err)
	}
	var requeued bool
	var terminal bool
	for _, fact := range facts {
		requeued = requeued || fact.Command == "effect_requeued"
		terminal = terminal || fact.State.Phase.Terminal()
	}
	if !requeued || !terminal {
		t.Fatalf("recovered facts = %+v", facts)
	}
}

func TestC5RuntimeResumesStartedToolEffectThroughAgentEngine(t *testing.T) {
	operation := startOperation(t, 73)
	canonical, err := CanonicalOperationPayload(operation)
	if err != nil {
		t.Fatal(err)
	}
	_, turnID, _ := protocol.OperationReferences(operation)
	terminalStore := &c5AtomicTerminalStore{
		MemoryTerminalEnvelopeStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
	}
	coordinators, err := turnkernel.NewStoreCoordinatorRuntime(terminalStore)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := coordinators.Open(
		t.Context(),
		string(turnID),
		turnkernel.NewStateWithPolicy(
			protocol.TurnIntentAnswer,
			string(policy.ModeAct),
			1,
			turnkernel.Policy{},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []turnkernel.Command{
		turnkernel.StartTurn{},
		turnkernel.PreparationFinished{},
		turnkernel.ModelSampleRequested{SampleID: "sample-before-tool"},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	sample, err := handle.Dispatcher.Start(
		turnkernel.EffectSampleProvider,
		"sample-before-tool",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Dispatcher.Resolve(
		turnkernel.ModelSampleResultReceived{
			EffectID: sample.ID,
			SampleID: "sample-before-tool",
			Calls: []turnkernel.ToolCallState{{
				ID:        "call_write",
				Name:      "write",
				Arguments: `{"path":"out.txt"}`,
			}},
		},
	); err != nil {
		t.Fatal(err)
	}
	toolEffect, err := handle.Dispatcher.Start(
		turnkernel.EffectExecuteTool,
		"call_write",
	)
	if err != nil {
		t.Fatal(err)
	}
	if toolEffect.Attempt != 1 {
		t.Fatalf("tool attempt = %d", toolEffect.Attempt)
	}
	if err := coordinators.Release(t.Context(), string(turnID)); err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, nil)
	executor := &runtimeWriteTool{}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	model := &runtimeApprovalProvider{calls: 1}
	root := t.TempDir()
	worker, err := newTestAgentEngine(agentengine.Options{
		Provider: model,
		Route:    runtimeTestRoute(t),
		Tools:    registry,
		Security: policy.DefaultRuntime(
			policy.ModeAct,
			policy.PermissionBypass,
		),
		Workspace:              root,
		Journal:                newTestWorkspaceJournal(t, root),
		Metrics:                telemetry.NewMetrics(),
		MaxOutputTokens:        128,
		ProfileRevision:        1,
		TurnCoordinatorRuntime: coordinators,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntimeWithRecovery(t.Context(), Options{
		Engine:        AdaptEngine(worker),
		EventStore:    NewMemoryEventStore(16),
		ContentStore:  NewMemoryContentStore(),
		TerminalStore: terminalStore,
		Lifecycle: &c5RecoveryLifecycle{recovery: RecoveryState{
			PendingOperations: map[protocol.OperationID]PendingOperation{
				operation.ID: {
					ID:        operation.ID,
					Canonical: canonical,
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	waitForCondition(t, func() bool {
		return runtime.Snapshot(t.Context()).PendingOperations == 0
	})
	if executor.calls.Load() != 1 {
		t.Fatalf("recovered tool executions = %d", executor.calls.Load())
	}
	facts, err := terminalStore.LoadDomainFacts(
		t.Context(),
		string(turnID),
	)
	if err != nil {
		t.Fatal(err)
	}
	var requeued bool
	for _, fact := range facts {
		requeued = requeued || fact.Command == "effect_requeued"
	}
	if !requeued || !facts[len(facts)-1].State.Phase.Terminal() {
		t.Fatalf("recovered facts = %+v", facts)
	}
}

func TestC5RuntimeResumesCommittingTurnThroughJournalEffect(t *testing.T) {
	operation := startOperation(t, 74)
	canonical, err := CanonicalOperationPayload(operation)
	if err != nil {
		t.Fatal(err)
	}
	_, turnID, _ := protocol.OperationReferences(operation)
	terminalStore := &c5AtomicTerminalStore{
		MemoryTerminalEnvelopeStore: turnkernel.NewMemoryTerminalEnvelopeStore(
			nil,
			nil,
		),
	}
	coordinators, err := turnkernel.NewStoreCoordinatorRuntime(terminalStore)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := coordinators.Open(
		t.Context(),
		string(turnID),
		turnkernel.NewStateWithPolicy(
			protocol.TurnIntentAnswer,
			string(policy.ModeAct),
			1,
			turnkernel.Policy{},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []turnkernel.Command{
		turnkernel.StartTurn{},
		turnkernel.PreparationFinished{},
		turnkernel.ToolCallsProposed{Calls: []turnkernel.ToolCallState{{
			ID: "call-before-commit", Name: "write",
		}}},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	toolEffect, err := handle.Dispatcher.Start(
		turnkernel.EffectExecuteTool,
		"call-before-commit",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Dispatcher.Resolve(turnkernel.ToolResultReceived{
		EffectID: toolEffect.ID,
		CallID:   "call-before-commit",
		Changes: []turnkernel.ObservedChange{{
			Path: "out.txt", Kind: "created",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, command := range []turnkernel.Command{
		turnkernel.ModelTextReceived{Text: "done"},
		turnkernel.ReleaseProvisionalOutput{},
		turnkernel.TerminalRequested{},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	journalEffect, err := handle.Dispatcher.Start(
		turnkernel.EffectCommitJournal,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if journalEffect.Attempt != 1 {
		t.Fatalf("journal attempt = %d", journalEffect.Attempt)
	}
	if err := coordinators.Release(t.Context(), string(turnID)); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	worker, err := newTestAgentEngine(agentengine.Options{
		Provider: &singleAnswerProvider{},
		Route:    runtimeTestRoute(t),
		Tools:    tool.NewRegistry(nil, nil),
		Security: policy.DefaultRuntime(
			policy.ModeAct,
			policy.PermissionBypass,
		),
		Workspace:              root,
		Journal:                newTestWorkspaceJournal(t, root),
		Metrics:                telemetry.NewMetrics(),
		MaxOutputTokens:        128,
		ProfileRevision:        1,
		TurnCoordinatorRuntime: coordinators,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := NewRuntimeWithRecovery(t.Context(), Options{
		Engine:        AdaptEngine(worker),
		EventStore:    NewMemoryEventStore(16),
		ContentStore:  NewMemoryContentStore(),
		TerminalStore: terminalStore,
		Lifecycle: &c5RecoveryLifecycle{recovery: RecoveryState{
			PendingOperations: map[protocol.OperationID]PendingOperation{
				operation.ID: {
					ID:        operation.ID,
					Canonical: canonical,
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	waitForCondition(t, func() bool {
		return runtime.Snapshot(t.Context()).PendingOperations == 0
	})
	envelope, _, err := terminalStore.LoadTerminal(
		t.Context(),
		string(turnID),
	)
	if err != nil {
		events, _ := runtime.events.Replay(t.Context(), 0)
		var rejection any
		for _, event := range events {
			if data, ok := event.Data.(*protocol.OperationRejectedData); ok {
				rejection = data
			}
		}
		facts, _ := terminalStore.LoadDomainFacts(
			t.Context(),
			string(turnID),
		)
		t.Fatalf(
			"load terminal: %v rejection=%+v events=%+v facts=%+v",
			err,
			rejection,
			events,
			facts,
		)
	}
	completed := envelope.FrozenState.CompletedEffects[journalEffect.ID]
	if completed.Attempt != 2 ||
		envelope.FrozenState.Phase != turnkernel.PhaseCompleted {
		t.Fatalf("recovered terminal envelope = %+v", envelope)
	}
}

func c5TerminalEnvelope(t *testing.T) turnkernel.TerminalEnvelope {
	t.Helper()
	state := turnkernel.NewState(protocol.TurnIntentAnswer, "act", 1)
	apply := func(command turnkernel.Command) {
		transition, err := (turnkernel.Reducer{}).Apply(state, command)
		if err != nil {
			t.Fatalf("apply %T: %v", command, err)
		}
		state = transition.State
	}
	apply(turnkernel.StartTurn{})
	apply(turnkernel.PreparationFinished{})
	apply(turnkernel.ModelTextReceived{Text: "done"})
	apply(turnkernel.ReleaseProvisionalOutput{})
	apply(turnkernel.TerminalRequested{})
	apply(turnkernel.FinishTerminal{})
	digest, err := turnkernel.Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	receipt := &protocol.ExecutionReceiptData{
		Goal:    "answer",
		Intent:  protocol.TurnIntentAnswer,
		Outcome: protocol.TurnOutcomeAnswered,
	}
	receiptPayload, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	terminalPayload, err := json.Marshal(&protocol.TurnCompletedData{
		Text:    "done",
		Outcome: protocol.TurnOutcomeAnswered,
	})
	if err != nil {
		t.Fatal(err)
	}
	turnID := protocol.TurnID("turn-c5-recovery")
	operationID := protocol.OperationID("operation-c5-recovery")
	threadID := protocol.ThreadID("thread-c5-recovery")
	itemID := protocol.ItemID("item-c5-recovery")
	entry := func(
		id string,
		kind protocol.EventKind,
		payload json.RawMessage,
	) turnkernel.ProjectionOutboxEntry {
		return turnkernel.ProjectionOutboxEntry{
			ID:          id,
			EventID:     terminalOutboxEventID(turnID, id),
			OperationID: operationID,
			ThreadID:    threadID,
			TurnID:      turnID,
			ItemID:      itemID,
			Kind:        string(kind),
			Payload:     payload,
		}
	}
	decision := *state.Terminal
	return turnkernel.TerminalEnvelope{
		TurnID:      string(turnID),
		EffectID:    "terminal:" + string(turnID),
		FrozenState: state,
		DomainFacts: []turnkernel.DomainFact{{
			TurnID:      string(turnID),
			Sequence:    1,
			Command:     "finish_terminal",
			State:       state,
			StateDigest: digest,
		}},
		Receipt:     receipt,
		FinalOutput: append([]string(nil), state.FinalOutput...),
		TerminalEvent: turnkernel.Event{
			Kind:     turnkernel.EventTerminalCommitted,
			Terminal: &decision,
		},
		OperationCommit: turnkernel.OperationCommitFact{
			OperationID: operationID,
			Status:      "committed",
		},
		Outbox: []turnkernel.ProjectionOutboxEntry{
			entry(
				"receipt",
				protocol.EventExecutionReceipt,
				receiptPayload,
			),
			entry(
				"terminal",
				protocol.EventTurnCompleted,
				terminalPayload,
			),
		},
	}
}
