package agentcontext

import (
	"errors"

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
	history []provider.Message,
	summaryMaxBytes int,
) (CompletedCompaction, error) {
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
