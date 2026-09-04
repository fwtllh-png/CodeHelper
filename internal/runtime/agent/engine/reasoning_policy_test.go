package engine

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestReasoningEffortStaysFixedAcrossRepairAttempts(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.ReasoningEffort = "medium"

	if got := engine.reasoningEffort(); got != "medium" {
		t.Fatalf("initial effort = %q", got)
	}
	if got := engine.reasoningEffort(); got != "medium" {
		t.Fatalf("repair effort = %q", got)
	}
}

func TestReasoningEffortDoesNotChangeForComplexPrompts(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.ReasoningEffort = "medium"
	if got := engine.reasoningEffort(); got != "medium" {
		t.Fatalf("complex effort = %q", got)
	}
}

func TestFinishOnlyReasoningEffortUsesAdvertisedCapabilities(t *testing.T) {
	for _, test := range []struct {
		name         string
		capabilities model.Capabilities
		want         string
	}{
		{name: "unsupported"},
		{
			name: "low",
			capabilities: model.Capabilities{
				Reasoning: true, ReasoningEfforts: []string{"low", "high"},
			},
			want: "low",
		},
		{
			name: "off",
			capabilities: model.Capabilities{
				Reasoning: true, ReasoningEfforts: []string{"off", "high"},
			},
			want: "off",
		},
		{
			name: "provider none",
			capabilities: model.Capabilities{
				Reasoning: true, ReasoningEfforts: []string{"none", "high"},
			},
			want: "none",
		},
		{
			name: "only advertised level",
			capabilities: model.Capabilities{
				Reasoning: true, ReasoningEfforts: []string{"high"},
			},
			want: "high",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := finishOnlyReasoningEffort(
				test.capabilities,
			); got != test.want {
				t.Fatalf("finish-only effort = %q, want %q", got, test.want)
			}
		})
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
	engine.options.ReasoningEffort = "medium"
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

func TestOperatorMetadataDrivesTurnSpecAndProviderRequest(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{textStream("done")}}
	engine := newEngine(t, runtime, nil)
	route := operatorConfiguredRoute(t)
	engine.options.Route = route
	engine.options.Routes, _ = model.NewRouteSet(route, nil, true)
	engine.options.MaxOutputTokens = 0

	spec, err := SnapshotTurnSpec(
		engine.options,
		TurnIdentity{
			SessionID: "session", ThreadID: "thread",
			TurnID: "turn", ProfileRevision: 1,
		},
		TurnRequest{Prompt: "answer", Intent: protocol.TurnIntentAnswer},
	)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Limits.Context.ContextTokens != 200_000 ||
		spec.Limits.Context.OutputCeiling != 24_000 ||
		spec.Limits.Context.HardInputTokens != 176_000 ||
		spec.ModelMetadata == nil ||
		spec.ModelMetadata.Limits != string(model.ProvenanceOperatorConfig) {
		t.Fatalf("turn spec = %+v", spec)
	}
	if _, err := engine.Run(t.Context(), "answer", nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 1 ||
		runtime.requests[0].MaxOutputTokens != 24_000 {
		t.Fatalf("provider requests = %+v", runtime.requests)
	}
}

func operatorConfiguredRoute(t *testing.T) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "openai-compatible", Adapter: model.AdapterOpenAICompatible,
		Endpoint:   "https://models.example.com/v1",
		Protocol:   model.ProtocolOpenAIChat,
		Provenance: model.ProvenanceOperatorConfig,
		Models: map[string]model.Model{"custom-model": {
			ID: "custom-model", CanonicalID: "vendor/custom-model",
			WireID: "custom-model",
			Limits: model.Limits{
				ContextTokens: 200_000, MaxOutputTokens: 24_000,
			},
			Capabilities: model.Capabilities{Streaming: true, ToolCalls: true},
			Pricing: model.Pricing{
				Provenance: model.ProvenanceOperatorConfig,
			},
			MetadataProvenance: model.MetadataProvenance{
				CanonicalID:  model.ProvenanceOperatorConfig,
				WireID:       model.ProvenanceOperatorConfig,
				Limits:       model.ProvenanceOperatorConfig,
				Capabilities: model.ProvenanceOperatorConfig,
				Pricing:      model.ProvenanceOperatorConfig,
			},
			Provenance: model.ProvenanceOperatorConfig,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	route, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := route.Resolve(model.RouteRequest{
		ProviderID: "openai-compatible", ModelID: "custom-model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolved
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
				ReasoningEfforts: []string{"low", "medium", "high", "xhigh"},
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
