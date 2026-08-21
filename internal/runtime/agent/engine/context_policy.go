package engine

import (
	"context"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

type ContextPolicy struct {
	Window                CompactWindowPolicy
	TruthRetention        compact.RetentionPolicy
	SemanticNarrative     string
	NarrativeLimits       compact.NarrativeLimits
	NarrativeTimeout      time.Duration
	NarrativeRetryLimit   int
	OwnerDeltaMaxSegments int
	OwnerDeltaMaxBytes    int
	RecentTailTurns       int
	RecentTailMaxTokens   uint64
	CommitRebase          func(
		context.Context,
		sessiondelta.ContextRebaseEnvelope,
	) error
	CommitRebaseWithFacts func(
		context.Context,
		sessiondelta.ContextRebaseEnvelope,
		turnkernel.DomainFactBatch,
	) error
}

func (e *Engine) prepareCompactLimit() uint64 {
	limit := e.activeRoute().Model().Limits.ContextTokens
	prepare, _, _ := contextWindowThresholds(e.options.Context.Window, limit)
	return prepare
}

func (e *Engine) emergencyCompactLimit() uint64 {
	limit := e.activeRoute().Model().Limits.ContextTokens
	_, _, emergency := contextWindowThresholds(e.options.Context.Window, limit)
	return emergency
}

func contextWindowThresholds(
	policy CompactWindowPolicy,
	limit uint64,
) (uint64, uint64, uint64) {
	compact := policy.AutoTokens
	if compact == 0 {
		compact = limit * 65 / 100
	}
	prepare := policy.PrepareTokens
	if prepare == 0 {
		prepare = min(limit*55/100, compact*55/65)
	}
	emergency := policy.EmergencyTokens
	if emergency == 0 {
		emergency = max(limit*85/100, compact+1)
	}
	return min(prepare, limit), min(compact, limit), min(emergency, limit)
}
