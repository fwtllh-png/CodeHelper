package turnkernel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

type recordingEffectExecutor struct {
	mu      sync.Mutex
	effects []Effect
	result  func(Effect) Command
}

func (e *recordingEffectExecutor) ExecuteEffect(
	_ context.Context,
	effect Effect,
) (Command, error) {
	e.mu.Lock()
	e.effects = append(e.effects, effect)
	e.mu.Unlock()
	return e.result(effect), nil
}

func (e *recordingEffectExecutor) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.effects)
}

func TestPhase4R3CoordinatorPersistsBeforeStateAndDispatch(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	executor := &recordingEffectExecutor{
		result: func(effect Effect) Command {
			return EffectResultReceived{EffectID: effect.ID, Success: true}
		},
	}
	coordinator := newTestCoordinator(t, store, executor)
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "tool"}},
	}); err != nil {
		t.Fatal(err)
	}
	state := coordinator.Snapshot()
	if executor.count() != 1 ||
		len(state.PendingEffects) != 0 ||
		len(state.CompletedEffects) != 1 {
		t.Fatalf("state=%+v effects=%d", state, executor.count())
	}
	facts, err := store.LoadDomainFacts(t.Context(), "turn-coordinator")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) == 0 ||
		facts[len(facts)-1].Command != "effect_result_received" {
		t.Fatalf("domain facts = %+v", facts)
	}
}

func TestPhase4R3PersistenceFailureCommitsNoStateOrEffect(t *testing.T) {
	fail := false
	store := &failingDomainFactStore{
		TerminalEnvelopeStore: NewMemoryTerminalEnvelopeStore(nil, nil),
		fail:                  &fail,
	}
	executor := &recordingEffectExecutor{
		result: func(effect Effect) Command {
			return EffectResultReceived{EffectID: effect.ID, Success: true}
		},
	}
	coordinator := newTestCoordinator(t, store, executor)
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}
	before, err := Digest(coordinator.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	err = coordinator.Submit(t.Context(), ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "tool"}},
	})
	if err == nil {
		t.Fatal("injected persistence failure was ignored")
	}
	if protocol.CodeOf(err) != protocol.CodeUnavailable ||
		protocol.DispositionOf(err) != protocol.FaultResumeTurn {
		t.Fatalf("persistence fault = %#v", protocol.ProblemOf(err))
	}
	after, digestErr := Digest(coordinator.Snapshot())
	if digestErr != nil {
		t.Fatal(digestErr)
	}
	if before != after || executor.count() != 0 {
		t.Fatalf(
			"failed transition changed state or dispatched: before=%s after=%s effects=%d",
			before,
			after,
			executor.count(),
		)
	}
}

func TestPhase4R3DispatcherRejectsMismatchedEffectResult(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	executor := &recordingEffectExecutor{
		result: func(Effect) Command {
			return EffectResultReceived{EffectID: "wrong", Success: true}
		},
	}
	coordinator := newTestCoordinator(t, store, executor)
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}
	err := coordinator.Submit(t.Context(), ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "tool"}},
	})
	if !errors.Is(err, ErrEffectResultIdentity) {
		t.Fatalf("dispatcher error = %v", err)
	}
	state := coordinator.Snapshot()
	if len(state.PendingEffects) != 1 ||
		len(state.CompletedEffects) != 0 {
		t.Fatalf("mismatched result changed effect state: %+v", state)
	}
}

func TestC6DurableDispatcherClosesStartedEffectByIdentity(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	dispatcher := NewDurableEffectDispatcher()
	coordinator, err := NewTurnCoordinator(
		"turn-deferred",
		NewState(protocol.TurnIntentAnswer, "act", 7),
		store,
		dispatcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "tool"}},
	}); err != nil {
		t.Fatal(err)
	}
	effect, err := dispatcher.Start(EffectExecuteTool, "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(dispatcher.PendingRouted("")) != 1 {
		t.Fatalf("pending effects = %+v", dispatcher.PendingRouted(""))
	}
	if err := dispatcher.Resolve(EffectResultReceived{
		EffectID: effect.ID,
		Success:  true,
	}); err != nil {
		t.Fatal(err)
	}
	state := coordinator.Snapshot()
	if len(dispatcher.PendingRouted("")) != 0 ||
		len(state.PendingEffects) != 0 ||
		state.CompletedEffects[effect.ID].Status != EffectSucceeded {
		t.Fatalf(
			"dispatcher=%+v state=%+v",
			dispatcher.PendingRouted(""),
			state,
		)
	}
	if err := dispatcher.Resolve(EffectResultReceived{
		EffectID: effect.ID,
		Success:  true,
	}); err == nil {
		t.Fatal("duplicate durable result was accepted")
	}
}

