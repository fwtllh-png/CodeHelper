package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type NarrativeGenerationResult struct {
	Artifact      agentcontext.NarrativeArtifact
	Usage         provider.Usage
	Provider      string
	Model         string
	CostUSD       float64
	CostKnown     bool
	Attempt       uint32
	RouteDigest   string
	Fallback      bool
	FailureReason string
	Receipt       *CompactionReceipt
}

func (e *Engine) stageNarrativeCandidate(
	candidate compactionCandidate,
) *agentcontext.CompactionState {
	scope := e.runningScope()
	threadID := protocol.ThreadID(e.options.SessionID)
	turnID := protocol.TurnID("")
	if scope != nil {
		if scope.spec.Identity.ThreadID != "" {
			threadID = protocol.ThreadID(scope.spec.Identity.ThreadID)
		}
		turnID = protocol.TurnID(scope.spec.Identity.TurnID)
	}
	if threadID == "" {
		threadID = "thread-local"
	}
	if turnID == "" {
		turnID = protocol.TurnID(fmt.Sprintf("turn-%d", e.turn))
	}
	routeDigest, err := e.SummaryRouteDigest()
	stableDigest, stableErr := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{Stable: e.promptMessages()},
	).Snapshot().Digest()
	routeFailure := ""
	if err != nil {
		routeFailure = err.Error()
	} else if stableErr != nil {
		routeFailure = stableErr.Error()
	}
	state := agentcontext.PrepareCompactionState(
		agentcontext.CompactionPreparation{
			Candidate: candidate, Previous: e.compactionState().State,
			ThreadID: threadID, TurnID: turnID,
			TargetWindowID:     e.currentWindowLedger().ID,
			StablePrefixDigest: stableDigest,
			RouteDigest:        routeDigest,
			RouteFailure:       routeFailure,
			Trigger:            e.options.Context.SemanticNarrative,
			NarrativeLimits:    e.options.Context.NarrativeLimits,
			Now:                time.Now().UTC(),
			InputTTL:           24 * time.Hour,
		},
	)
	e.stageContextCompaction(state)
	return agentcontext.CloneCompaction(
		agentcontext.Compaction{State: state},
	).State
}

func (e *Engine) SummaryRouteDigest() (string, error) {
	return agentcontext.SummaryRouteDigest(e.options.Routes)
}

// GenerateNarrative runs a tool-free Context maintenance sample.
func (e *Engine) GenerateNarrative(
	ctx context.Context,
	truth agentcontext.TruthCapsule,
	input agentcontext.NarrativeInputArtifact,
	createdTurn uint64,
) (NarrativeGenerationResult, error) {
	if e.options.Context.SemanticNarrative == "off" {
		return NarrativeGenerationResult{
			Fallback: true, FailureReason: "disabled",
		}, nil
	}
	result, err := agentcontext.GenerateNarrative(
		ctx,
		agentcontext.NarrativeGeneratorConfig{
			Provider: e.options.Provider, Routes: e.options.Routes,
			TokenEstimator: e.options.TokenEstimator,
			Limits:         e.options.Context.NarrativeLimits,
			Timeout:        e.options.Context.NarrativeTimeout,
		},
		truth,
		input,
		createdTurn,
	)
	return NarrativeGenerationResult{
		Artifact: result.Artifact, Usage: result.Usage,
		Provider: result.Provider, Model: result.Model,
		CostUSD: result.CostUSD, CostKnown: result.CostKnown,
		RouteDigest: result.RouteDigest,
	}, err
}

