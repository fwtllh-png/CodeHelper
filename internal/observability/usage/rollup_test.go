package usage

import (
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

// TestFormatCostKeepsFreeApartFromUnknown is the whole point of the formatter:
// a model with a known price of zero and a model with no price at all must not
// produce the same string, because that is exactly the confusion that lets an
// unknown cost be read as free.
func TestFormatCostKeepsFreeApartFromUnknown(t *testing.T) {
	for _, testCase := range []struct {
		name                         string
		microunits, priced, unpriced uint64
		want                         string
	}{
		{name: "known and free", priced: 2, want: "$0.00"},
		{name: "unpriced", unpriced: 2, want: "unknown"},
		{name: "nothing billed", want: "n/a"},
		{name: "sub cent", microunits: 500, priced: 1, want: "$0.0005"},
		{name: "cents", microunits: 12_300, priced: 3, want: "$0.0123"},
		{name: "whole dollars", microunits: 1_500_000, priced: 4, want: "$1.50"},
		{name: "dollars and fractions", microunits: 1_234_500, priced: 4, want: "$1.2345"},
		// A microunit is the stored unit, so no amount may be rounded away.
		{name: "single microunit", microunits: 1, priced: 1, want: "$0.000001"},
		{name: "smallest reported before rounding would bite",
			microunits: 150, priced: 1, want: "$0.00015"},
		{name: "large total", microunits: 12_345_678_900, priced: 900, want: "$12345.6789"},
		{name: "partly unpriced", microunits: 12_300, priced: 1, unpriced: 2,
			want: "$0.0123+ (2 calls unpriced)"},
		{name: "one unpriced call", microunits: 12_300, priced: 1, unpriced: 1,
			want: "$0.0123+ (1 call unpriced)"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			got := FormatCost(testCase.microunits, testCase.priced, testCase.unpriced)
			if got != testCase.want {
				t.Fatalf("FormatCost(%d, %d, %d) = %q want %q",
					testCase.microunits, testCase.priced, testCase.unpriced, got, testCase.want)
			}
		})
	}
	free := FormatCost(0, 1, 0)
	unknown := FormatCost(0, 0, 1)
	if free == unknown {
		t.Fatalf("free and unknown both render as %q", free)
	}
}

func TestFoldCountsTurnsAndCarriesTheUnpricedSplit(t *testing.T) {
	early := time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)
	late := early.Add(time.Hour)
	rollup := Fold(Scope{SessionID: "session-1"}, []Aggregate{
		{
			TurnID: "turn-1", InputTokens: 100, OutputTokens: 40, CachedTokens: 25,
			CostMicrounits: 150, PricedCalls: 1, Calls: 1, FirstAt: late, LastAt: late,
		},
		{
			TurnID: "turn-1", InputTokens: 10, OutputTokens: 5,
			UnpricedCalls: 1, Calls: 1, FirstAt: early, LastAt: early,
		},
		{
			TurnID: "turn-2", InputTokens: 90, OutputTokens: 15, ReasoningTokens: 5,
			CostMicrounits: 50, PricedCalls: 2, Calls: 2, FirstAt: early, LastAt: late,
		},
	})
	if rollup.Turns != 2 || rollup.Calls != 4 {
		t.Fatalf("rollup = %+v, want 2 turns over 4 calls", rollup)
	}
	if rollup.InputTokens != 200 || rollup.OutputTokens != 60 ||
		rollup.ReasoningTokens != 5 || rollup.CachedTokens != 25 {
		t.Fatalf("tokens = %+v", rollup)
	}
	if rollup.TotalTokens() != 260 {
		t.Fatalf("total tokens = %d, want input plus output only", rollup.TotalTokens())
	}
	if !rollup.First.Equal(early) || !rollup.Last.Equal(late) {
		t.Fatalf("window = %s..%s", rollup.First, rollup.Last)
	}
	if rollup.CostKnown() {
		t.Fatal("one unpriced call must make the whole rollup a floor")
	}
	if got := rollup.Cost(); got != "$0.0002+ (1 call unpriced)" { // 150 + 50 microunits
		t.Fatalf("cost = %q", got)
	}
	if got := rollup.CachedShare(); got != 0.125 {
		t.Fatalf("cached share = %v, want 25 of 200 input tokens", got)
	}
}

