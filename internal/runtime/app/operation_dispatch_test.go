package app

import (
	"errors"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestOperationOutcomeContract(t *testing.T) {
	problem := protocol.ProblemOf(errors.New("rejected"))
	async := &AsyncTurn{ThreadID: "thread", TurnID: "turn", OperationID: "operation", ItemID: "item"}
	tests := []struct {
		name    string
		outcome OperationOutcome
		valid   bool
	}{
		{"committed", OperationOutcome{Kind: OutcomeCommitted, CommitMode: CommitNow}, true},
		{"committed events", OperationOutcome{
			Kind: OutcomeCommitted, CommitMode: CommitNow,
			Events: []protocol.EventData{&protocol.OutputDeltaData{Text: "ok"}},
		}, true},
		{"rejected", OperationOutcome{Kind: OutcomeRejected, Problem: problem, CommitMode: CommitNow}, true},
		{"async", OperationOutcome{Kind: OutcomeAsync, Async: async, CommitMode: CommitDeferred}, true},
		{"terminal", OperationOutcome{Kind: OutcomeTerminal, CommitMode: CommitDeferred}, true},
		{"commit problem", OperationOutcome{Kind: OutcomeCommitted, Problem: problem, CommitMode: CommitNow}, false},
		{"reject without problem", OperationOutcome{Kind: OutcomeRejected}, false},
		{"reject events", OperationOutcome{
			Kind: OutcomeRejected, Problem: problem, CommitMode: CommitNow,
			Events: []protocol.EventData{&protocol.OutputDeltaData{Text: "bad"}},
		}, false},
		{"async without turn", OperationOutcome{Kind: OutcomeAsync, CommitMode: CommitDeferred}, false},
		{"async problem", OperationOutcome{
			Kind: OutcomeAsync, Async: async, Problem: problem, CommitMode: CommitDeferred,
		}, false},
		{"terminal immediate", OperationOutcome{Kind: OutcomeTerminal, CommitMode: CommitNow}, false},
		{"unknown", OperationOutcome{Kind: "future"}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateOperationOutcome(test.outcome)
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, error = %v", test.valid, err)
			}
		})
	}
}
