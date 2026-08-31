package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
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
	ModelMetadata protocol.ModelMetadataProvenance
	CostUSD       float64
	CostKnown     bool
	Attempt       uint32
	RouteDigest   string
	Fallback      bool
	FailureReason string
	Receipt       *CompactionReceipt
}

type compactionCompletionCheck struct {
	sourceContextDigest string
	sourceActive        uint64
	sourceTotal         uint64
	hardLimit           uint64
	input               agentcontext.MessageSnapshot
	outputReserve       uint64
	economicInput       uint64
	projectHistory      agentcontext.HistoryProjector
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
	if e.options.Context.SemanticNarrative == "off" {
		routeFailure = "semantic_narrative_disabled"
	}
	currentWindow := e.currentWindowLedger()
	targetWindow, windowErr := agentcontext.CreateWindowLedger(
		currentWindow.Number + 1,
	)
	if windowErr != nil {
		targetWindow = agentcontext.FallbackWindowLedger(
			currentWindow,
			e.options.SessionID,
		)
	}
	state := agentcontext.PrepareCompactionState(
		agentcontext.CompactionPreparation{
			Candidate: candidate, Previous: e.compactionState().State,
			ThreadID: threadID, TurnID: turnID,
			TargetWindowID:     targetWindow.ID,
			TargetWindow:       targetWindow,
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
		ModelMetadata: result.ModelMetadata,
		CostUSD:       result.CostUSD, CostKnown: result.CostKnown,
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
	e.mu.Lock()
	defer e.mu.Unlock()
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
				return agentcontext.NarrativeMaintenanceState{
					Compaction: e.context.Compaction(),
					WindowID:   e.context.Window().ID,
					Revision:   e.sessionRevision,
					History:    cloneMessages(e.history),
				}
			},
			Store: func(compaction agentcontext.Compaction) {
				e.context.SetCompaction(compaction)
			},
			Record: func(generated agentcontext.NarrativeGenerationResult) {
				e.usage.Add(generated.Usage)
				e.costUSD += generated.CostUSD
			},
			Snapshot: func() (agentcontext.ContextSnapshot, error) {
				return e.buildContextSnapshot(
					e.history,
					e.context.Compaction(),
					max(uint64(1), e.sessionRevision),
					max(uint64(1), e.stateEpoch),
				)
			},
			Apply: func(snapshot agentcontext.ContextSnapshot) {
				e.applyContextSnapshot(snapshot)
			},
			Validate: func(
				source []provider.Message,
				candidate []provider.Message,
			) error {
				stable := e.promptMessages()
				sourceWindow, err := e.measureTokenWindow(
					agentcontext.NewMessageLedger(
						agentcontext.LedgerInput{
							Stable: stable, History: source,
						},
					).Snapshot(),
					0,
					0,
				)
				if err != nil {
					return err
				}
				candidateWindow, err := e.measureTokenWindow(
					agentcontext.NewMessageLedger(
						agentcontext.LedgerInput{
							Stable: stable, History: candidate,
						},
					).Snapshot(),
					0,
					0,
				)
				if err != nil {
					return err
				}
				if candidateWindow.active >= sourceWindow.active {
					return errors.New(
						"context compaction did not reduce provider-visible tokens",
					)
				}
				return nil
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
		ModelMetadata: maintained.Generation.ModelMetadata,
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
	if result.Receipt != nil && result.Provider != "" {
		metadata := result.ModelMetadata
		result.Receipt.NarrativeProvider = result.Provider
		result.Receipt.NarrativeModel = result.Model
		result.Receipt.NarrativeMetadata = &metadata
	}
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
	send func(State, Event) error,
	checks ...compactionCompletionCheck,
) (*CompactionReceipt, error) {
	if e.options.Context.SemanticNarrative == "post_turn" {
		return nil, nil
	}
	compaction := e.compactionState()
	state := compaction.State
	if state == nil || state.Phase == "completed" {
		return nil, nil
	}
	if state.SourceWindowID != e.currentWindowLedger().ID {
		compaction.State = nil
		e.contextAuthority().SetCompaction(compaction)
		return nil, nil
	}
	if state.Plan == nil {
		return nil, fmt.Errorf(
			"prepared compaction plan is unavailable: %s",
			state.FallbackReason,
		)
	}
	if err := agentcontext.ValidateCompactionSource(
		*state.Plan,
		*history,
	); err != nil {
		return nil, err
	}
	var check compactionCompletionCheck
	if len(checks) != 0 {
		check = checks[0]
	}
	if check.sourceContextDigest != "" &&
		check.sourceContextDigest != state.SourceContextDigest {
		return nil, errors.New("compaction source context digest is stale")
	}
	currentCandidate, err := e.buildCompactionCandidate(
		*history,
		state.Plan.Cut,
		true,
	)
	if err != nil {
		return nil, err
	}
	currentAuthorityDigest, err := currentCandidate.Authority.AuthorityDigest()
	if err != nil ||
		currentAuthorityDigest != state.Plan.DeterministicResult.AuthorityDigest {
		return nil, errors.New("compaction authority digest is stale")
	}
	stableDigest, err := agentcontext.NewMessageLedger(
		agentcontext.LedgerInput{Stable: e.promptMessages()},
	).Snapshot().Digest()
	if err != nil {
		return nil, err
	}
	if stableDigest != state.Plan.DeterministicResult.StablePrefixDigest {
		return nil, errors.New("compaction stable prefix digest is stale")
	}
	scope := e.runningScope()
	if scope == nil || scope.state.kernel == nil {
		return nil, nil
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
			state.Phase, state.FallbackReason = "fallback", err.Error()
			result = NarrativeGenerationResult{Fallback: true, FailureReason: err.Error()}
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
			if send != nil {
				usage := result.Usage
				if err := send(Streaming, Event{
					Usage:         &usage,
					CostUSD:       result.CostUSD,
					CostKnown:     result.CostKnown,
					Sample:        e.nextSample(),
					Provider:      result.Provider,
					Model:         result.Model,
					ModelMetadata: &result.ModelMetadata,
					Purpose:       string(model.PurposeSummary),
				}); err != nil {
					return nil, err
				}
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
	if artifact == nil &&
		e.options.Context.SemanticNarrative != "off" &&
		check.hardLimit != 0 &&
		check.sourceTotal <= check.hardLimit {
		state.Phase = "prepared"
		e.stageContextCompaction(state)
		return nil, nil
	}
	if artifact == nil && len(state.Plan.RequiredKinds) != 0 {
		return nil, protocol.NewProblem(
			protocol.CodeResourceExhausted,
			"context maintenance cannot preserve required continuation facts",
			false,
			nil,
		)
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
	if check.sourceActive != 0 {
		candidateInput := check.input.WithHistory(
			agentcontext.ProjectHistory(
				completed.History,
				check.projectHistory,
			),
		)
		candidateWindow, measureErr := e.measureTokenWindow(
			candidateInput,
			check.outputReserve,
			check.economicInput,
		)
		if measureErr != nil {
			return nil, measureErr
		}
		if candidateWindow.active >= check.sourceActive {
			return nil, errors.New(
				"context compaction did not reduce provider-visible tokens",
			)
		}
	}
	compaction = e.compactionState()
	compaction.Count++
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
	snapshot.Window = state.Plan.TargetWindow
	if err := snapshot.Seal(); err != nil {
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
	e.applyContextSnapshot(snapshot)
	scope.mu.Lock()
	scope.state.context = e.context.Clone()
	if scope.state.contextLedger != nil {
		scope.state.contextLedger.ReplaceHistory(completed.History)
	}
	scope.mu.Unlock()
	*history = cloneMessages(completed.History)
	e.options.Metrics.Compaction(
		state.Plan.SourceBytes -
			agentcontext.HistoryBytes(completed.History),
	)
	status := "completed"
	if completed.State.FallbackReason != "" {
		status = "fallback"
		result.Fallback = true
		result.FailureReason = completed.State.FallbackReason
	}
	receipt := &CompactionReceipt{
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
	}
	if result.Provider != "" {
		metadata := result.ModelMetadata
		receipt.NarrativeProvider = result.Provider
		receipt.NarrativeModel = result.Model
		receipt.NarrativeMetadata = &metadata
	}
	return receipt, nil
}