func (e *Engine) RunPostTurnNarrative(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
) (NarrativeGenerationResult, error) {
	if e.options.Context.SemanticNarrative != "post_turn" {
		return NarrativeGenerationResult{
			Fallback: true, FailureReason: "disabled",
		}, nil
	}
	maintained, err := agentcontext.RunPostTurnNarrative(
		ctx,
		agentcontext.PostTurnNarrativeConfig{
			Generator: agentcontext.NarrativeGeneratorConfig{
				Provider: e.options.Provider, Routes: e.options.Routes,
				TokenEstimator: e.options.TokenEstimator,
				Limits:         e.options.Context.NarrativeLimits,
				Timeout:        e.options.Context.NarrativeTimeout,
			},
			RetryLimit:      e.options.Context.NarrativeRetryLimit,
			SummaryMaxBytes: e.summaryBudget(),
			ManifestLimits: agentcontext.ManifestLimits{
				OwnerDeltaMaxSegments: e.options.Context.OwnerDeltaMaxSegments,
				OwnerDeltaMaxBytes:    e.options.Context.OwnerDeltaMaxBytes,
			},
			Load: func() agentcontext.NarrativeMaintenanceState {
				e.mu.Lock()
				defer e.mu.Unlock()
				return agentcontext.NarrativeMaintenanceState{
					Compaction: e.context.Compaction(),
					WindowID:   e.context.Window().ID,
					Revision:   e.sessionRevision,
					History:    cloneMessages(e.history),
				}
			},
			Store: func(compaction agentcontext.Compaction) {
				e.mu.Lock()
				e.context.SetCompaction(compaction)
				e.mu.Unlock()
			},
			Record: func(generated agentcontext.NarrativeGenerationResult) {
				e.mu.Lock()
				e.usage.Add(generated.Usage)
				e.costUSD += generated.CostUSD
				e.mu.Unlock()
			},
			Snapshot: e.ExportContextSnapshot,
			Apply: func(snapshot agentcontext.ContextSnapshot) {
				e.mu.Lock()
				e.applyContextSnapshot(snapshot)
				e.mu.Unlock()
			},
			Commit: e.options.Context.CommitRebase,
		},
		threadID,
		turnID,
		e.turn,
	)
	result := NarrativeGenerationResult{
		Artifact:      maintained.Generation.Artifact,
		Usage:         maintained.Generation.Usage,
		Provider:      maintained.Generation.Provider,
		Model:         maintained.Generation.Model,
		CostUSD:       maintained.Generation.CostUSD,
		CostKnown:     maintained.Generation.CostKnown,
		Attempt:       maintained.Attempt,
		RouteDigest:   maintained.Generation.RouteDigest,
		Fallback:      maintained.Fallback,
		FailureReason: maintained.FailureReason,
	}
	result.Receipt = narrativeMaintenanceReceipt(
		maintained.State,
		maintained.Included,
		maintained.RenderedBytes,
		maintained.Generation.Usage,
	)
	return result, err
}

func narrativeMaintenanceReceipt(
	state *agentcontext.CompactionState,
	included bool,
	bytes int,
	usage provider.Usage,
) *CompactionReceipt {
	if state == nil {
		return nil
	}
	return &CompactionReceipt{
		CompactionID: state.ID, Status: state.Phase, Mode: "post_turn",
		Phase:                 CompactionPhasePostTurn,
		SourceWindowID:        state.SourceWindowID,
		TargetWindowID:        state.TargetWindowID,
		TruthGeneration:       state.Truth.Generation,
		TruthEntities:         len(state.Truth.Entities),
		CompatibilityHash:     state.Truth.CompatibilityHash,
		AuthorityDigest:       state.NarrativeInput.AuthorityDigest,
		AuthorityEquivalent:   true,
		DownshiftPolicy:       state.Truth.DownshiftPolicy,
		NarrativeIncluded:     included,
		NarrativeBytes:        bytes,
		NarrativeInputTokens:  usage.InputTokens,
		NarrativeOutputTokens: usage.OutputTokens,
		FallbackReason:        state.FallbackReason,
	}
}

