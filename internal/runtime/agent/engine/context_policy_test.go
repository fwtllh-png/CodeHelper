package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func TestContextWindowThresholdsDerivePrepareFromExplicitCompactLimit(t *testing.T) {
	prepare, compact, emergency := agentcontext.WindowThresholds(
		CompactWindowPolicy{AutoTokens: 512},
		3072,
	)
	if prepare != 512 || compact != 512 || emergency != 3072 {
		t.Fatalf(
			"thresholds = (%d, %d, %d), want (512, 512, 3072)",
			prepare,
			compact,
			emergency,
		)
	}
}

func TestDefaultCompactionThresholdsUseHardInputCapacity(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.MaxOutputTokens = 0
	for _, contextTokens := range []uint64{4096, 64 << 10, 128 << 10, 1 << 20} {
		outputTokens := max(uint64(1), contextTokens/4)
		catalog, err := model.NewCatalog(model.Provider{
			ID: "dynamic-window", Adapter: model.AdapterOpenAICompatible,
			Endpoint:   "http://127.0.0.1:1",
			Protocol:   model.ProtocolOpenAIResponses,
			Provenance: model.ProvenanceFixture,
			Models: map[string]model.Model{"model": {
				ID: "model", CanonicalID: "model", WireID: "model",
				Limits: model.Limits{
					ContextTokens:   contextTokens,
					MaxOutputTokens: outputTokens,
				},
				Capabilities: model.Capabilities{
					Streaming: true, Reasoning: true, ToolCalls: true,
				},
				Provenance: model.ProvenanceFixture,
			}},
		})
		if err != nil {
			t.Fatal(err)
		}
		resolver, err := model.NewResolver(catalog)
		if err != nil {
			t.Fatal(err)
		}
		route, err := resolver.Resolve(model.RouteRequest{
			ProviderID: "dynamic-window",
			ModelID:    "model",
		})
		if err != nil {
			t.Fatal(err)
		}
		engine.options.Route = route
		engine.options.Routes, _ = model.NewRouteSet(route, nil, false)

		prepare, compact, emergency := agentcontext.WindowThresholds(
			engine.options.Context.Window,
			engine.contextCapacity().HardInputTokens,
		)
		want := contextTokens - outputTokens
		if prepare != want || compact != want || emergency != want {
			t.Fatalf(
				"context=%d thresholds=(%d,%d,%d), want hard input %d",
				contextTokens,
				prepare,
				compact,
				emergency,
				want,
			)
		}
	}
}

func TestContextCapacityHonorsOperatorAndTokenBudgetCeilings(t *testing.T) {
	route := reasoningRoute(t)
	capacity := agentcontext.ResolveCapacity(route, 12_000, 0, 0)
	if capacity.OutputCeiling != 12_000 ||
		capacity.HardInputTokens != capacity.ContextTokens-12_000 ||
		capacity.OutputSource != "operator_config" {
		t.Fatalf("configured capacity = %+v", capacity)
	}
	capacity = agentcontext.ResolveCapacity(route, 0, 8_000, 0)
	if capacity.OutputCeiling != 8_000 ||
		capacity.HardInputTokens != capacity.ContextTokens-8_000 ||
		capacity.OutputSource != "operator_token_budget" {
		t.Fatalf("budget capacity = %+v", capacity)
	}
}

func TestRecentTailDefaultTracksDynamicCompactionLimit(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Route = reasoningRoute(t)
	engine.options.Routes, _ = model.NewRouteSet(engine.options.Route, nil, false)

	if got, want := engine.recentTailMaxTokens(), engine.autoCompactLimit(); got != want {
		t.Fatalf("recent tail limit = %d, want dynamic compact limit %d", got, want)
	}

	engine.options.Context.RecentTailMaxTokens = 12345
	if got := engine.recentTailMaxTokens(); got != 12345 {
		t.Fatalf("explicit recent tail limit = %d, want 12345", got)
	}
}

func TestSummaryBudgetUsesHardInputCapacityUnlessConfigured(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	if got, want := engine.summaryBudget(),
		int(engine.contextCapacity().HardInputTokens*4); got != want {
		t.Fatalf("summary budget = %d, want %d", got, want)
	}
	engine.options.SummaryMaxBytes = 4096
	if got := engine.summaryBudget(); got != 4096 {
		t.Fatalf("configured summary budget = %d", got)
	}
}
