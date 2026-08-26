package wire

import "github.com/fwtllh-png/CodeHelper/internal/config"

func effectiveSubagentLimits(
	limits config.Subagent,
	turnBudget uint64,
) config.Subagent {
	if limits.MaxTokens != 0 || turnBudget == 0 {
		return limits
	}
	parallel := uint64(max(1, limits.MaxParallel))
	if turnBudget > ^uint64(0)/parallel {
		limits.MaxTokens = ^uint64(0)
		return limits
	}
	limits.MaxTokens = turnBudget * parallel
	return limits
}
