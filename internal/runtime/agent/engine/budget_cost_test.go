package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
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
