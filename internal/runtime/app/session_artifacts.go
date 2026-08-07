package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type PlanTransitionPreparation struct {
	Artifact       protocol.SessionPlanArtifact
	ProfileUpdate  protocol.SessionProfileUpdateResult
	Prompt         string
	IdempotencyKey string
}

func (r *Runtime) Checkpoints(
	ctx context.Context,
	sessionID string,
	limit int,
) (protocol.CheckpointList, error) {
	if r.sessionArtifacts == nil {
		return protocol.CheckpointList{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"Session Checkpoints are unavailable",
			false,
			nil,
		)
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return protocol.CheckpointList{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Checkpoint limit exceeds 1000",
			false,
			nil,
		)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.CheckpointList{}, err
	}
	values, err := r.sessionArtifacts.ListCheckpoints(ctx, sessionID, limit)
	if err != nil {
		return protocol.CheckpointList{}, err
	}
	profile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return protocol.CheckpointList{}, err
	}
	quiescent := ensureSessionQuiescent(current, "restore") == nil
	for index := range values {
		compatible := values[index].ProfileRevision == profile.Profile.Revision
		values[index].CanRestore = compatible && quiescent
		values[index].CanFork = compatible && quiescent
	}
	result := protocol.CheckpointList{
		Version:     protocol.CheckpointProtocolVersion,
		SessionID:   sessionID,
		Checkpoints: values,
	}
	if err := result.Validate(); err != nil {
		return protocol.CheckpointList{}, err
	}
	return result, nil
}

func (r *Runtime) Checkpoint(
	ctx context.Context,
	sessionID, checkpointID string,
) (protocol.SessionCheckpoint, error) {
	if r.sessionArtifacts == nil {
		return protocol.SessionCheckpoint{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"Session Checkpoints are unavailable",
			false,
			nil,
		)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.SessionCheckpoint{}, err
	}
	checkpoint, _, _, err := r.sessionArtifacts.GetCheckpoint(
		ctx,
		checkpointID,
	)
	if err != nil {
		return protocol.SessionCheckpoint{}, err
	}
	if checkpoint.SessionID != sessionID {
		return protocol.SessionCheckpoint{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Checkpoint does not belong to the Session",
			false,
			nil,
		)
	}
	profile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return protocol.SessionCheckpoint{}, err
	}
	compatible := profile.Profile.Revision == checkpoint.ProfileRevision &&
		ensureSessionQuiescent(current, "restore") == nil
	checkpoint.CanRestore = compatible
	checkpoint.CanFork = compatible
	return checkpoint, nil
}

func (r *Runtime) RestoreCheckpoint(
	ctx context.Context,
	sessionID, checkpointID string,
) (protocol.CheckpointRestoreResult, error) {
	current, checkpoint, history, err := r.checkpointState(
		ctx,
		sessionID,
		checkpointID,
		"restore",
	)
	if err != nil {
		return protocol.CheckpointRestoreResult{}, err
	}
	manager, ok := r.engine.(CheckpointEngine)
	if !ok {
		return protocol.CheckpointRestoreResult{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"Checkpoint restore is unsupported by this engine",
			false,
			nil,
		)
	}
	decoded, err := DecodeCompactedHistory(history)
	if err != nil {
		return protocol.CheckpointRestoreResult{}, err
	}
	previous, err := manager.History(current.ThreadID)
	if err != nil {
		return protocol.CheckpointRestoreResult{}, err
	}
	if err := manager.RestoreCheckpoint(current.ThreadID, decoded); err != nil {
		return protocol.CheckpointRestoreResult{}, err
	}
	operationID := protocol.OperationID(stableArtifactID(
		"op",
		sessionID,
		checkpointID,
		"restore",
	))
	itemID := protocol.ItemID(stableArtifactID(
		"item",
		sessionID,
		checkpointID,
		"restore",
	))
	publishErr := r.publish(
		operationID,
		current.ThreadID,
		checkpoint.TurnID,
		itemID,
		&protocol.CheckpointRestoredData{
			CheckpointID:   checkpoint.ID,
			SourceThreadID: checkpoint.ThreadID,
			SourceTurnID:   checkpoint.TurnID,
			SourceCursor:   checkpoint.Cursor,
			ReplacementHistory: append(
				[]protocol.CompactedMessage(nil),
				history...,
			),
			SideEffectsReplayed: false,
		},
	)
	if publishErr != nil {
		_ = manager.RestoreCheckpoint(current.ThreadID, previous)
		return protocol.CheckpointRestoreResult{}, publishErr
	}
	return protocol.CheckpointRestoreResult{
		Version:             protocol.CheckpointProtocolVersion,
		Checkpoint:          checkpoint,
		ThreadID:            current.ThreadID,
		RestoredCursor:      checkpoint.Cursor,
		SideEffectsReplayed: false,
	}, nil
}