func (e *Engine) completeInlineNarrative(
	ctx context.Context,
	history *[]provider.Message,
) (*CompactionReceipt, error) {
	if e.options.Context.SemanticNarrative != "inline" {
		return nil, nil
	}
	state := e.compactionState().State
	if state == nil || state.Phase == "completed" {
		return nil, nil
	}
	scope := e.runningScope()
	if scope == nil || scope.state.kernel == nil {
		return nil, errors.New("inline compaction requires an active turn kernel")
	}
	if state.PlanDigest == "" {
		state.PlanDigest = agentcontext.FallbackCompactionPlanDigest(state)
	}
	var (
		result            NarrativeGenerationResult
		narrativeIncluded bool
	)
	if state.Phase == "rebasing" && state.Narrative != nil {
		result.Artifact = *state.Narrative
		narrativeIncluded = true
	} else if state.Phase != "fallback" && state.NarrativeInput != nil {
		state.Phase = "generating_narrative"
		state.Attempt++
		e.stageContextCompaction(state)
		narrativeEffect, err := scope.state.kernel.BeginContextEffect(
			turnkernel.EffectGenerateNarrative,
			state.ID,
			state.PlanDigest,
		)
		if err != nil {
			return nil, err
		}
		result, err = e.GenerateNarrative(
			ctx,
			state.Truth,
			*state.NarrativeInput,
			e.turn,
		)
		if resolveErr := scope.state.kernel.FinishContextEffect(
			narrativeEffect,
			err,
		); resolveErr != nil {
			return nil, errors.Join(err, resolveErr)
		}
		if err != nil {
			state.Phase = "fallback"
			state.FallbackReason = err.Error()
			result = NarrativeGenerationResult{
				Fallback: true, FailureReason: err.Error(),
			}
			e.stageContextCompaction(state)
		} else {
			if err := scope.state.kernel.RecordSupplementalUsage(
				"context_compaction",
				state.ID,
				result.Usage,
				result.CostUSD,
				result.CostKnown,
			); err != nil {
				return nil, err
			}
			scope.mu.Lock()
			scope.state.contextUsage.Add(result.Usage)
			scope.state.contextCost += result.CostUSD
			scope.mu.Unlock()
			narrativeIncluded = true
			state.Phase = "rebasing"
			state.Narrative = &result.Artifact
			e.stageContextCompaction(state)
		}
	} else if state.Phase != "fallback" {
		return nil, nil
	}
	var artifact *agentcontext.NarrativeArtifact
	if narrativeIncluded {
		artifact = &result.Artifact
	}
	completed, err := agentcontext.CompleteCompaction(
		*state,
		artifact,
		*history,
		e.summaryBudget(),
	)
	if err != nil {
		return nil, err
	}
	compaction := e.compactionState()
	compaction.State = &completed.State
	snapshot, err := e.buildContextSnapshot(
		completed.History,
		compaction,
		e.sessionRevision+1,
		max(uint64(1), e.stateEpoch),
	)
	if err != nil {
		return nil, err
	}
	threadID := state.ThreadID
	if threadID == "" {
		threadID = protocol.ThreadID(scope.spec.Identity.ThreadID)
	}
	turnID := state.TurnID
	if turnID == "" {
		turnID = protocol.TurnID(scope.spec.Identity.TurnID)
	}
	envelope, err := agentcontext.BuildRebaseEnvelope(
		agentcontext.RebaseRequest{
			Completed: completed, Snapshot: snapshot,
			ThreadID: threadID, TurnID: turnID,
			ManifestLimits: agentcontext.ManifestLimits{
				OwnerDeltaMaxSegments: e.options.Context.OwnerDeltaMaxSegments,
				OwnerDeltaMaxBytes:    e.options.Context.OwnerDeltaMaxBytes,
			},
		},
	)
	if err != nil {
		return nil, err
	}
	rebaseEffect, err := scope.state.kernel.BeginContextEffect(
		turnkernel.EffectCommitContextRebase,
		state.ID,
		state.PlanDigest,
	)
	if err != nil {
		return nil, err
	}
	if commit := e.options.Context.CommitRebaseWithFacts; commit != nil {
		if err := scope.state.kernel.FinishContextEffectWithCommit(
			ctx,
			rebaseEffect,
			nil,
			func(
				ctx context.Context,
				batch turnkernel.DomainFactBatch,
			) error {
				return commit(ctx, envelope, batch)
			},
		); err != nil {
			return nil, err
		}
	} else if commit := e.options.Context.CommitRebase; commit != nil {
		if err := commit(ctx, envelope); err != nil {
			return nil, err
		}
		if err := scope.state.kernel.FinishContextEffect(
			rebaseEffect,
			nil,
		); err != nil {
			return nil, err
		}
	} else if err := scope.state.kernel.FinishContextEffect(
		rebaseEffect, nil,
	); err != nil {
		return nil, err
	}
	*history = completed.History
	e.sessionRevision = snapshot.Revision
	e.stageContextCompaction(&completed.State)
	status := "completed"
	if completed.State.FallbackReason != "" {
		status = "fallback"
		result.Fallback = true
		result.FailureReason = completed.State.FallbackReason
	}
	return &CompactionReceipt{
		CompactionID: state.ID, Status: status, Mode: "inline",
		SourceWindowID:        state.SourceWindowID,
		TargetWindowID:        state.TargetWindowID,
		NarrativeIncluded:     narrativeIncluded,
		NarrativeBytes:        completed.RenderedBytes,
		NarrativeInputTokens:  result.Usage.InputTokens,
		NarrativeOutputTokens: result.Usage.OutputTokens,
		FallbackReason:        completed.State.FallbackReason,
		AuthorityDigest:       completed.AuthorityDigest,
		AuthorityEquivalent:   true,
	}, nil
}
