package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type PlanTransitionPreparation struct {
	Artifact       protocol.SessionPlanArtifact
	ProfileUpdate  protocol.SessionProfileUpdateResult
	Prompt         string
	Intent         protocol.TurnIntent
	IdempotencyKey string
}

type TurnRecoveryPreparation struct {
	Prompt         string
	DisplayPrompt  string
	Intent         protocol.TurnIntent
	IdempotencyKey string
	Recovery       protocol.TurnRecoveryContext
}

const turnRecoveryOutputLimit = 16 << 10
const turnRecoveryEvidenceLimit = 12 << 10
const turnRecoveryPromptPrefix = "Continue the exact source Turn identified below."

type recoveryToolStart struct {
	Tool            string
	ArgumentsDigest string
}

type recoveryToolEvidence struct {
	Tool            string                `json:"tool"`
	CallID          string                `json:"call_id"`
	ArgumentsDigest string                `json:"arguments_digest,omitempty"`
	OutputDigest    string                `json:"output_digest"`
	IsError         bool                  `json:"is_error"`
	Changes         []protocol.FileChange `json:"changes,omitempty"`
}

type recoveryReceiptEvidence struct {
	Outcome          protocol.TurnOutcome              `json:"outcome,omitempty"`
	ReadPaths        []string                          `json:"read_paths,omitempty"`
	Changes          []protocol.ReceiptChange          `json:"changes,omitempty"`
	Verification     protocol.ReceiptVerification      `json:"verification"`
	WorkspaceOutcome *protocol.ReceiptWorkspaceOutcome `json:"workspace_outcome,omitempty"`
}

type recoveryEvidenceCapsule struct {
	Version      int                      `json:"version"`
	SourceTurnID protocol.TurnID          `json:"source_turn_id"`
	Intent       protocol.TurnIntent      `json:"intent"`
	Terminal     string                   `json:"terminal"`
	Tools        []recoveryToolEvidence   `json:"closed_tools,omitempty"`
	OmittedTools int                      `json:"omitted_tools,omitempty"`
	Receipt      *recoveryReceiptEvidence `json:"receipt,omitempty"`
}

