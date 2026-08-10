package acp

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestPreparedStartTurnPreservesIntent(t *testing.T) {
	request := preparedStartTurn(
		"continue the source turn",
		protocol.TurnIntentWorkspaceChange,
		"recover-source",
	)
	if request.kind != protocol.OperationStartTurn {
		t.Fatalf("operation kind = %q", request.kind)
	}
	payload, ok := request.payload.(*protocol.StartTurnPayload)
	if !ok {
		t.Fatalf("payload type = %T", request.payload)
	}
	if payload.Prompt != "continue the source turn" ||
		payload.Intent != protocol.TurnIntentWorkspaceChange ||
		request.idempotencyKey != "recover-source" {
		t.Fatalf("prepared request = %+v, payload = %+v", request, payload)
	}
}
