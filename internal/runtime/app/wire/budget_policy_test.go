package wire

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/config"
)

func TestEffectiveTurnTokenBudgetUsesModelContextUnlessConfigured(t *testing.T) {
	if got := effectiveTurnTokenBudget(0, 128_000); got != 128_000 {
		t.Fatalf("derived turn budget = %d, want model context window", got)
	}
	if got := effectiveTurnTokenBudget(32_000, 128_000); got != 32_000 {
		t.Fatalf("configured turn budget = %d, want 32000", got)
	}
}

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

func TestEffectiveSubagentLimitsSaturateOnOverflow(t *testing.T) {
	limits := effectiveSubagentLimits(config.Subagent{MaxParallel: 2}, ^uint64(0))
	if limits.MaxTokens != ^uint64(0) {
		t.Fatalf("overflowing child tree budget = %d, want saturation", limits.MaxTokens)
	}
}
