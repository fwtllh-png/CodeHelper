package engine

import (
	"fmt"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func estimateMessageTokens(messages []provider.Message) uint64 {
	characters := 0
	for _, message := range messages {
		for _, block := range message.Blocks {
			characters += len([]rune(block.Text))
			characters += len([]rune(block.Signature))
			if block.ToolCall != nil {
				characters += len([]rune(block.ToolCall.Name + block.ToolCall.Arguments))
			}
			if block.ToolResult != nil {
				characters += len([]rune(block.ToolResult.Content))
			}
		}
	}
	return uint64(max(1, (characters+3)/4))
}

func estimateCost(pricing model.Pricing, usage provider.Usage) float64 {
	uncached := usage.InputTokens - min(usage.InputTokens, usage.CachedTokens)
	cachedPrice := pricing.InputPerMillion
	if pricing.CachedInputPerMillion != nil {
		cachedPrice = *pricing.CachedInputPerMillion
	}
	return float64(uncached)/1_000_000*pricing.InputPerMillion +
		float64(usage.CachedTokens)/1_000_000*cachedPrice +
		float64(usage.OutputTokens)/1_000_000*pricing.OutputPerMillion
}

func pricingKnown(pricing model.Pricing, usage provider.Usage) bool {
	return pricing.Known && (usage.CachedTokens == 0 || pricing.CachedInputPerMillion != nil)
}

func (e *Engine) checkBudget(
	messages []provider.Message,
	turnUsage provider.Usage,
	stepUsage provider.Usage,
) (uint64, error) {
	estimatedInput, err := e.options.TokenEstimator.Estimate(messages)
	if err != nil {
		return 0, protocol.NewProblem(protocol.CodeInternal, "estimate input tokens", false, err)
	}
	route := e.activeRoute()
	if estimatedInput+e.maxOutputFor(route) > route.Model().Limits.ContextTokens {
		return estimatedInput, protocol.NewProblem(
			protocol.CodeResourceExhausted, "context window exceeded", false, nil,
		)
	}
	projectedTokens := e.usage.Total() + turnUsage.Total() + stepUsage.Total() +
		estimatedInput + e.options.MaxOutputTokens
	if limit := e.options.Budget.MaxTokens; limit > 0 && projectedTokens > limit {
		return estimatedInput, protocol.NewProblem(
			protocol.CodeResourceExhausted,
			fmt.Sprintf("token budget exceeded: projected %d, limit %d", projectedTokens, limit),
			false,
			nil,
		)
	}
	if limit := e.options.Budget.MaxCostUSD; limit > 0 {
		pricing := route.Model().Pricing
		if !pricing.Known {
			return estimatedInput, protocol.NewProblem(
				protocol.CodeInvalidArgument, "cost budget requires known model pricing", false, nil,
			)
		}
		projectedUsage := turnUsage
		projectedUsage.Add(stepUsage)
		projectedUsage.Add(provider.Usage{
			InputTokens: estimatedInput, OutputTokens: e.options.MaxOutputTokens,
		})
		if projected := e.costUSD + estimateCost(pricing, projectedUsage); projected > limit {
			return estimatedInput, protocol.NewProblem(
				protocol.CodeResourceExhausted,
				fmt.Sprintf("cost budget exceeded: projected %.6f, limit %.6f", projected, limit),
				false,
				nil,
			)
		}
	}
	return estimatedInput, nil
}

func (e *Engine) maybeInjectBudgetReminder(messages *[]provider.Message) {
	limit := e.options.Budget.MaxTokens
	if limit == 0 || e.budgetReminderDelivered || messages == nil {
		return
	}
	threshold := e.options.BudgetReminderThreshold
	if threshold == 0 {
		threshold = limit / 10
		if threshold < 256 {
			threshold = 256
		}
		if threshold > limit {
			threshold = limit
		}
	}
	used := e.usage.Total()
	if used >= limit {
		return
	}
	remaining := limit - used
	if remaining > threshold {
		return
	}
	e.budgetReminderDelivered = true
	text := fmt.Sprintf(
		"[budget reminder] Approximately %d tokens remaining of session budget %d. "+
			"Prefer wrapping up or asking the user before starting large work.",
		remaining, limit,
	)
	*messages = append(*messages, provider.TextMessage(provider.RoleUser, text))
}

func (e *Engine) resetBudgetReminder() {
	e.budgetReminderDelivered = false
}

func (e *Engine) Usage() (provider.Usage, float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage, e.costUSD
}
