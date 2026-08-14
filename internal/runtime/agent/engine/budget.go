package engine

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func estimateMessageTokens(messages []provider.Message) uint64 {
	characters := 0
	for _, message := range messages {
		for _, block := range message.Blocks {
			characters += len([]rune(block.Text))
			if block.ToolCall != nil {
				characters += len([]rune(block.ToolCall.Name + block.ToolCall.Arguments))
			}
			if block.ToolResult != nil {
				characters += len([]rune(block.ToolResult.Content))
			}
			if block.Type == provider.ContentImage && block.Attachment != nil {
				characters += int(estimateImageTokens(*block.Attachment) * 4)
			}
		}
	}
	return uint64(max(1, (characters+3)/4))
}

func estimateImageTokens(attachment provider.Attachment) uint64 {
	config, _, err := image.DecodeConfig(bytes.NewReader(attachment.Data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		tiles := max(1, (len(attachment.Data)+(512<<10)-1)/(512<<10))
		return 85 + uint64(170*tiles)
	}
	width, height := config.Width, config.Height
	if longest := max(width, height); longest > 2048 {
		width = max(1, width*2048/longest)
		height = max(1, height*2048/longest)
	}
	if shortest := min(width, height); shortest > 768 {
		width = max(1, width*768/shortest)
		height = max(1, height*768/shortest)
	}
	tiles := ((width + 511) / 512) * ((height + 511) / 512)
	return 85 + uint64(170*tiles)
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
	estimatedInput uint64,
	turnUsage provider.Usage,
	stepUsage provider.Usage,
	outputReserve uint64,
) (uint64, error) {
	route := e.activeRoute()
	if estimatedInput+outputReserve > route.Model().Limits.ContextTokens {
		return estimatedInput, protocol.NewProblem(
			protocol.CodeResourceExhausted, "context window exceeded", false, nil,
		)
	}
	projectedTokens := e.usage.Total() + turnUsage.Total() + stepUsage.Total() +
		estimatedInput + outputReserve
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
			InputTokens: estimatedInput, OutputTokens: outputReserve,
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

func (e *Engine) budgetConvergence(used uint64) (provider.Message, bool) {
	limit := e.options.Budget.MaxTokens
	if limit == 0 {
		limit = e.activeRoute().Model().Limits.ContextTokens
	}
	if limit == 0 || used < limit*70/100 {
		return provider.Message{}, false
	}
	stage := uint8(1)
	if used >= limit*85/100 {
		stage = 2
	}
	scope := e.runningScope()
	if scope == nil {
		return provider.Message{}, stage == 2
	}
	scope.mu.Lock()
	if stage <= scope.state.budgetStage {
		scope.mu.Unlock()
		return provider.Message{}, stage == 2
	}
	scope.state.budgetStage = stage
	scope.mu.Unlock()
	message := provider.TextMessage(provider.RoleUser, fmt.Sprintf(
		"[token_budget]\nused_tokens=%d\nmax_tokens=%d\nstage=%s\n"+
			"Stop broad exploration. When stage=finish_only, do not request exploratory tools: produce the concise user-facing final answer now using current evidence. Preserve required completion/verification tools and report blockers.",
		used, limit, []string{"", "converge", "finish_only"}[stage],
	))
	message.Turn = e.turn
	return message, stage == 2
}

func (e *Engine) Usage() (provider.Usage, float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage, e.costUSD
}
