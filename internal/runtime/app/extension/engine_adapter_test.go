package extension

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestTurnImageAttachmentsPreserveValidatedModelInput(t *testing.T) {
	images := turnImageAttachments([]provider.Attachment{{
		Name: "lake.png", MediaType: "image/png", Data: []byte("image"),
	}})
	if len(images) != 1 ||
		images[0].Label != "lake.png" ||
		images[0].MediaType != "image/png" ||
		images[0].Content != "aW1hZ2U=" {
		t.Fatalf("turn images = %+v", images)
	}
}

func TestTurnPlanExecutionPreservesApprovedRecovery(t *testing.T) {
	payload := &protocol.StartTurnPayload{
		Recovery: &protocol.TurnRecoveryContext{
			Action: protocol.TurnRecoveryContinue, SourceTurnID: "turn-source",
			PlanID: "plan-source", PlanTransition: protocol.PlanTransitionImplement,
			ProfileRevision: 2,
		},
	}
	planID, transition, approved := turnPlanExecution(payload)
	if planID != "plan-source" ||
		transition != protocol.PlanTransitionImplement ||
		!approved {
		t.Fatalf(
			"recovery plan execution = (%q, %q, %t)",
			planID,
			transition,
			approved,
		)
	}
}