func (r *Runtime) ForkCheckpoint(
	ctx context.Context,
	sessionID, checkpointID, title string,
) (protocol.CheckpointForkResult, error) {
	_, checkpoint, history, err := r.checkpointState(
		ctx,
		sessionID,
		checkpointID,
		"fork",
	)
	if err != nil {
		return protocol.CheckpointForkResult{}, err
	}
	manager, ok := r.engine.(CheckpointEngine)
	if !ok {
		return protocol.CheckpointForkResult{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"Checkpoint Fork is unsupported by this engine",
			false,
			nil,
		)
	}
	decoded, err := DecodeCompactedHistory(history)
	if err != nil {
		return protocol.CheckpointForkResult{}, err
	}
	newThreadID, err := protocol.NewThreadID()
	if err != nil {
		return protocol.CheckpointForkResult{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = "Checkpoint Fork"
	}
	title = boundedArtifactText(title, 256)
	if err := manager.ForkCheckpoint(
		checkpoint.ThreadID,
		newThreadID,
		decoded,
	); err != nil {
		return protocol.CheckpointForkResult{}, err
	}
	operationID := protocol.OperationID(stableArtifactID(
		"op",
		sessionID,
		checkpointID,
		string(newThreadID),
	))
	itemID := protocol.ItemID(stableArtifactID(
		"item",
		sessionID,
		checkpointID,
		string(newThreadID),
	))
	if err := r.publish(
		operationID,
		checkpoint.ThreadID,
		checkpoint.TurnID,
		itemID,
		&protocol.CheckpointForkedData{
			CheckpointID: checkpoint.ID,
			NewThreadID:  newThreadID,
			Title:        title,
			SourceCursor: checkpoint.Cursor,
			ReplacementHistory: append(
				[]protocol.CompactedMessage(nil),
				history...,
			),
		},
	); err != nil {
		manager.Release(newThreadID)
		return protocol.CheckpointForkResult{}, err
	}
	if _, err := r.sessionLifecycle.ActivateThread(
		ctx,
		sessionID,
		newThreadID,
	); err != nil {
		return protocol.CheckpointForkResult{}, err
	}
	return protocol.CheckpointForkResult{
		Version:    protocol.CheckpointProtocolVersion,
		Checkpoint: checkpoint,
		SessionID:  sessionID,
		ThreadID:   newThreadID,
		ParentID:   checkpoint.ThreadID,
	}, nil
}

func (r *Runtime) SessionPlan(
	ctx context.Context,
	sessionID string,
) (protocol.SessionPlanSnapshot, error) {
	if r.sessionArtifacts == nil {
		return protocol.SessionPlanSnapshot{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"Session Plan Artifacts are unavailable",
			false,
			nil,
		)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.SessionPlanSnapshot{}, err
	}
	artifact, found, err := r.sessionArtifacts.LatestPlan(
		ctx,
		sessionID,
		current.ThreadID,
	)
	if err != nil {
		return protocol.SessionPlanSnapshot{}, err
	}
	if !found {
		return protocol.SessionPlanSnapshot{
			Version: protocol.CheckpointProtocolVersion,
		}, nil
	}
	profile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return protocol.SessionPlanSnapshot{}, err
	}
	compatible := profile.Profile.Revision == artifact.ProfileRevision &&
		ensureSessionQuiescent(current, "implement Plan") == nil
	artifact.CanImplement = artifact.CanImplement && compatible
	artifact.CanAutopilot = artifact.CanAutopilot && compatible
	return protocol.SessionPlanSnapshot{
		Version:  protocol.CheckpointProtocolVersion,
		Artifact: &artifact,
	}, nil
}