func (r *ArtifactService) PrepareTurnRecovery(
	ctx context.Context,
	request protocol.TurnRecoveryRequest,
) (TurnRecoveryPreparation, error) {
	if err := request.Validate(); err != nil {
		return TurnRecoveryPreparation{},
			runtimeProblem(protocol.CodeInvalidArgument, err.Error(), err)
	}
	current, err := r.SessionStatus(ctx, request.SessionID)
	if err != nil {
		return TurnRecoveryPreparation{}, err
	}
	if err := ensureSessionQuiescent(current, string(request.Action)); err != nil {
		return TurnRecoveryPreparation{}, err
	}
	if r.SessionProfilesAvailable() {
		if _, err := r.RestoreSessionProfile(
			ctx,
			request.SessionID,
			current.ThreadID,
		); err != nil {
			return TurnRecoveryPreparation{}, fmt.Errorf(
				"restore current session profile for Turn recovery: %w",
				err,
			)
		}
	}
	events, err := r.events.Replay(ctx, 0)
	if err != nil {
		return TurnRecoveryPreparation{}, err
	}
	var started *protocol.TurnStartedData
	toolStarts := make(map[string]recoveryToolStart)
	var closedTools []recoveryToolEvidence
	var sourceReceipt *protocol.ExecutionReceiptData
	terminal := false
	terminalState := ""
	var partialOutput strings.Builder
	for _, event := range events {
		if event.ThreadID != current.ThreadID ||
			event.TurnID != request.SourceTurnID {
			continue
		}
		switch data := event.Data.(type) {
		case *protocol.TurnStartedData:
			copy := *data
			started = &copy
		case *protocol.OutputDeltaData:
			appendBoundedRecoveryOutput(&partialOutput, data.Text)
		case *protocol.ToolStartData:
			toolStarts[data.CallID] = recoveryToolStart{
				Tool: data.Tool, ArgumentsDigest: recoveryDigestJSON(data.Arguments),
			}
		case *protocol.ToolResultData:
			start, ok := toolStarts[data.CallID]
			if !ok || start.Tool != data.Tool {
				continue
			}
			closedTools = append(closedTools, recoveryToolEvidence{
				Tool: data.Tool, CallID: data.CallID,
				ArgumentsDigest: start.ArgumentsDigest,
				OutputDigest:    recoveryDigest([]byte(data.Output)),
				IsError:         data.IsError,
				Changes:         append([]protocol.FileChange(nil), data.Changes...),
			})
			delete(toolStarts, data.CallID)
		case *protocol.ExecutionReceiptData:
			copy := *data
			sourceReceipt = &copy
		case *protocol.TurnCompletedData:
			terminal = true
			terminalState = "completed"
		case *protocol.TurnFailedData:
			terminal = true
			terminalState = fmt.Sprintf("failed (%s): %s", data.Code, data.Message)
		case *protocol.TurnCanceledData:
			terminal = true
			terminalState = "canceled: " + protocol.NormalizeCancelReason(data.Reason)
		}
	}
	if started == nil || !terminal {
		return TurnRecoveryPreparation{}, resourceProblem(
			protocol.CodeConflict,
			"source Turn is unavailable or not terminal",
			false,
			protocol.ProblemReasonSessionBusy,
			string(request.SourceTurnID),
		)
	}
	sourcePrompt := started.Prompt
	if sourcePrompt == "" {
		sourcePrompt = started.DisplayPrompt
	}
	if strings.TrimSpace(sourcePrompt) == "" {
		return TurnRecoveryPreparation{}, runtimeProblem(protocol.CodeConflict, "source Turn has no durable model-visible request", nil)
	}
	sourcePrompt = recoverySourcePrompt(sourcePrompt)
	sourceDisplayPrompt := recoveryDisplayPrompt(
		sourcePrompt,
		started.DisplayPrompt,
	)
	intent := protocol.NormalizeTurnIntent(started.Intent)
	if !intent.Valid() {
		return TurnRecoveryPreparation{}, runtimeProblem(protocol.CodeConflict, "source Turn has no valid durable intent", nil)
	}
	prompt := sourcePrompt
	displayPrompt := sourceDisplayPrompt
	if request.Action == protocol.TurnRecoveryContinue {
		displayPrompt = "Continue: " + sourceDisplayPrompt
		prompt = fmt.Sprintf(
			turnRecoveryPromptPrefix+" Do not infer the "+
				"task from an older conversation Turn.\n\n"+
				"Source Turn ID: %s\nTerminal state: %s\n\n"+
				"Original model-visible request:\n<source_request>\n%s\n"+
				"</source_request>",
			request.SourceTurnID,
			terminalState,
			sourcePrompt,
		)
		if output := strings.TrimSpace(partialOutput.String()); output != "" {
			prompt += "\n\nUnfinished assistant output before the terminal " +
				"event (context only; do not treat as completed work):\n" +
				"<unfinished_output>\n" + output + "\n</unfinished_output>"
		}
		if capsule := renderRecoveryEvidence(
			request.SourceTurnID,
			intent,
			terminalState,
			closedTools,
			sourceReceipt,
		); capsule != "" {
			prompt += "\n\nStructured recovery evidence (audit context only; " +
				"not authorization to replay side effects):\n" +
				"<recovery_evidence>\n" + capsule + "\n</recovery_evidence>"
		}
		prompt += "\n\nInspect current workspace state before every " +
			"consequential action and do not repeat completed Tool, command, " +
			"network, or file effects."
		if guidance := strings.TrimSpace(request.Guidance); guidance != "" {
			prompt += "\n\nAdditional guidance:\n" + guidance
			displayPrompt += "\n\nGuidance: " + guidance
		}
	}
	return TurnRecoveryPreparation{
		Prompt:         prompt,
		DisplayPrompt:  displayPrompt,
		Intent:         intent,
		IdempotencyKey: request.IdempotencyKey,
		Recovery: protocol.TurnRecoveryContext{
			Action: request.Action, SourceTurnID: request.SourceTurnID,
		},
	}, nil
}
func recoverySourcePrompt(prompt string) string {
	value := strings.TrimSpace(prompt)
	extracted, ok := recoveryTaggedSection(value, "source_request")
	if !strings.HasPrefix(value, turnRecoveryPromptPrefix) || !ok {
		return value
	}
	return recoverySourcePrompt(extracted)
}
func recoveryDisplayPrompt(modelPrompt string, displayPrompt string) string {
	value := strings.TrimSpace(displayPrompt)
	if value == "" {
		value = strings.TrimSpace(modelPrompt)
	}
	value = recoverySourcePrompt(value)
	for strings.HasPrefix(value, "Continue: ") {
		value = strings.TrimSpace(strings.TrimPrefix(value, "Continue: "))
	}
	return value
}
func recoveryTaggedSection(prompt string, tag string) (string, bool) {
	open, close := "<"+tag+">", "</"+tag+">"
	_, body, ok := strings.Cut(prompt, open)
	if !ok {
		return "", false
	}
	for end := 0; ; end += len(close) {
		offset := strings.Index(body[end:], close)
		if offset < 0 {
			return "", false
		}
		end += offset
		if section := body[:end]; strings.Count(section, open) ==
			strings.Count(section, close) {
			return section, true
		}
	}
}
func renderRecoveryEvidence(
	sourceTurnID protocol.TurnID,
	intent protocol.TurnIntent,
	terminal string,
	tools []recoveryToolEvidence,
	receipt *protocol.ExecutionReceiptData,
) string {
	capsule := recoveryEvidenceCapsule{
		Version:      1,
		SourceTurnID: sourceTurnID,
		Intent:       intent,
		Terminal:     terminal,
		Tools:        append([]recoveryToolEvidence(nil), tools...),
	}
	if receipt != nil {
		capsule.Receipt = &recoveryReceiptEvidence{
			Outcome:      receipt.Outcome,
			ReadPaths:    append([]string(nil), receipt.ReadPaths...),
			Changes:      append([]protocol.ReceiptChange(nil), receipt.Changes...),
			Verification: receipt.Verification,
		}
		if receipt.WorkspaceOutcome != nil {
			copy := *receipt.WorkspaceOutcome
			capsule.Receipt.WorkspaceOutcome = &copy
		}
		if terminal != "completed" {
			capsule.Receipt.Outcome = ""
		}
	}
	for {
		data, err := json.Marshal(capsule)
		if err != nil {
			return ""
		}
		if len(data) <= turnRecoveryEvidenceLimit {
			return string(data)
		}
		switch {
		case len(capsule.Tools) != 0:
			capsule.Tools = capsule.Tools[:len(capsule.Tools)-1]
			capsule.OmittedTools++
		case capsule.Receipt != nil && len(capsule.Receipt.ReadPaths) != 0:
			capsule.Receipt.ReadPaths =
				capsule.Receipt.ReadPaths[:len(capsule.Receipt.ReadPaths)-1]
		case capsule.Receipt != nil && len(capsule.Receipt.Changes) != 0:
			capsule.Receipt.Changes =
				capsule.Receipt.Changes[:len(capsule.Receipt.Changes)-1]
		default:
			return ""
		}
	}
}
func recoveryDigestJSON(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	if !json.Valid(data) {
		return recoveryDigest(data)
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return recoveryDigest(data)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return recoveryDigest(data)
	}
	return recoveryDigest(canonical)
}
func recoveryDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func appendBoundedRecoveryOutput(builder *strings.Builder, text string) {
	if text == "" || builder.Len() >= turnRecoveryOutputLimit {
		return
	}
	remaining := turnRecoveryOutputLimit - builder.Len()
	if len(text) > remaining {
		text = text[:remaining]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	builder.WriteString(text)
}

func (r *ArtifactService) Checkpoints(
	ctx context.Context,
	sessionID string,
	limit int,
) (protocol.CheckpointList, error) {
	if r.sessionArtifacts == nil {
		return protocol.CheckpointList{}, runtimeProblem(protocol.CodeUnavailable, "Session Checkpoints are unavailable", nil)
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		return protocol.CheckpointList{}, runtimeProblem(protocol.CodeInvalidArgument, "Checkpoint limit exceeds 1000", nil)
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

func (r *ArtifactService) Checkpoint(
	ctx context.Context,
	sessionID, checkpointID string,
) (protocol.SessionCheckpoint, error) {
	if r.sessionArtifacts == nil {
		return protocol.SessionCheckpoint{}, runtimeProblem(protocol.CodeUnavailable, "Session Checkpoints are unavailable", nil)
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
		return protocol.SessionCheckpoint{}, runtimeProblem(protocol.CodeInvalidArgument, "Checkpoint does not belong to the Session", nil)
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

func (r *ArtifactService) RestoreCheckpoint(
	ctx context.Context,
	sessionID, checkpointID string,
) (protocol.CheckpointRestoreResult, error) {
	current, checkpoint, history, contextSnapshot, err := r.checkpointState(
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
		return protocol.CheckpointRestoreResult{}, resourceProblem(
			protocol.CodeUnavailable,
			"Checkpoint restore is unsupported by this engine",
			false,
			protocol.ProblemReasonUnsupported,
			checkpointID,
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
	var (
		reconciliation  sessiondelta.ReconciliationReceipt
		previousContext *sessiondelta.ContextSnapshot
	)
	if contextSnapshot != nil {
		contextManager, supported := manager.(ContextCheckpointEngine)
		if !supported {
			return protocol.CheckpointRestoreResult{}, resourceProblem(
				protocol.CodeUnavailable,
				"Context checkpoint restore is unsupported by this engine",
				false,
				protocol.ProblemReasonUnsupported,
				checkpointID,
			)
		}
		value, exportErr := contextManager.ContextSnapshot(current.ThreadID)
		if exportErr != nil {
			return protocol.CheckpointRestoreResult{}, exportErr
		}
		previousContext = &value
		reconciliation, err = contextManager.RestoreContext(
			current.ThreadID,
			*contextSnapshot,
		)
	} else {
		err = manager.RestoreCheckpoint(current.ThreadID, decoded)
	}
	if err != nil {
		return protocol.CheckpointRestoreResult{}, err
	}
	operationID := protocol.OperationID(stableArtifactID(
		"op",
		sessionID,
		checkpointID,
		"restore",
	))
	currentCommitID := ""
	var committedContext *sessiondelta.ContextSnapshot
	if contextSnapshot != nil && r.durable {
		store, supported := r.contextRebaseStore.(CurrentContextStore)
		if !supported {
			_, rollbackErr := manager.(ContextCheckpointEngine).RestoreContext(
				current.ThreadID,
				*previousContext,
			)
			return protocol.CheckpointRestoreResult{}, errors.Join(
				errors.New("durable current context store is unavailable"),
				rollbackErr,
			)
		}
		restored, exportErr := manager.(ContextCheckpointEngine).ContextSnapshot(
			current.ThreadID,
		)
		if exportErr != nil {
			_, rollbackErr := manager.(ContextCheckpointEngine).RestoreContext(
				current.ThreadID,
				*previousContext,
			)
			return protocol.CheckpointRestoreResult{}, errors.Join(
				exportErr,
				rollbackErr,
			)
		}
		currentCommitID = stableArtifactID(
			"context",
			string(operationID),
			restored.Digest,
		)
		commitErr := store.CommitCurrentContext(
			ctx,
			sessiondelta.CurrentContextCommit{
				ID:       currentCommitID,
				ThreadID: current.ThreadID,
				TurnID:   checkpoint.TurnID,
				Snapshot: restored,
			},
		)
		if commitErr != nil {
			_, rollbackErr := manager.(ContextCheckpointEngine).RestoreContext(
				current.ThreadID,
				*previousContext,
			)
			return protocol.CheckpointRestoreResult{}, errors.Join(
				commitErr,
				rollbackErr,
			)
		}
		committedContext = &restored
	}
	itemID := protocol.ItemID(stableArtifactID(
		"item",
		sessionID,
		checkpointID,
		"restore",
	))
	var contextDigest string
	var contextRevision, stateEpoch uint64
	if committedContext != nil {
		contextDigest = committedContext.Digest
		contextRevision = committedContext.Revision
		stateEpoch = committedContext.Epoch
	}
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
			ExactContext:        contextSnapshot != nil,
			WorkspaceClaimsValid: contextSnapshot != nil &&
				reconciliation.Stale == 0,
			InvalidatedClaims: reconciliation.Invalidated,
			StaleClaims:       reconciliation.Stale,
			ContextCommitID:   currentCommitID,
			ContextDigest:     contextDigest,
			ContextRevision:   contextRevision,
			StateEpoch:        stateEpoch,
		},
	)
	if publishErr != nil {
		var rollbackErr error
		if previousContext != nil {
			contextManager := manager.(ContextCheckpointEngine)
			_, rollbackErr = contextManager.RestoreContext(
				current.ThreadID,
				*previousContext,
			)
			if rollbackErr == nil && currentCommitID != "" {
				rollback, exportErr := contextManager.ContextSnapshot(
					current.ThreadID,
				)
				if exportErr != nil {
					rollbackErr = exportErr
				} else {
					store := r.contextRebaseStore.(CurrentContextStore)
					rollbackErr = store.CommitCurrentContext(
						ctx,
						sessiondelta.CurrentContextCommit{
							ID: stableArtifactID(
								"context",
								string(operationID),
								"rollback",
								rollback.Digest,
							),
							ThreadID: current.ThreadID,
							TurnID:   checkpoint.TurnID,
							Snapshot: rollback,
						},
					)
				}
			}
		} else {
			rollbackErr = manager.RestoreCheckpoint(current.ThreadID, previous)
		}
		return protocol.CheckpointRestoreResult{}, errors.Join(
			publishErr,
			rollbackErr,
		)
	}
	return protocol.CheckpointRestoreResult{
		Version:             protocol.CheckpointProtocolVersion,
		Checkpoint:          checkpoint,
		ThreadID:            current.ThreadID,
		RestoredCursor:      checkpoint.Cursor,
		SideEffectsReplayed: false,
		ExactContext:        contextSnapshot != nil,
		WorkspaceClaimsValid: contextSnapshot != nil &&
			reconciliation.Stale == 0,
		InvalidatedClaims: reconciliation.Invalidated,
		StaleClaims:       reconciliation.Stale,
	}, nil
}

func (r *ArtifactService) ForkCheckpoint(
	ctx context.Context,
	sessionID, checkpointID, title string,
) (protocol.CheckpointForkResult, error) {
	_, checkpoint, history, contextSnapshot, err := r.checkpointState(
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
		return protocol.CheckpointForkResult{}, resourceProblem(
			protocol.CodeUnavailable,
			"Checkpoint Fork is unsupported by this engine",
			false,
			protocol.ProblemReasonUnsupported,
			checkpointID,
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
	var reconciliation sessiondelta.ReconciliationReceipt
	currentCommitID := ""
	var committedContext *sessiondelta.ContextSnapshot
	if contextSnapshot != nil {
		contextManager, supported := manager.(ContextCheckpointEngine)
		if !supported {
			return protocol.CheckpointForkResult{}, resourceProblem(
				protocol.CodeUnavailable,
				"Context checkpoint Fork is unsupported by this engine",
				false,
				protocol.ProblemReasonUnsupported,
				checkpointID,
			)
		}
		reconciliation, err = contextManager.ForkContext(
			checkpoint.ThreadID,
			newThreadID,
			*contextSnapshot,
		)
		if err == nil && r.durable {
			store, durable := r.contextRebaseStore.(CurrentContextStore)
			if !durable {
				manager.Release(newThreadID)
				return protocol.CheckpointForkResult{},
					errors.New("durable current context store is unavailable")
			}
			forked, exportErr := contextManager.ContextSnapshot(newThreadID)
			if exportErr != nil {
				manager.Release(newThreadID)
				return protocol.CheckpointForkResult{}, exportErr
			}
			currentCommitID = stableArtifactID(
				"context",
				string(operationID),
				forked.Digest,
			)
			err = store.CommitCurrentContext(
				ctx,
				sessiondelta.CurrentContextCommit{
					ID:             currentCommitID,
					ThreadID:       newThreadID,
					TurnID:         checkpoint.TurnID,
					SessionID:      sessionID,
					ParentThreadID: checkpoint.ThreadID,
					Title:          title,
					SourceCursor:   checkpoint.Cursor,
					Snapshot:       forked,
				},
			)
			if err != nil {
				manager.Release(newThreadID)
				return protocol.CheckpointForkResult{}, err
			}
			committedContext = &forked
		}
	} else {
		err = manager.ForkCheckpoint(
			checkpoint.ThreadID,
			newThreadID,
			decoded,
		)
	}
	if err != nil {
		return protocol.CheckpointForkResult{}, err
	}
	var contextDigest string
	var contextRevision, stateEpoch uint64
	if committedContext != nil {
		contextDigest = committedContext.Digest
		contextRevision = committedContext.Revision
		stateEpoch = committedContext.Epoch
	}
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
			ExactContext: contextSnapshot != nil,
			WorkspaceClaimsValid: contextSnapshot != nil &&
				reconciliation.Stale == 0,
			InvalidatedClaims: reconciliation.Invalidated,
			StaleClaims:       reconciliation.Stale,
			ContextCommitID:   currentCommitID,
			ContextDigest:     contextDigest,
			ContextRevision:   contextRevision,
			StateEpoch:        stateEpoch,
		},
	); err != nil {
		if currentCommitID != "" {
			store := r.contextRebaseStore.(CurrentContextStore)
			err = errors.Join(
				err,
				store.DeleteCurrentContext(
					ctx,
					newThreadID,
					currentCommitID,
					true,
				),
			)
		}
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
		Version:      protocol.CheckpointProtocolVersion,
		Checkpoint:   checkpoint,
		SessionID:    sessionID,
		ThreadID:     newThreadID,
		ParentID:     checkpoint.ThreadID,
		ExactContext: contextSnapshot != nil,
		WorkspaceClaimsValid: contextSnapshot != nil &&
			reconciliation.Stale == 0,
		InvalidatedClaims: reconciliation.Invalidated,
		StaleClaims:       reconciliation.Stale,
	}, nil
}

func (r *ArtifactService) SessionPlan(
	ctx context.Context,
	sessionID string,
) (protocol.SessionPlanSnapshot, error) {
	if r.sessionArtifacts == nil {
		return protocol.SessionPlanSnapshot{}, runtimeProblem(protocol.CodeUnavailable, "Session Plan Artifacts are unavailable", nil)
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

func (r *ArtifactService) PreparePlanTransition(
	ctx context.Context,
	sessionID, planID string,
	transition protocol.PlanTransition,
) (PlanTransitionPreparation, error) {
	if r.sessionArtifacts == nil {
		return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeUnavailable, "Session Plan Artifacts are unavailable", nil)
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
		return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeInvalidArgument, "Plan Artifact does not belong to the active Session Thread", nil)
	}
	profile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if profile.Profile.Revision != artifact.ProfileRevision {
		return PlanTransitionPreparation{}, retryableProblem(
			protocol.CodeConflict,
			"Plan Artifact Profile Revision is stale",
		)
	}
	mode := "act"
	patch := protocol.SessionProfilePatch{Mode: &mode}
	switch transition {
	case protocol.PlanTransitionImplement:
		if !artifact.CanImplement {
			return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeConflict, "Plan Artifact cannot start implementation", nil)
		}
	case protocol.PlanTransitionAutopilot:
		if !artifact.CanAutopilot {
			return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeConflict, "Plan Artifact cannot start Autopilot", nil)
		}
		posture := "auto"
		patch.ApprovalPosture = &posture
	default:
		return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeInvalidArgument, "Plan transition is invalid", nil)
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
		Intent:        protocol.TurnIntentWorkspaceChange,
		IdempotencyKey: fmt.Sprintf(
			"plan:%s:%s",
			artifact.ID,
			transition,
		),
	}, nil
}

func (r *ArtifactService) PreparePlanTransitionTo(
	ctx context.Context,
	sourceSessionID, targetSessionID, planID string,
	transition protocol.PlanTransition,
) (PlanTransitionPreparation, error) {
	if r.sessionArtifacts == nil {
		return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeUnavailable, "Session Plan Artifacts are unavailable", nil)
	}
	artifact, err := r.sessionArtifacts.GetPlan(ctx, planID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if artifact.SessionID != sourceSessionID {
		return PlanTransitionPreparation{}, resourceProblem(
			protocol.CodeInvalidArgument,
			"Plan Artifact does not belong to the source Session",
			false,
			protocol.ProblemReasonWrongSession,
			planID,
		)
	}
	sourceProfile, err := r.SessionProfile(ctx, sourceSessionID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if sourceProfile.Profile.Revision != artifact.ProfileRevision {
		return PlanTransitionPreparation{}, revisionProblem(
			"Plan Artifact Profile Revision is stale",
			planID,
			artifact.ProfileRevision,
			sourceProfile.Profile.Revision,
		)
	}
	current, err := r.SessionStatus(ctx, targetSessionID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if err := ensureSessionQuiescent(current, "implement Plan"); err != nil {
		return PlanTransitionPreparation{}, err
	}
	if sourceSessionID == targetSessionID &&
		current.ParentThreadID != artifact.ThreadID {
		return PlanTransitionPreparation{}, resourceProblem(
			protocol.CodeInvalidArgument,
			"Plan Artifact does not belong to the parent Fork Thread",
			false,
			protocol.ProblemReasonWrongSession,
			planID,
		)
	}
	profile, err := r.SessionProfile(ctx, targetSessionID)
	if err != nil {
		return PlanTransitionPreparation{}, err
	}
	if !samePlanTargetProfile(sourceProfile.Profile, profile.Profile) {
		return PlanTransitionPreparation{}, resourceProblem(
			protocol.CodeConflict,
			"target Session Profile does not match the Plan source Profile",
			false,
			protocol.ProblemReasonWrongSession,
			targetSessionID,
		)
	}
	mode := "act"
	patch := protocol.SessionProfilePatch{Mode: &mode}
	switch transition {
	case protocol.PlanTransitionImplement:
		if !artifact.CanImplement {
			return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeConflict, "Plan Artifact cannot start implementation", nil)
		}
	case protocol.PlanTransitionAutopilot:
		if !artifact.CanAutopilot {
			return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeConflict, "Plan Artifact cannot start Autopilot", nil)
		}
		posture := "auto"
		patch.ApprovalPosture = &posture
	default:
		return PlanTransitionPreparation{}, runtimeProblem(protocol.CodeInvalidArgument, "Plan transition is invalid", nil)
	}
	updated, err := r.UpdateSessionProfile(
		ctx,
		targetSessionID,
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
		Intent:        protocol.TurnIntentWorkspaceChange,
		IdempotencyKey: fmt.Sprintf(
			"plan:%s:%s:%s",
			artifact.ID,
			transition,
			targetSessionID,
		),
	}, nil
}
func samePlanTargetProfile(
	source, target protocol.SessionProfile,
) bool {
	return source.Mode == target.Mode &&
		source.Provider == target.Provider &&
		source.Model == target.Model &&
		source.ReasoningEffort == target.ReasoningEffort &&
		slices.Equal(source.EnabledToolIDs, target.EnabledToolIDs) &&
		source.ApprovalPosture == target.ApprovalPosture &&
		source.ExecutionTarget == target.ExecutionTarget &&
		source.MaxSteps == target.MaxSteps
}
func (r *ArtifactService) checkpointState(
	ctx context.Context,
	sessionID, checkpointID, action string,
) (
	protocol.SessionSummary,
	protocol.SessionCheckpoint,
	[]protocol.CompactedMessage,
	*sessiondelta.ContextSnapshot,
	error,
) {
	if r.sessionArtifacts == nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil,
			runtimeProblem(protocol.CodeUnavailable, "Session Checkpoints are unavailable", nil)
	}
	current, err := r.SessionStatus(ctx, sessionID)
	if err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil, err
	}
	if err := ensureSessionQuiescent(current, action); err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil, err
	}
	checkpoint, history, checkpointProfile, err :=
		r.sessionArtifacts.GetCheckpoint(ctx, checkpointID)
	if err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil, err
	}
	if checkpoint.SessionID != sessionID {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil,
			resourceProblem(
				protocol.CodeInvalidArgument,
				"Checkpoint does not belong to the Session",
				false,
				protocol.ProblemReasonWrongSession,
				checkpointID,
			)
	}
	currentProfile, err := r.SessionProfile(ctx, sessionID)
	if err != nil {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil, err
	}
	if currentProfile.Profile.Revision != checkpoint.ProfileRevision ||
		checkpointProfile.Revision != checkpoint.ProfileRevision {
		return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil,
			revisionProblem(
				"Checkpoint Profile Revision is stale",
				checkpointID,
				checkpoint.ProfileRevision,
				currentProfile.Profile.Revision,
			)
	}
	var contextSnapshot *sessiondelta.ContextSnapshot
	if checkpoint.ContextDigest != "" {
		store, ok := r.sessionArtifacts.(ContextSessionArtifactStore)
		if !ok {
			return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil,
				errors.New("context checkpoint store is unavailable")
		}
		contextCheckpoint, snapshot, storedProfile, contextErr :=
			store.GetContextCheckpoint(ctx, checkpointID)
		if contextErr != nil {
			return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil,
				contextErr
		}
		if contextCheckpoint.ID != checkpoint.ID ||
			storedProfile.Revision != checkpointProfile.Revision {
			return protocol.SessionSummary{}, protocol.SessionCheckpoint{}, nil, nil,
				errors.New("context checkpoint lookup is inconsistent")
		}
		contextSnapshot = &snapshot
	}
	return current, checkpoint, history, contextSnapshot, nil
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
func (r *ArtifactService) decoratePlanArtifact(
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
func (r *ArtifactService) persistSessionArtifact(
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
func (r *ArtifactService) persistTerminalArtifactForTurn(
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
func (r *ArtifactService) persistTerminalCheckpoint(
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
	changed, external, note, parentCheckpointID, receipt := r.checkpointEffects(
		ctx,
		event.ThreadID,
		event.TurnID,
	)
	summary = boundedArtifactText(strings.TrimSpace(summary), 2048)
	if summary == "" {
		summary = fmt.Sprintf("%s Turn %s", status, event.TurnID)
	}
	checkpoint := protocol.SessionCheckpoint{
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
		ChangeReceipt:       receipt,
		ChangedFiles:        changed,
		ExternalSideEffects: external,
		SideEffectNote:      note,
		CanRestore:          true,
		CanFork:             true,
		CreatedAt:           event.CreatedAt,
	}
	var saved protocol.SessionCheckpoint
	if contextManager, ok := manager.(ContextCheckpointEngine); ok {
		contextSnapshot, snapshotErr := contextManager.ContextSnapshot(
			event.ThreadID,
		)
		if snapshotErr != nil {
			r.logArtifactError("snapshot Checkpoint context", event, snapshotErr)
			return
		}
		store, supported := r.sessionArtifacts.(ContextSessionArtifactStore)
		if !supported {
			r.logArtifactError(
				"save Checkpoint context",
				event,
				errors.New("context checkpoint store is unavailable"),
			)
			return
		}
		checkpoint.StateEpoch = contextSnapshot.Epoch
		checkpoint.ContextDigest = contextSnapshot.Digest
		checkpoint.WorkspaceDigest = contextSnapshot.Workspace.SparseDigest
		saved, err = store.SaveContextCheckpoint(
			ctx,
			checkpoint,
			encoded,
			contextSnapshot,
			profile,
		)
	} else {
		saved, err = r.sessionArtifacts.SaveCheckpoint(
			ctx,
			checkpoint,
			encoded,
			profile,
		)
	}
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
func (r *ArtifactService) checkpointEffects(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
) (int, bool, string, string, *protocol.ReceiptReference) {
	events, err := r.events.Replay(ctx, 0)
	if err != nil {
		return 0, true, "Side-effect receipt could not be read", "", nil
	}
	changed := make(map[string]struct{})
	external := false
	parentCheckpointID := ""
	var reference *protocol.ReceiptReference
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
		reference = &protocol.ReceiptReference{
			EventID: event.ID, TurnID: event.TurnID, Cursor: event.Sequence,
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
	return len(changed), external, note, parentCheckpointID, reference
}
func (r *ArtifactService) logArtifactError(
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
