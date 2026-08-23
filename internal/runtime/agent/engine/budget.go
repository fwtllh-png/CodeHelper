package engine

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func (e *Engine) checkBudget(
	estimatedInput uint64,
	turnUsage provider.Usage,
	stepUsage provider.Usage,
	outputReserve uint64,
) (uint64, error) {
	route := e.activeRoute()
	return agentcontext.CheckBudget(agentcontext.BudgetRequest{
		ContextTokens:  route.Model().Limits.ContextTokens,
		EstimatedInput: estimatedInput, OutputReserve: outputReserve,
		SessionUsage: e.usage, TurnUsage: turnUsage, StepUsage: stepUsage,
		MaxTokens: e.options.Budget.MaxTokens, SpentCostUSD: e.costUSD,
		MaxCostUSD: e.options.Budget.MaxCostUSD, Pricing: route.Model().Pricing,
		Scope: e.turnBudgetScope(),
	})
}

func (e *Engine) turnBudgetScope() string {
	if scope := e.runningScope(); scope != nil &&
		scope.spec.Identity.TurnID != "" {
		return "turn:" + scope.spec.Identity.TurnID
	}
	return "turn"
}

func (e *Engine) budgetConvergence(used uint64) (provider.Message, bool) {
	limit := e.options.Budget.MaxTokens
	stage, finishOnly := agentcontext.BudgetStage(used, limit)
	if stage == 0 {
		return provider.Message{}, false
	}
	scope := e.runningScope()
	if scope == nil {
		return provider.Message{}, finishOnly
	}
	scope.mu.Lock()
	if stage <= scope.state.budgetStage {
		scope.mu.Unlock()
		return provider.Message{}, finishOnly
	}
	scope.state.budgetStage = stage
	scope.mu.Unlock()
	message := provider.TextMessage(provider.RoleUser, fmt.Sprintf(
		"[token_budget]\nused_tokens=%d\nmax_tokens=%d\nstage=%s\n"+
			"Stop broad exploration. When stage=finish_only, do not request exploratory tools: produce the concise user-facing final answer now using current evidence. Preserve required completion/verification tools and report blockers.",
		used, limit, []string{"", "converge", "finish_only"}[stage],
	))
	message.Turn = e.turn
	return message, finishOnly
}

func (e *Engine) Usage() (provider.Usage, float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage, e.costUSD
}
