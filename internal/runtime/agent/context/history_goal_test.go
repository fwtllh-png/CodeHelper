package agentcontext

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
)

func TestActiveTurnGoalUnwrapsContinueEnvelope(t *testing.T) {
	envelope := "Continue the exact source Turn identified below. Do not infer the " +
		"task from an older conversation Turn.\n\n" +
		"Original user request:\n<source_request>\n" +
		"audit multi-paxos against the design doc\n</source_request>\n\n" +
		"Source Turn ID: turn_source\n" +
		"Previous attempt ended as failed (unavailable): provider rate " +
		"limit retry budget exhausted."
	user := provider.TextMessage(provider.RoleUser, envelope)
	user.Turn = 3
	goal := ActiveTurnGoal([]provider.Message{user})
	if goal != "audit multi-paxos against the design doc" {
		t.Fatalf("goal = %q", goal)
	}
	plain := provider.TextMessage(provider.RoleUser, "list the paxos modules")
	plain.Turn = 1
	if ActiveTurnGoal([]provider.Message{plain}) != "list the paxos modules" {
		t.Fatalf("plain goal = %q", ActiveTurnGoal([]provider.Message{plain}))
	}
}
