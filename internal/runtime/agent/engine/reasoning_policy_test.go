package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
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

func TestStageOutputLimitReservesByExecutionStage(t *testing.T) {
	for _, test := range []struct {
		effort string
		finish bool
		want   uint64
	}{
		{"low", false, 2048},
		{"medium", false, 4096},
		{"high", false, 8192},
		{"high", true, 2048},
	} {
		if got := promptcontext.OutputLimit(16_384, test.effort, test.finish); got != test.want {
			t.Fatalf("OutputLimit(%q, %t) = %d, want %d", test.effort, test.finish, got, test.want)
		}
	}
}

func reasoningRoute(t *testing.T) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "reasoning", Kind: model.ProviderCustom, Endpoint: "http://127.0.0.1:1",
		Protocol: model.ProtocolOpenAIResponses, Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{"model": {
			ID: "model", CanonicalID: "model", WireID: "model",
			Limits: model.Limits{ContextTokens: 128_000, MaxOutputTokens: 16_384},
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
