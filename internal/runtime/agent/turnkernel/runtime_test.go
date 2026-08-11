package turnkernel

import (
	"errors"
	"reflect"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestC1CoordinatorRuntimeRestoresAndDurablyRequeuesRunningEffect(
	t *testing.T,
) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	first, err := NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := first.Open(
		t.Context(),
		"turn-c1-runtime",
		NewState(protocol.TurnIntentAnswer, "act", 1),
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
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	effect, err := handle.Dispatcher.Start(EffectExecuteTool, "call-1")
	if err != nil {
		t.Fatal(err)
	}
	running := handle.Coordinator.Snapshot()
	runningDigest, err := Digest(running)
	if err != nil {
		t.Fatal(err)
	}
	if running.PendingEffects[effect.ID].Status != EffectRunning {
		t.Fatalf("running state = %+v", running.PendingEffects[effect.ID])
	}
	if err := first.Release(t.Context(), "turn-c1-runtime"); err != nil {
		t.Fatal(err)
	}

	second, err := NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := second.Restore(t.Context(), "turn-c1-runtime")
	if err != nil {
		t.Fatal(err)
	}
	state := restored.Coordinator.Snapshot()
	requeued := state.PendingEffects[effect.ID]
	if requeued.Status != EffectRequested || requeued.Attempt != 1 {
		t.Fatalf("requeued effect = %+v", requeued)
	}
	pending := restored.Dispatcher.PendingRouted(EffectExecuteTool)
	if len(pending) != 1 || pending[0].ID != effect.ID {
		t.Fatalf("restored pending effects = %+v", pending)
	}
	facts, err := store.LoadDomainFacts(t.Context(), "turn-c1-runtime")
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) < 2 ||
		facts[len(facts)-2].StateDigest != runningDigest ||
		facts[len(facts)-1].Command != "effect_requeued" {
		t.Fatalf("restored facts = %+v", facts)
	}
	restarted, err := restored.Dispatcher.Start(
		EffectExecuteTool,
		"call-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Attempt != 2 {
		t.Fatalf("restarted attempt = %d, want 2", restarted.Attempt)
	}
	if err := restored.Dispatcher.Resolve(ToolResultReceived{
		EffectID: restarted.ID,
		CallID:   "call-1",
	}); err != nil {
		t.Fatal(err)
	}
	closed := restored.Coordinator.Snapshot()
	if len(closed.PendingEffects) != 0 ||
		closed.CompletedEffects[effect.ID].Attempt != 2 {
		t.Fatalf("restored closure = %+v", closed)
	}
	if _, err := second.Restore(
		t.Context(),
		"turn-c1-runtime",
	); !errors.Is(err, ErrCoordinatorAlreadyActive) {
		t.Fatalf("duplicate restore error = %v", err)
	}
}

func TestC1CoordinatorRuntimeRejectsIncompleteRestore(t *testing.T) {
	runtime, err := NewStoreCoordinatorRuntime(
		NewMemoryTerminalEnvelopeStore(nil, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Restore(
		t.Context(),
		"turn-without-facts",
	); err == nil {
		t.Fatal("restore without facts succeeded")
	}
}

func TestC3CoordinatorRuntimeRequeuesRunningModelEffect(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	first, err := NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := first.Open(
		t.Context(),
		"turn-c3-model-restore",
		NewState(protocol.TurnIntentAnswer, "act", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{
		StartTurn{},
		PreparationFinished{},
		ModelSampleRequested{SampleID: "sample-restore"},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	effect, err := handle.Dispatcher.Start(
		EffectSampleProvider,
		"sample-restore",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Release(
		t.Context(),
		"turn-c3-model-restore",
	); err != nil {
		t.Fatal(err)
	}
	second, err := NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := second.Restore(
		t.Context(),
		"turn-c3-model-restore",
	)
	if err != nil {
		t.Fatal(err)
	}
	state := restored.Coordinator.Snapshot()
	requeued := state.PendingEffects[effect.ID]
	if requeued.Status != EffectRequested ||
		requeued.Attempt != effect.Attempt {
		t.Fatalf("requeued model effect = %+v", requeued)
	}
	if state.SampleLedger["sample-restore"].Status != SampleRequested ||
		state.ActiveSampleID != "" {
		t.Fatalf("restored sample state = %+v", state)
	}
}

func TestC5CoordinatorRestorePreservesDurableEffectPayload(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	first, err := NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := first.Open(
		t.Context(),
		"turn-c5-effect-payload",
		NewState(protocol.TurnIntentAnswer, "act", 1),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []Command{
		StartTurn{},
		PreparationFinished{},
		ToolCallsProposed{Calls: []ToolCallState{{
			ID:        "call-1",
			Name:      "write",
			Arguments: `{"path":"a.txt","content":"x"}`,
		}}},
	} {
		if err := handle.Coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	var expected Effect
	for _, effect := range handle.Coordinator.Snapshot().PendingEffects {
		if effect.Kind == EffectExecuteTool {
			expected = effect
		}
	}
	if len(expected.Payload) == 0 {
		t.Fatal("tool effect payload was not persisted")
	}
	if err := first.Release(
		t.Context(),
		"turn-c5-effect-payload",
	); err != nil {
		t.Fatal(err)
	}
	second, err := NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := second.Restore(
		t.Context(),
		"turn-c5-effect-payload",
	)
	if err != nil {
		t.Fatal(err)
	}
	var actual Effect
	for _, effect := range restored.Coordinator.Snapshot().PendingEffects {
		if effect.Kind == EffectExecuteTool {
			actual = effect
		}
	}
	if !reflect.DeepEqual(actual.Payload, expected.Payload) ||
		actual.PayloadDigest != expected.PayloadDigest {
		t.Fatalf("restored effect=%+v want=%+v", actual, expected)
	}
}