func (r *Runtime) PreparePlanTransition(
	ctx context.Context,
	sessionID, planID string,
	transition protocol.PlanTransition,
) (PlanTransitionPreparation, error) {
	if r.sessionArtifacts == nil {
		return PlanTransitionPreparation{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			"Session Plan Artifacts are unavailable",
			false,
			nil,
		)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if err := ensureSessionQuiescent(current, "implement Plan"); err != nil {
		return PlanTransitionPreparation{}, err
	}
	artifact, err := r.sessionArtifacts.GetPlan(ctx, planID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if artifact.SessionID != sessionID ||
		artifact.ThreadID != current.ThreadID {
		return PlanTransitionPreparation{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Plan Artifact does not belong to the active Session Thread",
			false,
			nil,
		)
	}
	profile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if profile.Profile.Revision != artifact.ProfileRevision {
		return PlanTransitionPreparation{}, protocol.NewProblem(
			protocol.CodeConflict,
			"Plan Artifact Profile Revision is stale",
			true,
			nil,
		)
	}
	mode := "act"
	patch := protocol.SessionProfilePatch{Mode: &mode}
	switch transition {
	case protocol.PlanTransitionImplement:
		if !artifact.CanImplement {
			return PlanTransitionPreparation{}, protocol.NewProblem(
				protocol.CodeConflict,
				"Plan Artifact cannot start implementation",
				false,
				nil,
			)
		}
	case protocol.PlanTransitionAutopilot:
		if !artifact.CanAutopilot {
			return PlanTransitionPreparation{}, protocol.NewProblem(
				protocol.CodeConflict,
				"Plan Artifact cannot start Autopilot",
				false,
				nil,
			)
		}
		posture := "auto"
		patch.ApprovalPosture = &posture
	default:
		return PlanTransitionPreparation{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Plan transition is invalid",
			false,
			nil,
		)
	}
	updated, err := r.UpdateSessionProfile(
		ctx,
		sessionID,
		current.ThreadID,
		profile.Profile.Revision,
		patch,
	)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	prompt := "Implement the approved structured Plan below. " +
		"Do not repeat completed external side effects; inspect current " +
		"workspace state before each consequential action.\n\n" +
		artifact.Body
	return PlanTransitionPreparation{
		Artifact:      artifact,
		ProfileUpdate: updated,
		Prompt:        prompt,
		IdempotencyKey: fmt.Sprintf(
			"plan:%s:%s",
			artifact.ID,
			transition,
		),
	}, nil
}

func (r *Runtime) checkpointState(
	ctx context.Context,
	sessionID, checkpointID, action string,
) (
	protocol.SessionSummary,
	protocol.SessionCheckpoint,
	[]protocol.CompactedMessage,
	error,
) {
	if r.sessionArtifacts == nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil,
			protocol.NewProblem(
				protocol.CodeUnavailable,
				"Session Checkpoints are unavailable",
				false,
				nil,
			)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, err
	}
	if err := ensureSessionQuiescent(current, action); err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, err
	}
	checkpoint, history, checkpointProfile, err :=
		r.sessionArtifacts.GetCheckpoint(ctx, checkpointID)
	if err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, err
	}
	if checkpoint.SessionID != sessionID {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil,
			protocol.NewProblem(
				protocol.CodeInvalidArgument,
				"Checkpoint does not belong to the Session",
				false,
				nil,
			)
	}
	currentProfile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, err
	}
	if currentProfile.Profile.Revision != checkpoint.ProfileRevision ||
		checkpointProfile.Revision != checkpoint.ProfileRevision {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil,
			protocol.NewProblem(
				protocol.CodeConflict,
				"Checkpoint Profile Revision is stale",
				true,
				nil,
			)
	}
	return current, checkpoint, history, nil
}

