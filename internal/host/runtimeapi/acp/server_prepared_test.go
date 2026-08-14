package acp

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestPreparedStartTurnPreservesIntent(t *testing.T) {
	request := preparedStartTurn(
		"internal recovery context",
		"Continue: fix the parser",
		protocol.TurnIntentWorkspaceChange,
		"recover-source",
		&protocol.TurnRecoveryContext{
			Action:       protocol.TurnRecoveryContinue,
			SourceTurnID: "turn-source",
		},
	)
	if request.kind != protocol.OperationStartTurn {
		t.Fatalf("operation kind = %q", request.kind)
	}
	payload, ok := request.payload.(*protocol.StartTurnPayload)
	if !ok {
		t.Fatalf("payload type = %T", request.payload)
	}
	if payload.Prompt != "internal recovery context" ||
		payload.DisplayPrompt != "Continue: fix the parser" ||
		payload.Intent != protocol.TurnIntentWorkspaceChange ||
		payload.Recovery == nil ||
		payload.Recovery.SourceTurnID != "turn-source" ||
		request.idempotencyKey != "recover-source" {
		t.Fatalf("prepared request = %+v, payload = %+v", request, payload)
	}
}
