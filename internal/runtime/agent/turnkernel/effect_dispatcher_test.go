package turnkernel

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestC2RoutedEffectsExcludeLaterStageKinds(t *testing.T) {
	for _, kind := range []EffectKind{
		EffectExecuteTool,
		EffectAwaitApproval,
		EffectAwaitInput,
	} {
		if !C2RoutedEffect(kind) {
			t.Fatalf("C2 effect %q is not routed", kind)
		}
	}
	for _, kind := range []EffectKind{
		EffectSampleProvider,
		EffectRunVerification,
		EffectCommitJournal,
		EffectRollbackJournal,
	} {
		if C2RoutedEffect(kind) {
			t.Fatalf("later-stage effect %q migrated during C2", kind)
		}
	}
}

func TestC3RoutedEffectsOwnModelAndVerification(t *testing.T) {
	for _, kind := range []EffectKind{
		EffectSampleProvider,
		EffectRunVerification,
	} {
		if !C3RoutedEffect(kind) || !RoutedEffect(kind) {
			t.Fatalf("C3 effect %q is not routed", kind)
		}
	}
	for _, kind := range []EffectKind{
		EffectCommitJournal,
		EffectRollbackJournal,
	} {
		if C3RoutedEffect(kind) {
			t.Fatalf("C4 effect %q migrated during C3", kind)
		}
	}
}

func TestC4RoutedEffectsOwnJournal(t *testing.T) {
	for _, kind := range []EffectKind{
		EffectCommitJournal,
		EffectRollbackJournal,
	} {
		if !C4RoutedEffect(kind) || !RoutedEffect(kind) {
			t.Fatalf("C4 journal effect %q is not routed", kind)
		}
	}
}

func TestC6EveryEffectKindHasDurableRoute(t *testing.T) {
	for _, kind := range []EffectKind{
		EffectSampleProvider,
		EffectExecuteTool,
		EffectAwaitApproval,
		EffectAwaitInput,
		EffectRunVerification,
		EffectCommitJournal,
		EffectRollbackJournal,
	} {
		if !RoutedEffect(kind) {
			t.Fatalf("effect %q has no durable route", kind)
		}
	}
}

