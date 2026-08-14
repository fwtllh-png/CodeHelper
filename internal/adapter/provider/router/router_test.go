package router

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type testProvider struct{ calls int }

func (p *testProvider) Stream(
	context.Context,
	provider.ModelRequest,
) (provider.Stream, error) {
	p.calls++
	return testStream{}, nil
}

type testStream struct{}

func (testStream) Recv() (provider.StreamEvent, error) { return provider.StreamEvent{}, io.EOF }
func (testStream) Close() error                        { return nil }

func TestRegistryRejectsDuplicateAdapter(t *testing.T) {
	target := &testProvider{}
	first, err := BindAdapter(model.AdapterOpenAI, target)
	if err != nil {
		t.Fatal(err)
	}
	second, err := BindAdapter(model.AdapterOpenAI, target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRegistry(first, second); err == nil ||
		!strings.Contains(err.Error(), "already registered") {
		t.Fatalf("NewRegistry() error = %v, want duplicate refusal", err)
	}
}

func TestRouterRejectsMissingActiveAdapter(t *testing.T) {
	route := testRoute(t, model.AdapterDeepSeek, model.ProtocolOpenAIChat)
	routes, err := model.NewRouteSet(route, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := BindAdapter(model.AdapterOpenAI, &testProvider{})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(registry, routes); err == nil ||
		!strings.Contains(err.Error(), `adapter "deepseek" is not registered`) {
		t.Fatalf("New() error = %v, want missing adapter refusal", err)
	}
}

func TestRouterSelectsReadyRouteAdapter(t *testing.T) {
	openAI := &testProvider{}
	deepSeek := &testProvider{}
	openAIAdapter, _ := BindAdapter(model.AdapterOpenAI, openAI)
	deepSeekAdapter, _ := BindAdapter(model.AdapterDeepSeek, deepSeek)
	registry, err := NewRegistry(openAIAdapter, deepSeekAdapter)
	if err != nil {
		t.Fatal(err)
	}
	route := testRoute(t, model.AdapterDeepSeek, model.ProtocolOpenAIChat)
	routes, err := model.NewRouteSet(route, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := New(registry, routes)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.Stream(t.Context(), provider.ModelRequest{Route: route})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if deepSeek.calls != 1 || openAI.calls != 0 {
		t.Fatalf("calls: deepseek=%d openai=%d", deepSeek.calls, openAI.calls)
	}
}

func testRoute(
	t *testing.T,
	adapter model.AdapterID,
	protocol model.WireProtocol,
) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "test", Adapter: adapter, Endpoint: "http://127.0.0.1:1",
		Protocol: protocol, Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{"model": {
			ID: "model", CanonicalID: "model", WireID: "model",
			Limits:       model.Limits{ContextTokens: 1024, MaxOutputTokens: 128},
			Capabilities: model.Capabilities{Streaming: true},
			Pricing:      model.Pricing{Provenance: model.ProvenanceFixture},
			Provenance:   model.ProvenanceFixture,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{ProviderID: "test", ModelID: "model"})
	if err != nil {
		t.Fatal(err)
	}
	return route
}