func TestPhase4R3CoordinatorSerializesConcurrentCommands(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	executor := &recordingEffectExecutor{
		result: func(effect Effect) Command {
			return EffectResultReceived{EffectID: effect.ID, Success: true}
		},
	}
	coordinator := newTestCoordinator(t, store, executor)
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			_ = coordinator.Submit(t.Context(), ModelTextReceived{Text: "x"})
		})
	}
	group.Wait()
	state := coordinator.Snapshot()
	if len(state.ProvisionalOutput) != 32 {
		t.Fatalf("serialized output count = %d", len(state.ProvisionalOutput))
	}
	facts, err := store.LoadDomainFacts(t.Context(), "turn-coordinator")
	if err != nil {
		t.Fatal(err)
	}
	for index, fact := range facts {
		if fact.Sequence != uint64(index+1) {
			t.Fatalf("fact[%d] sequence = %d", index, fact.Sequence)
		}
	}
}

func TestPhase4R6CoordinatorRestoresCurrentDomainFacts(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	dispatcher := NewDurableEffectDispatcher()
	coordinator := newDeferredTestCoordinator(t, store, dispatcher)
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "read"}},
	}); err != nil {
		t.Fatal(err)
	}
	before := coordinator.Snapshot()

	restoredDispatcher := NewDurableEffectDispatcher()
	restored, err := RestoreTurnCoordinator(
		t.Context(),
		"turn-test",
		store,
		restoredDispatcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	after := restored.Snapshot()
	beforeDigest, _ := Digest(before)
	afterDigest, _ := Digest(after)
	if beforeDigest != afterDigest {
		t.Fatalf("restored digest = %s, want %s", afterDigest, beforeDigest)
	}
	pending := restoredDispatcher.PendingRouted("")
	if len(pending) != 1 ||
		pending[0].Kind != EffectExecuteTool ||
		pending[0].CallID != "call-1" {
		t.Fatalf("restored effects = %+v", pending)
	}
}

func TestPhase4R6CoordinatorRestoreRejectsCorruptFacts(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	dispatcher := NewDurableEffectDispatcher()
	coordinator := newDeferredTestCoordinator(t, store, dispatcher)
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	store.facts["turn-test"][0].StateDigest = "sha256:corrupt"
	store.mu.Unlock()
	if _, err := RestoreTurnCoordinator(
		t.Context(),
		"turn-test",
		store,
		NewDurableEffectDispatcher(),
	); err == nil {
		t.Fatal("corrupt Domain Fact restored successfully")
	}
}

type failingDomainFactStore struct {
	TerminalEnvelopeStore
	fail *bool
}

func (s *failingDomainFactStore) AppendDomainFacts(
	ctx context.Context,
	turnID string,
	expectedNext uint64,
	facts []DomainFact,
) error {
	if *s.fail {
		return errors.New("injected domain fact append failure")
	}
	return s.TerminalEnvelopeStore.AppendDomainFacts(
		ctx,
		turnID,
		expectedNext,
		facts,
	)
}

func newTestCoordinator(
	t *testing.T,
	store DomainFactStore,
	executor EffectExecutor,
) *TurnCoordinator {
	t.Helper()
	coordinator, err := NewTurnCoordinator(
		"turn-coordinator",
		NewState(protocol.TurnIntentAnswer, "act", 7),
		store,
		SynchronousEffectDispatcher{
			Executors: map[EffectKind]EffectExecutor{
				EffectExecuteTool: executor,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func newDeferredTestCoordinator(
	t *testing.T,
	store DomainFactStore,
	dispatcher EffectDispatcher,
) *TurnCoordinator {
	t.Helper()
	coordinator, err := NewTurnCoordinator(
		"turn-test",
		NewState(protocol.TurnIntentAnswer, "act", 7),
		store,
		dispatcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}
