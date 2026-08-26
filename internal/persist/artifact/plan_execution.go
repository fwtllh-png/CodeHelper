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
	if payload == nil {
		return nil
	}
	if payload.Recovery != nil && payload.Recovery.PlanID != "" {
		if err := r.validatePlanRecovery(ctx, payload); err != nil {
			return err
		}
	}
	if payload.PlanExecution == nil {
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

func (r *Service) validatePlanRecovery(
	ctx context.Context,
	payload *protocol.StartTurnPayload,
) error {
	sessionID, err := r.SessionForThread(ctx, payload.ThreadID)
	if err != nil {
		return err
	}
	profile, err := r.RestoreSessionProfile(ctx, sessionID, payload.ThreadID)
	if err != nil {
		return err
	}
	if profile.Profile.Revision != payload.Recovery.ProfileRevision {
		return revisionProblem(
			"Turn recovery Plan approval uses a stale Session Profile",
			payload.Recovery.PlanID,
			payload.Recovery.ProfileRevision,
			profile.Profile.Revision,
		)
	}
	events, err := r.ReplayArtifactEvents(ctx, 0)
	if err != nil {
		return err
	}
	for _, event := range events {
		if event.ThreadID != payload.ThreadID ||
			event.TurnID != payload.Recovery.SourceTurnID {
			continue
		}
		started, ok := event.Data.(*protocol.TurnStartedData)
		if ok &&
			started.PlanID == payload.Recovery.PlanID &&
			started.PlanTransition == payload.Recovery.PlanTransition &&
			started.ProfileRevision == payload.Recovery.ProfileRevision {
			return nil
		}
	}
	return runtimeProblem(
		protocol.CodeConflict,
		"Turn recovery Plan approval does not match the source Turn",
		nil,
	)
}
