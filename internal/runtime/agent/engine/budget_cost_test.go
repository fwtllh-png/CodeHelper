package engine

import (
	"errors"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestEstimateCostUsesCachedInputPrice(t *testing.T) {
	cachedPrice := 0.5
	pricing := model.Pricing{
		Known: true, InputPerMillion: 2, CachedInputPerMillion: &cachedPrice,
		OutputPerMillion: 8,
	}
	usage := provider.Usage{InputTokens: 1000, CachedTokens: 400, OutputTokens: 100}
	if got, want := estimateCost(pricing, usage), 0.0022; got != want {
		t.Fatalf("cost=%f want %f", got, want)
	}
	if !pricingKnown(pricing, usage) {
		t.Fatal("explicit cached pricing reported unknown")
	}
	pricing.CachedInputPerMillion = nil
	if pricingKnown(pricing, usage) {
		t.Fatal("cached usage without cached pricing reported known")
	}
}

func TestCheckBudgetFitsAvailableOutputCapacity(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Budget.MaxTokens = 500
	got, err := engine.checkBudget(
		200,
		provider.Usage{InputTokens: 100},
		provider.Usage{},
		1_000,
	)
	if err != nil || got != 200 {
		t.Fatalf("output capacity = %d, %v", got, err)
	}
}

func TestCheckBudgetUsesSharedResumableExhaustion(t *testing.T) {
	testCases := []struct {
		name   string
		budget Budget
		reason string
	}{
		{
			name:   "tokens",
			budget: Budget{MaxTokens: 100},
			reason: protocol.ProblemReasonTokenBudgetExhausted,
		},
		{
			name:   "cost",
			budget: Budget{MaxCostUSD: 0.000001},
			reason: protocol.ProblemReasonCostBudgetExhausted,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			engine := newEngine(t, &scriptedProvider{}, nil)
			engine.options.Budget = testCase.budget
			_, err := engine.checkBudget(
				200,
				provider.Usage{},
				provider.Usage{},
				128,
			)
			var problem *protocol.Problem
			if !errors.As(err, &problem) ||
				problem.Code != protocol.CodeResourceExhausted ||
				problem.Retryable ||
				problem.Fault == nil ||
				problem.Fault.Disposition != protocol.FaultResumeTurn ||
				problem.Details == nil ||
				problem.Details.Reason != testCase.reason {
				t.Fatalf("budget error = %+v", problem)
			}
		})
	}
}
