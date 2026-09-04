package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func (e *Engine) orphanedDraftTurnID(ctx context.Context) string {
	if e.journal == nil || e.options.SessionForTurn == nil {
		return ""
	}
	ids := e.journal.DraftTurnIDs()
	if len(ids) != 1 {
		return ""
	}
	if _, alive := e.options.SessionForTurn(ctx, ids[0]); alive {
		return ""
	}
	return ids[0]
}

func (e *Engine) retryDraftTurnID(ctx context.Context, spec TurnSpec) string {
	if e.journal == nil ||
		spec.Request.Recovery == nil ||
		spec.Request.Recovery.Action != protocol.TurnRecoveryRetry {
		return ""
	}
	sourceTurnID := string(spec.Request.Recovery.SourceTurnID)
	if e.journal.HasDraft(sourceTurnID) {
		return sourceTurnID
	}
	return e.orphanedDraftTurnID(ctx)
}

func (e *Engine) journalAdmissionProblem(
	ctx context.Context,
	journalErr error,
) *protocol.Problem {
	problem := protocol.ProblemOf(journalErr)
	if journalErr == nil ||
		!errors.Is(journalErr, workspacejournal.ErrRetainedDraft) ||
		e.orphanedDraftTurnID(ctx) == "" {
		return problem
	}
	return protocol.NewFault(
		protocol.CodeConflict,
		journalErr.Error(),
		true,
		protocol.FaultMetadata{
			Origin:         protocol.FaultOriginPersistence,
			Disposition:    protocol.FaultRetryTurn,
			SideEffects:    protocol.SideEffectDraft,
			RecoveryAction: "continue to keep the orphaned workspace draft, or retry to revert it",
		},
		journalErr,
	)
}
