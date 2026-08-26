package engine

import (
	"context"
	"time"

	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

type ContextPolicy struct {
	Window                CompactWindowPolicy
	TruthRetention        agentcontext.RetentionPolicy
	SemanticNarrative     string
	NarrativeLimits       agentcontext.NarrativeLimits
	NarrativeTimeout      time.Duration
	NarrativeRetryLimit   int
	OwnerDeltaMaxSegments int
	OwnerDeltaMaxBytes    int
	RecentTailTurns       int
	RecentTailMaxTokens   uint64
	CommitRebase          func(
		context.Context,
		agentcontext.ContextRebaseEnvelope,
	) error
	CommitRebaseWithFacts func(
		context.Context,
		agentcontext.ContextRebaseEnvelope,
		turnkernel.DomainFactBatch,
	) error
}

func (e *Engine) contextCapacity() agentcontext.Capacity {
	if scope := e.runningScope(); scope != nil &&
		scope.spec.Limits.Context.ContextTokens != 0 {
		return scope.spec.Limits.Context
	}
	return agentcontext.ResolveCapacity(
		e.activeRoute(), e.options.MaxOutputTokens,
		e.options.Budget.MaxTurnTokens, e.options.Budget.MaxTokens,
	)
}

func (e *Engine) prepareCompactLimit() uint64 {
	prepare, _, _ := agentcontext.WindowThresholds(
		e.options.Context.Window, e.contextCapacity().HardInputTokens,
	)
	return prepare
}

func (e *Engine) emergencyCompactLimit() uint64 {
	_, _, emergency := agentcontext.WindowThresholds(
		e.options.Context.Window, e.contextCapacity().HardInputTokens,
	)
	return emergency
}

func (e *Engine) recentTailMaxTokens() uint64 {
	if configured := e.options.Context.RecentTailMaxTokens; configured != 0 {
		return configured
	}
	return e.autoCompactLimit()
}
