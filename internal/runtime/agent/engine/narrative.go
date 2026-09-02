package engine

import (
	"context"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
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

func (e *Engine) SummaryRouteDigest() (string, error) {
	return agentcontext.SummaryRouteDigest(e.options.Routes)
}

// GenerateNarrative runs a tool-free Context maintenance sample.
func (e *Engine) GenerateNarrative(
	ctx context.Context,
	truth agentcontext.TruthCapsule,
	input agentcontext.NarrativeInputArtifact,
	createdTurn uint64,
	focus string,
) (NarrativeGenerationResult, error) {
	if e.options.Context.SemanticNarrative == "off" {
		return NarrativeGenerationResult{
			Fallback: true, FailureReason: "disabled",
		}, nil
	}
	timeout := e.options.Context.NarrativeTimeout
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var rateLimitRetries uint32
	var rateLimitWaited time.Duration
	var retries uint32
	var lastErr error
	for {
		remaining := timeout
		if deadline, ok := callCtx.Deadline(); ok {
			remaining = time.Until(deadline)
		}
		if remaining <= 0 {
			if lastErr != nil {
				return NarrativeGenerationResult{}, lastErr
			}
			return NarrativeGenerationResult{}, callCtx.Err()
		}
		result, err := agentcontext.GenerateNarrative(
			callCtx,
			agentcontext.NarrativeGeneratorConfig{
				Provider: e.options.Provider, Routes: e.options.Routes,
				TokenEstimator: e.options.TokenEstimator,
				Limits:         e.options.Context.NarrativeLimits,
				Timeout:        remaining,
				Focus:          focus,
			},
			truth,
			input,
			createdTurn,
		)
		if err == nil {
			return NarrativeGenerationResult{
				Artifact: result.Artifact, Usage: result.Usage,
				Provider: result.Provider, Model: result.Model,
				ModelMetadata: result.ModelMetadata,
				CostUSD:       result.CostUSD, CostKnown: result.CostKnown,
				RouteDigest: result.RouteDigest,
				Attempt:     rateLimitRetries + retries + 1,
			}, nil
		}
		lastErr = err
		if callCtx.Err() != nil {
			return NarrativeGenerationResult{}, err
		}
		retry, retryable := providerwire.RetryPolicy{
			MaxRetries:          e.narrativeTransientRetryLimit(),
			MaxDelay:            e.options.MaxRetryDelay,
			RateLimitMaxRetries: e.options.RateLimitMaxRetries,
			RateLimitMaxWait:    e.narrativeRateLimitWait(timeout),
			RateLimitRetries:    rateLimitRetries,
			RateLimitWaited:     rateLimitWaited,
			RouteCooldown:       e.summaryRouteCooldown(),
			Now:                 e.options.Observability.Now,
		}.Decide(err, false, retries, false)
		if !retryable {
			return NarrativeGenerationResult{}, err
		}
		if waitErr := waitRetryDelay(callCtx, retry.EffectiveDelay); waitErr != nil {
			return NarrativeGenerationResult{}, err
		}
		if retry.Failure.Code == provider.FailureRateLimit {
			rateLimitRetries++
			rateLimitWaited += retry.EffectiveDelay
			continue
		}
		retries++
	}
}

func (e *Engine) narrativeTransientRetryLimit() int {
	limit := e.options.MaxRetries
	if narrative := e.options.Context.NarrativeRetryLimit; narrative > 0 {
		if limit <= 0 || narrative < limit {
			return narrative
		}
	}
	return limit
}

func (e *Engine) narrativeRateLimitWait(timeout time.Duration) time.Duration {
	wait := e.options.RateLimitMaxWait
	if wait <= 0 || wait > timeout {
		return timeout
	}
	return wait
}

func (e *Engine) summaryRouteCooldown() time.Duration {
	route, err := e.options.Routes.For(model.PurposeSummary)
	if err != nil {
		return 0
	}
	return e.routeCooldown(route)
}

func (e *Engine) RunPostTurnNarrative(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
) (NarrativeGenerationResult, error) {
	return e.generatePostTurnDigest(ctx, threadID, turnID, "", nil)
}

func (e *Engine) generatePostTurnDigest(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	focus string,
	source []provider.Message,
) (NarrativeGenerationResult, error) {
	if status, ok := e.closedTurnSealStatus(); ok &&
		(status == agentcontext.CheckpointCanceled ||
			status == agentcontext.CheckpointFailed) {
		return NarrativeGenerationResult{}, nil
	}
	result, err := e.attemptPostTurnDigest(ctx, threadID, turnID, focus, source)
	var artifact *agentcontext.NarrativeArtifact
	if !result.Fallback && len(result.Artifact.Body.Items) > 0 {
		copy := result.Artifact
		artifact = &copy
	}
	e.sealClosedTurnMemory(
		agentcontext.CheckpointCompleted,
		artifact,
		result.FailureReason,
	)
	return result, err
}

func (e *Engine) attemptPostTurnDigest(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	focus string,
	source []provider.Message,
) (NarrativeGenerationResult, error) {
	if e.options.Context.SemanticNarrative != "post_turn" {
		return NarrativeGenerationResult{
			Fallback: true, FailureReason: "disabled",
		}, nil
	}
	e.mu.Lock()
	if source == nil {
		source = cloneMessages(e.history)
	}
	omitted := agentcontext.OmittedHistory(
		source,
		e.options.Context.RecentTailTurns,
	)
	truth := e.buildTruthCapsule(e.buildCompactSummary(nil), nil)
	windowID := e.context.Window().ID
	createdTurn := e.turn
	e.mu.Unlock()
	if len(omitted) == 0 {
		return NarrativeGenerationResult{
			Fallback: true, FailureReason: "no_pending_input",
		}, nil
	}
	authority, err := truth.AuthorityDigest()
	if err != nil {
		return narrativeFallback(err.Error()), nil
	}
	routeDigest, err := e.SummaryRouteDigest()
	if err != nil {
		return narrativeFallback(err.Error()), nil
	}
	input, err := agentcontext.BuildNarrativeInput(
		threadID,
		windowID,
		authority,
		routeDigest,
		omitted,
		e.options.Context.NarrativeLimits,
		time.Now().UTC(),
		time.Hour,
	)
	if err != nil {
		return narrativeFallback(err.Error()), nil
	}
	generated, err := e.GenerateNarrative(
		ctx, truth, input, createdTurn, focus,
	)
	if err != nil {
		return narrativeFallback(err.Error()), nil
	}
	e.mu.Lock()
	compaction := e.context.Compaction()
	artifact := generated.Artifact
	compaction.Digest = &artifact
	e.context.SetCompaction(compaction)
	if generated.Usage.Total() != 0 {
		e.usage.Add(generated.Usage)
		e.costUSD += generated.CostUSD
	}
	e.mu.Unlock()
	generated.Receipt = digestReceipt(
		&artifact,
		generated.Usage,
		false,
		"",
	)
	if generated.Receipt != nil && generated.Provider != "" {
		metadata := generated.ModelMetadata
		generated.Receipt.NarrativeProvider = generated.Provider
		generated.Receipt.NarrativeModel = generated.Model
		generated.Receipt.NarrativeMetadata = &metadata
	}
	return generated, nil
}

func narrativeFallback(reason string) NarrativeGenerationResult {
	return NarrativeGenerationResult{
		Fallback: true, FailureReason: reason,
		Receipt: &CompactionReceipt{
			Status:         "fallback",
			Mode:           "post_turn",
			Phase:          CompactionPhasePostTurn,
			FallbackReason: reason,
		},
	}
}

func digestReceipt(
	artifact *agentcontext.NarrativeArtifact,
	usage provider.Usage,
	fallback bool,
	reason string,
) *CompactionReceipt {
	if artifact == nil {
		return nil
	}
	status := "completed"
	if fallback {
		status = "fallback"
	}
	bytes := 0
	for _, item := range artifact.Body.Items {
		bytes += len(item.Text)
	}
	return &CompactionReceipt{
		CompactionID:          artifact.Digest,
		Status:                status,
		Mode:                  "post_turn",
		Phase:                 CompactionPhasePostTurn,
		SourceWindowID:        artifact.WindowID,
		TargetWindowID:        artifact.WindowID,
		AuthorityDigest:       artifact.AuthorityDigest,
		AuthorityEquivalent:   true,
		NarrativeIncluded:     !fallback && len(artifact.Body.Items) > 0,
		NarrativeBytes:        bytes,
		NarrativeInputTokens:  usage.InputTokens,
		NarrativeOutputTokens: usage.OutputTokens,
		FallbackReason:        reason,
	}
}
