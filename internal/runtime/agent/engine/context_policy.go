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

const (
	statelessPrepareTokens   = 16 << 10
	statelessCompactTokens   = 24 << 10
	statelessEmergencyTokens = 48 << 10
)

func (e *Engine) effectiveWindowPolicy() agentcontext.WindowPolicy {
	policy := e.options.Context.Window
	if e.activeRoute().Model().Capabilities.IncrementalResponses {
		return policy
	}
	limit := e.activeRoute().Model().Limits.ContextTokens
	if policy.PrepareTokens == 0 {
		policy.PrepareTokens = min(limit*55/100, statelessPrepareTokens)
	}
	if policy.AutoTokens == 0 {
		policy.AutoTokens = min(limit*65/100, statelessCompactTokens)
	}
	if policy.EmergencyTokens == 0 {
		policy.EmergencyTokens = min(limit*85/100, statelessEmergencyTokens)
	}
	return policy
}

func (e *Engine) prepareCompactLimit() uint64 {
	limit := e.activeRoute().Model().Limits.ContextTokens
	prepare, _, _ := agentcontext.WindowThresholds(e.effectiveWindowPolicy(), limit)
	return prepare
}

func (e *Engine) emergencyCompactLimit() uint64 {
	limit := e.activeRoute().Model().Limits.ContextTokens
	_, _, emergency := agentcontext.WindowThresholds(e.effectiveWindowPolicy(), limit)
	return emergency
}
