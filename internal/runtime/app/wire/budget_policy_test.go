package wire

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/config"
)

func TestEffectiveSubagentLimitsDeriveTreeFromParallelTurnCapacity(t *testing.T) {
	limits := effectiveSubagentLimits(config.Subagent{MaxParallel: 3}, 128_000)
	if limits.MaxTokens != 384_000 {
		t.Fatalf("derived child tree budget = %d, want 384000", limits.MaxTokens)
	}
	explicit := effectiveSubagentLimits(config.Subagent{
		MaxParallel: 3,
		MaxTokens:   50_000,
	}, 128_000)
	if explicit.MaxTokens != 50_000 {
		t.Fatalf("explicit child tree budget = %d, want 50000", explicit.MaxTokens)
	}
}

func TestEffectiveSubagentLimitsDoNotInventATokenBudget(t *testing.T) {
	limits := effectiveSubagentLimits(config.Subagent{MaxParallel: 3}, 0)
	if limits.MaxTokens != 0 {
		t.Fatalf("implicit child tree budget = %d, want no cumulative limit", limits.MaxTokens)
	}
}

func TestEffectiveSubagentLimitsSaturateOnOverflow(t *testing.T) {
	limits := effectiveSubagentLimits(config.Subagent{MaxParallel: 2}, ^uint64(0))
	if limits.MaxTokens != ^uint64(0) {
		t.Fatalf("overflowing child tree budget = %d, want saturation", limits.MaxTokens)
	}
}
