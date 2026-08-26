package web

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestWebStartRejectsReservedExecutionContexts(t *testing.T) {
	for _, payload := range []*protocol.StartTurnPayload{
		{PlanExecution: &protocol.PlanTransitionRequest{}},
		{Recovery: &protocol.TurnRecoveryContext{}},
	} {
		if err := validateWebOperationPayload(payload); protocol.CodeOf(err) !=
			protocol.CodeInvalidArgument {
			t.Fatalf("reserved start payload error = %v", err)
		}
	}
	if err := validateWebOperationPayload(&protocol.StartTurnPayload{}); err != nil {
		t.Fatalf("ordinary start payload rejected: %v", err)
	}
}
