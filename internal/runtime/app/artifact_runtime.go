package app

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/persist/artifact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (r *Runtime) ReplayArtifactEvents(
	ctx context.Context,
	after protocol.Cursor,
) ([]protocol.Event, error) {
	return r.events.Replay(ctx, after)
}

func (r *Runtime) PublishArtifactEvent(
	operationID protocol.OperationID,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	itemID protocol.ItemID,
	data protocol.EventData,
) error {
	return r.EventService.publish(operationID, threadID, turnID, itemID, data)
}

func (r *Runtime) ArtifactStore() artifact.SessionArtifactStore {
	return r.sessionArtifacts
}

func (r *Runtime) CheckpointRuntime() any { return r.engine }
func (r *Runtime) Durable() bool          { return r.durable }

func (r *Runtime) ContextRebaseStore() artifact.ContextRebaseStore {
	return r.contextRebaseStore
}

func (r *Runtime) SessionPersistenceAvailable() bool {
	return r.sessionLifecycle != nil && r.profiles != nil
}

func (r *Runtime) SessionForThread(
	ctx context.Context,
	threadID protocol.ThreadID,
) (string, error) {
	return r.sessionLifecycle.SessionForThread(ctx, threadID)
}

func (r *Runtime) ActivateThread(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (protocol.SessionSummary, error) {
	return r.sessionLifecycle.ActivateThread(ctx, sessionID, threadID)
}

func (r *Runtime) StoredProfile(
	ctx context.Context,
	sessionID string,
	fallback protocol.SessionProfile,
) (protocol.SessionProfile, error) {
	return r.profiles.Profile(ctx, sessionID, fallback)
}

func (r *Runtime) DefaultProfile() protocol.SessionProfile {
	return r.defaultProfile
}

func (r *Runtime) ReportArtifactError(
	action string,
	event protocol.Event,
	err error,
) {
	if err == nil {
		return
	}
	r.metrics.Error()
	if r.logger == nil {
		return
	}
	r.logger.Error(
		action,
		"thread_id", event.ThreadID,
		"turn_id", event.TurnID,
		"sequence", event.Sequence,
		"error", err,
	)
}
