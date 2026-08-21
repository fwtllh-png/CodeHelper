package turnkernel

import (
	"context"
	"errors"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestContextCompactionEffectsAreDurableAndIdentityBound(t *testing.T) {
	state := NewState(protocol.TurnIntentAnswer, "act", 1)
	state = apply(t, state, StartTurn{}).State
	state = apply(t, state, PreparationFinished{}).State
	requested := apply(t, state, ContextCompactionRequested{
		CompactionID: "compact-1",
		PlanDigest:   "sha256:plan",
	})
	if len(requested.Effects) != 1 ||
		requested.Effects[0].Kind != EffectGenerateNarrative ||
		requested.Effects[0].CallID != "compact-1" {
		t.Fatalf("narrative effects=%+v", requested.Effects)
	}
	effect := requested.Effects[0]
	state = apply(t, requested.State, EffectStarted{
		EffectID: effect.ID, Attempt: 1,
	}).State
	state = apply(t, state, EffectResultReceived{
		EffectID: effect.ID, Success: true,
	}).State
	rebase := apply(t, state, ContextRebaseRequested{
		CompactionID: "compact-1",
		PlanDigest:   "sha256:plan",
	})
	if len(rebase.Effects) != 1 ||
		rebase.Effects[0].Kind != EffectCommitContextRebase {
		t.Fatalf("rebase effects=%+v", rebase.Effects)
	}
}

func TestContextCompactionRequiresQuiescentBoundary(t *testing.T) {
	state := NewState(protocol.TurnIntentAnswer, "act", 1)
	state = apply(t, state, StartTurn{}).State
	state = apply(t, state, PreparationFinished{}).State
	state = apply(t, state, ModelSampleRequested{SampleID: "sample-1"}).State
	state = apply(t, state, ModelSampleStarted{
		SampleID: "sample-1", Attempt: 1,
	}).State
	if _, err := (Reducer{}).Apply(state, ContextCompactionRequested{
		CompactionID: "compact-1",
		PlanDigest:   "sha256:plan",
	}); err == nil {
		t.Fatal("active sample accepted context compaction")
	}
}

func TestContextRebaseEffectCanShareItsFactCommit(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	dispatcher := NewDurableEffectDispatcher()
	coordinator := newDeferredTestCoordinator(t, store, dispatcher)
	for _, command := range []Command{
		StartTurn{},
		PreparationFinished{},
		ContextRebaseRequested{
			CompactionID: "compact-1",
			PlanDigest:   "sha256:plan",
		},
	} {
		if err := coordinator.Submit(t.Context(), command); err != nil {
			t.Fatal(err)
		}
	}
	effect, err := dispatcher.Start(
		EffectCommitContextRebase,
		"compact-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	before := coordinator.Snapshot()
	factsBefore, err := store.LoadDomainFacts(
		t.Context(),
		"turn-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	result := EffectResultReceived{EffectID: effect.ID, Success: true}
	err = dispatcher.ResolveWith(result, func(command Command) error {
		return coordinator.SubmitWithCommit(
			t.Context(),
			command,
			func(context.Context, DomainFactBatch) error {
				return errors.New("injected context transaction failure")
			},
		)
	})
	if err == nil {
		t.Fatal("context transaction failure was ignored")
	}
	after := coordinator.Snapshot()
	factsAfter, loadErr := store.LoadDomainFacts(t.Context(), "turn-test")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if len(factsAfter) != len(factsBefore) ||
		len(after.PendingEffects) != len(before.PendingEffects) {
		t.Fatalf(
			"failed commit changed state: before=%+v after=%+v facts=%d/%d",
			before,
			after,
			len(factsBefore),
			len(factsAfter),
		)
	}
	err = dispatcher.ResolveWith(result, func(command Command) error {
		return coordinator.SubmitWithCommit(
			t.Context(),
			command,
			func(ctx context.Context, batch DomainFactBatch) error {
				return store.AppendDomainFacts(
					ctx,
					batch.TurnID,
					batch.ExpectedNext,
					batch.Facts,
				)
			},
		)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(coordinator.Snapshot().PendingEffects) != 0 {
		t.Fatal("successful shared commit left the rebase effect pending")
	}
}
