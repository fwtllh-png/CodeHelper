package provider

import "github.com/fwtllh-png/CodeHelper/internal/adapter/model"

func EstimateCost(pricing model.Pricing, usage Usage) float64 {
	uncached := usage.InputTokens - min(
		usage.InputTokens,
		usage.CachedTokens,
	)
	cachedPrice := pricing.InputPerMillion
	if pricing.CachedInputPerMillion != nil {
		cachedPrice = *pricing.CachedInputPerMillion
	}
	return float64(uncached)/1_000_000*pricing.InputPerMillion +
		float64(usage.CachedTokens)/1_000_000*cachedPrice +
		float64(usage.OutputTokens)/1_000_000*pricing.OutputPerMillion
}

func PricingKnown(pricing model.Pricing, usage Usage) bool {
	return pricing.Known &&
		(usage.CachedTokens == 0 || pricing.CachedInputPerMillion != nil)
}
