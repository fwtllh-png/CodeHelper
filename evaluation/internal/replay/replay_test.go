package replay

import (
	"context"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
)

func TestReplayPreservesFailureAndACPBoundary(t *testing.T) {
	events := sealed(t, []evidence.Envelope{
		replayEvent(evidence.SourceACP, "acp.request.completed", 0),
		replayEvent(evidence.SourceACP, "acp.request.started", 1),
		replayEvent(evidence.SourceRuntime, "turn.failed", 2),
	})
	events[0].Identity.Request = "request-001"
	events[1].Identity.Request = "request-002"
	events[2].Identity.Turn = "turn-001"
	events = sealed(t, events)

	outcome, err := Execute(events)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.FailureSignature != "turn_failed" ||
		outcome.OrphanResponses != 1 ||
		outcome.IncompleteRequests != 1 {
		t.Fatalf("replay outcome = %+v", outcome)
	}
}

func TestReplayLevelCannotUpgradeWithoutExecutor(t *testing.T) {
	events := sealed(t, []evidence.Envelope{
		replayEvent(evidence.SourceRuntime, "turn.failed", 0),
	})
	if _, err := ExecuteAt(
		context.Background(),
		LevelRuntime,
		events,
		nil,
	); err == nil || !strings.Contains(err.Error(), "no production executor") {
		t.Fatalf("Runtime Replay upgrade error = %v", err)
	}
}

func TestRequiredProviderSplitFailsWithZeroEligibleEvents(t *testing.T) {
	events := sealed(t, []evidence.Envelope{
		replayEvent(evidence.SourceRuntime, "turn.failed", 0),
	})
	if _, err := VerifyMutationCoverage(
		events,
		[]MutationKind{MutationSplit},
	); err == nil || !strings.Contains(err.Error(), "zero eligible") {
		t.Fatalf("zero provider split error = %v", err)
	}
}

func TestCausalSliceRetainsAncestorClosure(t *testing.T) {
	events := []evidence.Envelope{
		replayEvent(evidence.SourceACP, "acp.request.started", 0),
		replayEvent(evidence.SourceRuntime, "turn.started", 1),
		replayEvent(evidence.SourceRuntime, "tool.result", 2),
		replayEvent(evidence.SourceRuntime, "turn.failed", 3),
	}
	events[1].Causality.ParentSequence = 1
	events[2].Causality.ParentSequence = 2
	events[3].Causality.ParentSequence = 3
	events = sealed(t, events)
	selected, err := CausalSlice(events, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 4 {
		t.Fatalf("causal slice = %+v", selected)
	}
}

func TestProductionReplayLevelsEnterRealBoundaries(t *testing.T) {
	providerEvents := sealed(t, []evidence.Envelope{
		replayEvent(evidence.SourceProvider, "provider.frame", 0),
		replayEvent(evidence.SourceRuntime, "turn.failed", 1),
	})
	executors := ProductionExecutors()
	for _, level := range []Level{LevelProvider, LevelRuntime, LevelHost} {
		t.Run(string(level), func(t *testing.T) {
			events := providerEvents
			if level != LevelProvider {
				events = sealed(t, []evidence.Envelope{
					replayEvent(evidence.SourceRuntime, "turn.failed", 0),
				})
			}
			outcome, err := ExecuteAt(t.Context(), level, events, executors)
			if err != nil {
				t.Fatal(err)
			}
			if outcome.Level != level {
				t.Fatalf("Replay level = %s, want %s", outcome.Level, level)
			}
		})
	}
}

func TestReplayMutationsAreDeterministicAndObservable(t *testing.T) {
	events := sealed(t, []evidence.Envelope{
		replayEvent(evidence.SourceProvider, "text_delta", 0),
		replayEvent(evidence.SourceRuntime, "turn.failed", 10),
	})
	mutations := []Mutation{
		{Kind: MutationSplit, Sequence: 1},
		{Kind: MutationDelay, Sequence: 1, DelayMS: 50},
		{Kind: MutationDuplicate, Sequence: 1},
		{Kind: MutationTruncate, Sequence: 2},
		{Kind: MutationInterrupt, Sequence: 2},
		{Kind: MutationUnknown, Sequence: 1},
	}
	for _, mutation := range mutations {
		t.Run(string(mutation.Kind), func(t *testing.T) {
			first, err := Mutate(events, mutation)
			if err != nil {
				t.Fatal(err)
			}
			second, err := Mutate(events, mutation)
			if err != nil {
				t.Fatal(err)
			}
			firstJSON, _ := evidence.EncodeJSONL(first)
			secondJSON, _ := evidence.EncodeJSONL(second)
			if string(firstJSON) != string(secondJSON) {
				t.Fatal("mutation is not deterministic")
			}
			outcome, err := Execute(first)
			if err != nil {
				t.Fatal(err)
			}
			if mutation.Kind == MutationDuplicate && outcome.DuplicateEvents == 0 {
				t.Fatal("duplicate mutation is not observable")
			}
			if mutation.Kind == MutationInterrupt && !outcome.Interrupted {
				t.Fatal("interrupt mutation is not observable")
			}
		})
	}
	malformed, err := Mutate(events, Mutation{
		Kind: MutationMalformed, Sequence: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if malformed[0].Kind != "provider.malformed_event" {
		t.Fatalf("malformed mutation = %+v", malformed)
	}
}

func TestReplayRejectsProviderSequenceRegression(t *testing.T) {
	first := replayEvent(evidence.SourceProvider, "text_delta", 0)
	first.Data = []byte(`{"wire_sequence":7}`)
	second := replayEvent(evidence.SourceProvider, "text_delta", 1)
	second.Data = []byte(`{"wire_sequence":6}`)
	events := sealed(t, []evidence.Envelope{first, second})
	if _, err := Execute(events); err == nil ||
		!strings.Contains(err.Error(), "wire sequence") {
		t.Fatalf("provider sequence error = %v", err)
	}
}

func replayEvent(
	source evidence.Source,
	kind string,
	offset int64,
) evidence.Envelope {
	return evidence.Envelope{
		OffsetMS: offset,
		Source:   source,
		Kind:     kind,
		Identity: evidence.Identity{Capture: "capture-001"},
		Policy: evidence.Policy{
			Class: evidence.DataOperational, Redaction: evidence.RedactionNotRequired,
		},
		Data: []byte(`{"metadata":true}`),
	}
}

func sealed(t *testing.T, events []evidence.Envelope) []evidence.Envelope {
	t.Helper()
	result, err := evidence.Seal(events)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