func stableArtifactID(prefix string, values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:])
}

func boundedArtifactText(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (r *Runtime) decoratePlanArtifact(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	data *protocol.PlanDeltaData,
) error {
	if r.sessionArtifacts == nil || r.sessionLifecycle == nil ||
		r.profiles == nil || data == nil || !data.Done ||
		strings.TrimSpace(data.Body) == "" {
		return nil
	}
	sessionID, err := r.sessionLifecycle.SessionForThread(ctx, threadID)
	if err != nil {
		return err
	}
	profile, err := r.profiles.Profile(ctx, sessionID, r.defaultProfile)
	if err != nil {
		return err
	}
	data.ArtifactID = stableArtifactID(
		"plan",
		sessionID,
		string(threadID),
		string(turnID),
		data.Body,
	)
	data.ProfileRevision = profile.Revision
	data.Status = string(protocol.PlanArtifactReady)
	data.CanImplement = true
	data.CanAutopilot = true
	return nil
}

func (r *Runtime) persistSessionArtifact(
	ctx context.Context,
	event protocol.Event,
) {
	if r.sessionArtifacts == nil || r.sessionLifecycle == nil ||
		r.profiles == nil {
		return
	}
	switch data := event.Data.(type) {
	case *protocol.PlanDeltaData:
		if data.ArtifactID == "" {
			return
		}
		sessionID, err := r.sessionLifecycle.SessionForThread(
			ctx,
			event.ThreadID,
		)
		if err != nil {
			r.logArtifactError("resolve Plan Session", event, err)
			return
		}
		_, err = r.sessionArtifacts.SavePlan(ctx, protocol.SessionPlanArtifact{
			Version:         protocol.CheckpointProtocolVersion,
			ID:              data.ArtifactID,
			SessionID:       sessionID,
			ThreadID:        event.ThreadID,
			TurnID:          event.TurnID,
			Cursor:          event.Sequence,
			Status:          protocol.PlanArtifactReady,
			Body:            data.Body,
			ProfileRevision: data.ProfileRevision,
			CanImplement:    data.CanImplement,
			CanAutopilot:    data.CanAutopilot,
			CreatedAt:       event.CreatedAt,
		})
		if err != nil {
			r.logArtifactError("save Plan Artifact", event, err)
		}
	case *protocol.TurnCompletedData:
		r.persistTerminalCheckpoint(
			ctx,
			event,
			protocol.CheckpointCompleted,
			data.Text,
		)
	case *protocol.TurnCanceledData:
		if protocol.NormalizeCancelReason(data.Reason) !=
			protocol.CancelReasonUserInterrupted {
			return
		}
		r.persistTerminalCheckpoint(
			ctx,
			event,
			protocol.CheckpointInterrupted,
			"Interrupted by the user; safe paired history was retained",
		)
	}
}

func (r *Runtime) persistTerminalArtifactForTurn(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
) {
	if r.sessionArtifacts == nil {
		return
	}
	events, err := r.events.Replay(ctx, 0)
	if err != nil {
		r.logArtifactError(
			"replay terminal event for Checkpoint",
			protocol.Event{ThreadID: threadID, TurnID: turnID},
			err,
		)
		return
	}
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.ThreadID == threadID && event.TurnID == turnID &&
			protocol.IsTerminalEvent(event.Kind) {
			r.persistSessionArtifact(ctx, event)
			return
		}
	}
}

