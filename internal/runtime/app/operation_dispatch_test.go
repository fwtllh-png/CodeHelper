package app

import (
	"errors"
	"testing"
)

func TestOperationOutcomeContract(t *testing.T) {
	problem := errors.New("rejected")
	tests := []struct {
		name    string
		outcome OperationOutcome
		valid   bool
	}{
		{"committed", OperationOutcome{Kind: OutcomeCommitted}, true},
		{"rejected", OperationOutcome{Kind: OutcomeRejected, Problem: problem}, true},
		{"async", OperationOutcome{Kind: OutcomeAsync}, true},
		{"terminal", OperationOutcome{Kind: OutcomeTerminal}, true},
		{"commit problem", OperationOutcome{Kind: OutcomeCommitted, Problem: problem}, false},
		{"reject without problem", OperationOutcome{Kind: OutcomeRejected}, false},
		{"async problem", OperationOutcome{Kind: OutcomeAsync, Problem: problem}, false},
		{"terminal problem", OperationOutcome{Kind: OutcomeTerminal, Problem: problem}, false},
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