func TestFoldOfNothingIsEmptyRatherThanFree(t *testing.T) {
	rollup := Fold(Scope{ThreadID: "thread-1"}, nil)
	if !rollup.Empty() {
		t.Fatalf("rollup = %+v", rollup)
	}
	if rollup.CostKnown() {
		t.Fatal("a scope with no calls cannot have a known cost")
	}
	if got := rollup.Cost(); got != "n/a" {
		t.Fatalf("cost = %q, want it to refuse to name an amount", got)
	}
	if got := rollup.CachedShare(); got != 0 {
		t.Fatalf("cached share = %v", got)
	}
}

// TestQueryRollupAgreesWithTheRowsUnderIt is the reason the fold is in Go rather
// than a second SQL statement: the total a surface shows and the per-model rows
// it can show next to it come from the same read.
func TestQueryRollupAgreesWithTheRowsUnderIt(t *testing.T) {
	repository := testRepository(t)
	addTurn(t, repository, "turn-2", 1)
	start := testEvent(t, 1, &protocol.TurnStartedData{Provider: "provider", Model: "model"})
	if err := repository.Project(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	secondStart := testEventForTurn(t, 2, "turn-2",
		&protocol.TurnStartedData{Provider: "provider", Model: "other"})
	if err := repository.Project(t.Context(), secondStart); err != nil {
		t.Fatal(err)
	}
	events := []protocol.Event{
		testEvent(t, 3, &protocol.UsageData{
			Sample: 1, InputTokens: 100, OutputTokens: 40, CachedTokens: 20,
			CostMicrounits: 150, CostKnown: true,
		}),
		testEventForTurn(t, 4, "turn-2", &protocol.UsageData{
			Sample: 1, InputTokens: 60, OutputTokens: 10,
		}),
	}
	for _, event := range events {
		if err := repository.Project(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	rollup, err := repository.QueryRollup(t.Context(), Query{SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	if rollup.Turns != 2 || rollup.Calls != 2 {
		t.Fatalf("rollup = %+v, want one call in each of two turns", rollup)
	}
	if rollup.InputTokens != 160 || rollup.OutputTokens != 50 || rollup.CachedTokens != 20 {
		t.Fatalf("rollup tokens = %+v", rollup)
	}
	if rollup.CostMicrounits != 150 || rollup.PricedCalls != 1 || rollup.UnpricedCalls != 1 {
		t.Fatalf("rollup cost = %+v", rollup)
	}
	if rollup.CostKnown() {
		t.Fatal("the unpriced call must keep the session cost a floor")
	}
	aggregates, err := repository.QueryAggregates(t.Context(), Query{SessionID: "session-1"})
	if err != nil {
		t.Fatal(err)
	}
	var rows Rollup
	for _, aggregate := range aggregates {
		rows.InputTokens += aggregate.InputTokens
		rows.CostMicrounits += aggregate.CostMicrounits
	}
	if rows.InputTokens != rollup.InputTokens || rows.CostMicrounits != rollup.CostMicrounits {
		t.Fatalf("rollup %+v disagrees with its rows %+v", rollup, rows)
	}
	// Narrowing to one turn must narrow the numbers with it.
	single, err := repository.QueryRollup(t.Context(), Query{TurnID: "turn-2"})
	if err != nil {
		t.Fatal(err)
	}
	if single.Turns != 1 || single.InputTokens != 60 || single.Cost() != "unknown" {
		t.Fatalf("turn rollup = %+v cost = %q", single, single.Cost())
	}
}

// addTurn appends a finished turn to the fixture thread. It has to be finished
// because the schema allows a thread only one active turn at a time.
func addTurn(t *testing.T, repository *Repository, id string, ordinal int) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := repository.db.ExecContext(t.Context(), `
		INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		VALUES (?, 'thread-1', ?, 'completed', ?, ?)`, id, ordinal, now, now,
	); err != nil {
		t.Fatal(err)
	}
}

func testEventForTurn(
	t *testing.T,
	sequence protocol.Cursor,
	turnID protocol.TurnID,
	data protocol.EventData,
) protocol.Event {
	t.Helper()
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: sequence, OperationID: "operation-1",
		ThreadID: "thread-1", TurnID: turnID, ItemID: "item-1",
	}, data)
	if err != nil {
		t.Fatal(err)
	}
	return event
}