func (r *Runtime) persistTerminalCheckpoint(
	ctx context.Context,
	event protocol.Event,
	status protocol.CheckpointStatus,
	summary string,
) {
	manager, ok := r.engine.(CheckpointEngine)
	if !ok {
		return
	}
	history, err := manager.History(event.ThreadID)
	if err != nil || len(history) == 0 {
		if err != nil {
			r.logArtifactError("read Checkpoint history", event, err)
		}
		return
	}
	encoded, err := EncodeCompactedHistory(history)
	if err != nil {
		r.logArtifactError("encode Checkpoint history", event, err)
		return
	}
	sessionID, err := r.sessionLifecycle.SessionForThread(ctx, event.ThreadID)
	if err != nil {
		r.logArtifactError("resolve Checkpoint Session", event, err)
		return
	}
	profile, err := r.profiles.Profile(ctx, sessionID, r.defaultProfile)
	if err != nil {
		r.logArtifactError("read Checkpoint Profile", event, err)
		return
	}
	changed, external, note, parentCheckpointID := r.checkpointEffects(
		ctx,
		event.ThreadID,
		event.TurnID,
	)
	summary = boundedArtifactText(strings.TrimSpace(summary), 2048)
	if summary == "" {
		summary = fmt.Sprintf("%s Turn %s", status, event.TurnID)
	}
	saved, err := r.sessionArtifacts.SaveCheckpoint(
		ctx,
		protocol.SessionCheckpoint{
			Version: protocol.CheckpointProtocolVersion,
			ID: stableArtifactID(
				"checkpoint",
				sessionID,
				string(event.ThreadID),
				string(event.TurnID),
				fmt.Sprint(event.Sequence),
			),
			SessionID:           sessionID,
			ThreadID:            event.ThreadID,
			TurnID:              event.TurnID,
			Cursor:              event.Sequence,
			Status:              status,
			Summary:             summary,
			ProfileRevision:     profile.Revision,
			ParentCheckpointID:  parentCheckpointID,
			ChangedFiles:        changed,
			ExternalSideEffects: external,
			SideEffectNote:      note,
			CanRestore:          true,
			CanFork:             true,
			CreatedAt:           event.CreatedAt,
		},
		encoded,
		profile,
	)
	if err != nil {
		r.logArtifactError("save Session Checkpoint", event, err)
		return
	}
	if err := r.publish(
		event.OperationID,
		event.ThreadID,
		event.TurnID,
		protocol.ItemID(stableArtifactID(
			"item",
			saved.ID,
			"created",
		)),
		&protocol.CheckpointCreatedData{Checkpoint: saved},
	); err != nil {
		r.logArtifactError("publish Session Checkpoint", event, err)
	}
}

func (r *Runtime) checkpointEffects(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
) (int, bool, string, string) {
	events, err := r.events.Replay(ctx, 0)
	if err != nil {
		return 0, true, "Side-effect receipt could not be read", ""
	}
	changed := make(map[string]struct{})
	external := false
	parentCheckpointID := ""
	for _, event := range events {
		if fork, ok := event.Data.(*protocol.CheckpointForkedData); ok &&
			fork.NewThreadID == threadID {
			parentCheckpointID = fork.CheckpointID
		}
		if event.TurnID != turnID {
			continue
		}
		receipt, ok := event.Data.(*protocol.ExecutionReceiptData)
		if !ok || receipt == nil {
			continue
		}
		for _, change := range receipt.Changes {
			changed[change.Path] = struct{}{}
		}
		external = external || len(receipt.ToolsSucceeded) > 0
	}
	note := ""
	if external {
		note = "Completed Tool effects remain applied and are never replayed by Restore"
	}
	return len(changed), external, note, parentCheckpointID
}

func (r *Runtime) logArtifactError(
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
