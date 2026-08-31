package agentcontext

import (
	"errors"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type CompletedCompaction struct {
	State           CompactionState
	History         []provider.Message
	AuthorityDigest string
	RenderedBytes   int
}

func CompleteCompaction(
	state CompactionState,
	artifact *NarrativeArtifact,
	sourceHistory []provider.Message,
	summaryMaxBytes int,
) (CompletedCompaction, error) {
	if state.Plan == nil {
		return CompletedCompaction{}, errors.New("compaction plan is unavailable")
	}
	if err := (Compaction{State: &state}).Validate(); err != nil {
		return CompletedCompaction{}, err
	}
	if err := ValidateCompactionSource(*state.Plan, sourceHistory); err != nil {
		return CompletedCompaction{}, err
	}
	deterministic := state.Plan.DeterministicResult
	history := append(
		[]provider.Message{CloneMessage(deterministic.Head)},
		CloneMessages(deterministic.Tail)...,
	)
	if len(history) == 0 {
		return CompletedCompaction{}, errors.New("narrative rebase history is empty")
	}
	if _, found, err := ParseTruthCapsule(history[0].Text()); err != nil {
		return CompletedCompaction{}, err
	} else if !found {
		return CompletedCompaction{},
			errors.New("narrative rebase lost its truth capsule")
	}
	result := CompletedCompaction{
		State:   state,
		History: CloneMessages(history),
	}
	result.State.Phase = "completed"
	if artifact != nil {
		if state.NarrativeInput == nil {
			return CompletedCompaction{},
				errors.New("narrative input is unavailable")
		}
		if err := artifact.Validate(time.Time{}); err != nil {
			return CompletedCompaction{}, err
		}
		if artifact.InputDigest != state.NarrativeInput.Digest ||
			artifact.AuthorityDigest != state.NarrativeInput.AuthorityDigest ||
			artifact.RouteDigest != state.NarrativeInput.RouteDigest {
			return CompletedCompaction{},
				errors.New("narrative artifact fence is stale")
		}
		rendered, err := RenderStructured(
			Summary{Window: len(state.NarrativeInput.Excerpts)},
			state.Truth,
			artifact.Body,
			summaryMaxBytes,
		)
		if err != nil {
			return CompletedCompaction{}, err
		}
		result.History[0] = provider.TextMessage(provider.RoleSystem, rendered.Text)
		result.RenderedBytes = len(rendered.Text)
		if state.Plan != nil &&
			HistoryBytes(result.History) >= state.Plan.SourceBytes {
			return CompletedCompaction{},
				errors.New("continuation checkpoint did not reduce context")
		}
		value := *artifact
		result.State.Narrative = &value
		result.State.FallbackReason = ""
	}
	authorityDigest, err := state.Truth.AuthorityDigest()
	if err != nil {
		return CompletedCompaction{}, err
	}
	result.AuthorityDigest = authorityDigest
	return result, nil
}

func ValidateCompactionSource(
	plan CompactionPlan,
	history []provider.Message,
) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if HistoryDigest(history) != plan.SourceHistoryDigest ||
		plan.Cut > len(history) {
		return errors.New("compaction source history digest is stale")
	}
	for index, message := range history[:plan.Cut] {
		if plan.RemovedMessageIDs[index] != StableMessageID(
			plan.DeterministicResult.ThreadID,
			message,
			index,
		) {
			return errors.New("compaction removed-message fence is stale")
		}
	}
	tail := history[plan.Cut:]
	if len(tail) != len(plan.TailMessageIDs) {
		return errors.New("compaction tail fence is stale")
	}
	for index, message := range tail {
		if plan.TailMessageIDs[index] != StableMessageID(
			plan.DeterministicResult.ThreadID,
			message,
			plan.Cut+index,
		) {
			return errors.New("compaction tail-message fence is stale")
		}
	}
	return nil
}

type RebaseRequest struct {
	Completed      CompletedCompaction
	Snapshot       ContextSnapshot
	ThreadID       protocol.ThreadID
	TurnID         protocol.TurnID
	ManifestLimits ManifestLimits
}

func BuildRebaseEnvelope(request RebaseRequest) (ContextRebaseEnvelope, error) {
	completed := request.Completed
	snapshot := CloneContextSnapshot(request.Snapshot)
	snapshot.History = CloneMessages(completed.History)
	snapshot.MessageTurns = make([]uint64, len(snapshot.History))
	for index, message := range snapshot.History {
		snapshot.MessageTurns[index] = message.Turn
	}
	snapshot.Compaction.State = &completed.State
	if err := snapshot.Seal(); err != nil {
		return ContextRebaseEnvelope{}, err
	}
	envelope := ContextRebaseEnvelope{
		CompactionID:        completed.State.ID,
		ThreadID:            request.ThreadID,
		TurnID:              request.TurnID,
		SourceWindowID:      completed.State.SourceWindowID,
		TargetWindowID:      completed.State.TargetWindowID,
		BaseRevision:        snapshot.Revision - 1,
		SourceContextDigest: completed.State.SourceContextDigest,
		AuthorityDigest:     completed.AuthorityDigest,
		ManifestLimits:      request.ManifestLimits,
		Snapshot:            snapshot,
	}
	if completed.State.Narrative != nil {
		envelope.NarrativeDigest = completed.State.Narrative.Digest
	}
	if err := envelope.Seal(); err != nil {
		return ContextRebaseEnvelope{}, err
	}
	return envelope, nil
}
