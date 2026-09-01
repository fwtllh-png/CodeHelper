package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func TestContextWindowThresholdsDerivePrepareFromExplicitCompactLimit(t *testing.T) {
	prepare, compact, emergency := agentcontext.WindowThresholds(
		CompactWindowPolicy{AutoTokens: 512},
		3072,
	)
	if prepare != 512 || compact != 512 || emergency != 2764 {
		t.Fatalf(
			"thresholds = (%d, %d, %d), want (512, 512, 2764)",
			prepare,
			compact,
			emergency,
		)
	}
}

func TestDefaultCompactionThresholdsUseHardInputCapacityPercentages(t *testing.T) {
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
		hardInput := contextTokens - outputTokens
		wantPrepare := hardInput * agentcontext.DefaultPreparePercent / 100
		wantCompact := hardInput * agentcontext.DefaultCompactPercent / 100
		wantEmergency := hardInput * agentcontext.DefaultEmergencyPercent / 100
		if prepare != wantPrepare || compact != wantCompact ||
			emergency != wantEmergency {
			t.Fatalf(
				"context=%d thresholds=(%d,%d,%d), want (%d,%d,%d)",
				contextTokens,
				prepare,
				compact,
				emergency,
				wantPrepare,
				wantCompact,
				wantEmergency,
			)
		}
	}
}

func TestCompactionThresholdsPreserveExplicitOverrides(t *testing.T) {
	prepare, compact, emergency := agentcontext.WindowThresholds(
		CompactWindowPolicy{
			PrepareTokens:   600,
			AutoTokens:      700,
			EmergencyTokens: 900,
		},
		1000,
	)
	if prepare != 600 || compact != 700 || emergency != 900 {
		t.Fatalf(
			"thresholds = (%d,%d,%d), want explicit (600,700,900)",
			prepare,
			compact,
			emergency,
		)
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

func TestWriteReservationCountTracksDeclaredResources(t *testing.T) {
	if got := writeReservationCount(tool.Descriptor{}, `{}`); got != 1 {
		t.Fatalf("unscoped write reservation count = %d, want one call", got)
	}
	descriptor := tool.Descriptor{
		ResourceResolver: tool.ResourceResolver{
			Templates: []tool.ResourceTemplate{{}, {}, {}},
		},
	}
	if got := writeReservationCount(descriptor, `{}`); got != len(descriptor.ResourceResolver.Templates) {
		t.Fatalf(
			"write reservation count = %d, want declared resources %d",
			got,
			len(descriptor.ResourceResolver.Templates),
		)
	}
	descriptor.ResourceResolver = tool.ResourceResolver{ChangesField: "changes"}
	arguments := `{"changes":[{"path":"a.go"},{"path":"b.go","to":"c.go"}]}`
	if got := writeReservationCount(descriptor, arguments); got != 3 {
		t.Fatalf("transaction reservation count = %d, want 3 paths", got)
	}
	descriptor.ResourceResolver = tool.ResourceResolver{PatchField: "patch"}
	arguments = `{"patch":"--- a/a.go\n+++ b/a.go\n--- /dev/null\n+++ b/new.go\n"}`
	if got := writeReservationCount(descriptor, arguments); got != 2 {
		t.Fatalf("patch reservation count = %d, want 2 paths", got)
	}
}
