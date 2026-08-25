package artifact

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/plandrift"
)

func (r *Service) PrepareStartPayload(
	ctx context.Context,
	workspace string,
	payload *protocol.StartTurnPayload,
) error {
	if payload == nil || payload.PlanExecution == nil {
		return nil
	}
	sessionID, err := r.SessionForThread(ctx, payload.ThreadID)
	if err != nil {
		return err
	}
	request := payload.PlanExecution
	var prepared PlanExecutionPreparation
	if request.SessionID == sessionID {
		prepared, err = r.PreparePlanExecution(
			ctx, sessionID, request.PlanID, request.Transition,
		)
	} else {
		prepared, err = r.PreparePlanExecutionTo(
			ctx, request.SessionID, sessionID,
			request.PlanID, request.Transition,
		)
	}
	if err != nil {
		return err
	}
	if err := plandrift.Verify(workspace, []byte(prepared.Artifact.Body)); err != nil {
		return err
	}
	payload.Prompt = prepared.Prompt
	payload.Intent = protocol.TurnIntentWorkspaceChange
	return nil
}
