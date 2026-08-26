package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestAdaptiveReasoningStartsFromIntentAndEscalatesAfterFailure(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	scope := &Scope{
		engine: engine,
		spec: TurnSpec{
			Route: reasoningRoute(t),
			Request: TurnRequest{
				Prompt: "Fix the local parser",
				Intent: protocol.TurnIntentAnswer,
			},
		},
		state: newScopeState(engine),
	}
	engine.publishScope(scope)
	t.Cleanup(scope.Close)

	if got := engine.reasoningEffort(scope, promptcontext.SampleNormal); got != "medium" {
		t.Fatalf("initial effort = %q", got)
	}
	if got := engine.reasoningEffort(scope, promptcontext.SampleToolFailureRepair); got != "high" {
		t.Fatalf("repair effort = %q", got)
	}
	if got := engine.reasoningEffort(scope, promptcontext.SampleVerificationRepair); got != "xhigh" {
		t.Fatalf("second repair effort = %q", got)
	}
}

func TestAdaptiveReasoningUsesHighForComplexArchitecture(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	scope := &Scope{
		engine: engine,
		spec: TurnSpec{
			Route: reasoningRoute(t),
			Request: TurnRequest{
				Prompt: "Find the root cause of a cross-module race condition",
				Intent: protocol.TurnIntentAnswer,
			},
		},
		state: newScopeState(engine),
	}
	if got := engine.reasoningEffort(scope, promptcontext.SampleNormal); got != "high" {
		t.Fatalf("complex effort = %q", got)
	}
}

func TestOutputCapacityIsIndependentOfReasoningEffort(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.MaxOutputTokens = 0
	route := reasoningRoute(t)
	if got := engine.maxOutputFor(route); got != route.Model().Limits.MaxOutputTokens {
		t.Fatalf("automatic output limit = %d", got)
	}
	engine.options.MaxOutputTokens = 12_000
	if got := engine.maxOutputFor(route); got != 12_000 {
		t.Fatalf("configured output limit = %d", got)
	}
}

func TestEngineUsesModelOutputCapacityWithAdaptiveMedium(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("done"),
	}}
	engine := newEngine(t, runtime, nil)
	engine.options.MaxOutputTokens = 0
	engine.options.Route = reasoningRoute(t)
	engine.options.Routes, _ = model.NewRouteSet(engine.options.Route, nil, false)
	if _, err := engine.Run(t.Context(), "answer the question", nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 1 ||
		runtime.requests[0].MaxOutputTokens !=
			engine.activeRoute().Model().Limits.MaxOutputTokens ||
		runtime.requests[0].ReasoningEffort != "medium" {
		t.Fatalf("request = %+v", runtime.requests)
	}
}

func reasoningRoute(t *testing.T) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "reasoning", Adapter: model.AdapterOpenAICompatible, Endpoint: "http://127.0.0.1:1",
		Protocol: model.ProtocolOpenAIResponses, Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{"model": {
			ID: "model", CanonicalID: "model", WireID: "model",
			Limits: model.Limits{ContextTokens: 1_048_576, MaxOutputTokens: 393_216},
			Capabilities: model.Capabilities{
				Streaming: true, Reasoning: true, ToolCalls: true,
			},
			Pricing: model.Pricing{
				Currency: "USD", Known: true, Provenance: model.ProvenanceFixture,
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
		ProviderID: "reasoning", ModelID: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return route
}
