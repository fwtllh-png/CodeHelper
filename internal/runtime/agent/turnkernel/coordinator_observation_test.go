package turnkernel

import (
	"context"
	"testing"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestDomainFactObserverRunsAfterDurableAppend(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	coordinator, err := NewTurnCoordinator(
		"turn-observed",
		NewState(protocol.TurnIntentAnswer, "act", 1),
		store,
		NewDurableEffectDispatcher(),
	)
	if err != nil {
		t.Fatal(err)
	}
	var observed []DomainFact
	coordinator.SetDomainFactObserver(func(
		ctx context.Context,
		facts []DomainFact,
	) {
		durable, loadErr := store.LoadDomainFacts(ctx, "turn-observed")
		if loadErr != nil {
			t.Error(loadErr)
			return
		}
		if len(durable) != len(facts) {
			t.Errorf("durable=%d observed=%d", len(durable), len(facts))
		}
		observed = append(observed, facts...)
	})
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0].Sequence != 1 ||
		observed[0].StateDigest == "" {
		t.Fatalf("observed = %+v", observed)
	}
}

func TestDomainFactObserverPanicDoesNotChangeTransition(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	coordinator, err := NewTurnCoordinator(
		"turn-observer-panic",
		NewState(protocol.TurnIntentAnswer, "act", 1),
		store,
		NewDurableEffectDispatcher(),
	)
	if err != nil {
		t.Fatal(err)
	}
	coordinator.SetDomainFactObserver(func(context.Context, []DomainFact) {
		panic("observation failure")
	})
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if coordinator.Snapshot().Phase != PhasePreparing {
		t.Fatalf("state = %+v", coordinator.Snapshot())
	}
	facts, err := store.LoadDomainFacts(t.Context(), "turn-observer-panic")
	if err != nil || len(facts) != 1 {
		t.Fatalf("facts=%+v error=%v", facts, err)
	}
}