func TestModelResultCanDispatchNestedToolEffect(t *testing.T) {
	dispatcher := NewDurableEffectDispatcher()
	coordinator, err := NewTurnCoordinator(
		"turn-c3-model",
		NewState(protocol.TurnIntentAnswer, "act", 1),
		NewMemoryTerminalEnvelopeStore(nil, nil),
		dispatcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{
		StartTurn{},
		PreparationFinished{},
		ModelSampleRequested{SampleID: "sample-1"},
	} {
		if err := coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	sample, err := dispatcher.Start(EffectSampleProvider, "sample-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Resolve(ModelSampleResultReceived{
		EffectID: sample.ID,
		SampleID: "sample-1",
		Text:     "working",
		Calls: []ToolCallState{{
			ID: "call-1", Name: "write",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	state := coordinator.Snapshot()
	if state.Phase != PhaseExecutingTools ||
		state.SampleLedger["sample-1"].Status != SampleCompleted {
		t.Fatalf("model result state = %+v", state)
	}
	if _, started, err := dispatcher.Routed(
		EffectExecuteTool,
		"call-1",
	); err != nil || started {
		t.Fatalf(
			"nested tool effect started=%t err=%v",
			started,
			err,
		)
	}
}

func TestC2ToolEffectPersistsStartAndExactlyOneResult(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	dispatcher := NewDurableEffectDispatcher()
	coordinator, err := NewTurnCoordinator(
		"turn-c2-tool",
		NewState(protocol.TurnIntentAnswer, "act", 1),
		store,
		dispatcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{
		StartTurn{},
		PreparationFinished{},
		ToolCallsProposed{
			Calls: []ToolCallState{{ID: "call-1", Name: "read"}},
		},
	} {
		if err := coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	if pending := dispatcher.PendingRouted(EffectExecuteTool); len(pending) != 1 {
		t.Fatalf("routed tool effects = %+v", pending)
	}
	effect, err := dispatcher.Start(EffectExecuteTool, "call-1")
	if err != nil {
		t.Fatal(err)
	}
	if state := coordinator.Snapshot(); state.PendingEffects[effect.ID].Status != EffectRunning {
		t.Fatalf("started effect state = %+v", state.PendingEffects[effect.ID])
	}
	if err := dispatcher.Resolve(ToolResultReceived{
		EffectID: effect.ID,
		CallID:   "call-1",
	}); err != nil {
		t.Fatal(err)
	}
	state := coordinator.Snapshot()
	if len(state.OpenCalls) != 0 ||
		state.CompletedEffects[effect.ID].Status != EffectSucceeded {
		t.Fatalf("closed tool state = %+v", state)
	}
	facts, err := store.LoadDomainFacts(t.Context(), "turn-c2-tool")
	if err != nil {
		t.Fatal(err)
	}
	startedAt := firstFactCommand(facts, "effect_started")
	resultAt := firstFactCommand(facts, "tool_result_received")
	if startedAt < 0 || resultAt <= startedAt {
		t.Fatalf("tool fact order = %+v", facts)
	}
	if err := dispatcher.Resolve(ToolResultReceived{
		EffectID: effect.ID,
		CallID:   "call-1",
	}); err == nil {
		t.Fatal("duplicate tool result succeeded")
	}
}

func firstFactCommand(facts []DomainFact, command string) int {
	for index, fact := range facts {
		if fact.Command == command {
			return index
		}
	}
	return -1
}

func TestC2ResultSinkRetryDoesNotRestartEffect(t *testing.T) {
	dispatcher := NewDurableEffectDispatcher()
	effect := Effect{
		ID:             "effect-retry",
		Kind:           EffectExecuteTool,
		PayloadDigest:  "sha256:payload",
		IdempotencyKey: "tool:retry",
		Status:         EffectRequested,
		CallID:         "call-retry",
	}
	var starts atomic.Int64
	var results atomic.Int64
	fail := true
	if err := dispatcher.Dispatch(
		t.Context(),
		effect,
		func(command Command) error {
			switch command.(type) {
			case EffectStarted:
				starts.Add(1)
			case ToolResultReceived:
				results.Add(1)
				if fail {
					fail = false
					return errors.New("injected result sink failure")
				}
			}
			return nil
		},
	); err != nil {
		t.Fatal(err)
	}
	started, err := dispatcher.Start(EffectExecuteTool, "call-retry")
	if err != nil {
		t.Fatal(err)
	}
	result := ToolResultReceived{
		EffectID: started.ID,
		CallID:   "call-retry",
	}
	if err := dispatcher.Resolve(result); err == nil {
		t.Fatal("injected result sink failure was ignored")
	}
	if err := dispatcher.Resolve(result); err != nil {
		t.Fatal(err)
	}
	if starts.Load() != 1 || results.Load() != 2 {
		t.Fatalf(
			"submissions: starts=%d results=%d",
			starts.Load(),
			results.Load(),
		)
	}
}

func TestC2ConcurrentCancelAndToolResultCloseOnce(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	dispatcher := NewDurableEffectDispatcher()
	coordinator, err := NewTurnCoordinator(
		"turn-c2-race",
		NewState(protocol.TurnIntentAnswer, "act", 1),
		store,
		dispatcher,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{
		StartTurn{},
		PreparationFinished{},
		ToolCallsProposed{
			Calls: []ToolCallState{{ID: "call-race", Name: "read"}},
		},
	} {
		if err := coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	effect, err := dispatcher.Start(EffectExecuteTool, "call-race")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errs := make(chan error, 2)
	group.Go(func() {
		errs <- coordinator.Submit(t.Context(), CancelRequested{
			Reason: protocol.CancelReasonUserInterrupted,
		})
	})
	group.Go(func() {
		errs <- dispatcher.Resolve(ToolResultReceived{
			EffectID: effect.ID,
			CallID:   "call-race",
		})
	})
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	state := coordinator.Snapshot()
	if !state.Cancellation.Accepted ||
		len(state.OpenCalls) != 0 ||
		len(state.CompletedEffects) != 1 {
		t.Fatalf("cancel/result state = %+v", state)
	}
}
