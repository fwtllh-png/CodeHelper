package router

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
)

type testAdapter struct {
	id model.AdapterID
}

func (a testAdapter) ID() model.AdapterID            { return a.id }
func (testAdapter) Supports(model.WireProtocol) bool { return true }
func (a testAdapter) Prepare(request provider.ModelRequest) (providerwire.PreparedCall, error) {
	return providerwire.PreparedCall{
		Method: http.MethodPost, Path: "/test", Adapter: a.id,
		Protocol: request.Route.Protocol(),
	}, nil
}
func (testAdapter) OpenStream(
	io.ReadCloser,
	providerwire.PreparedCall,
) (provider.Stream, error) {
	return testStream{}, nil
}
func (testAdapter) ClassifyHTTP(providerwire.HTTPFailure) error { return nil }

type testTransport struct {
	calls   int
	id      model.AdapterID
	request provider.ModelRequest
	call    providerwire.PreparedCall
}

func (t *testTransport) Execute(
	_ context.Context,
	request provider.ModelRequest,
	call providerwire.PreparedCall,
	_ providerwire.Adapter,
) (provider.Stream, error) {
	t.calls++
	t.id = call.Adapter
	t.request = request
	t.call = call
	return testStream{}, nil
}

type sessionTestAdapter struct{ testAdapter }

func (sessionTestAdapter) TrySession(
	context.Context,
	provider.ModelRequest,
	providerwire.PreparedCall,
	providerwire.SessionTransport,
) (provider.Stream, bool, error) {
	return nil, false, nil
}

func TestRouterFiltersReplayBeforeAdapterAndTransport(t *testing.T) {
	registry, err := NewRegistry(
		testAdapter{id: model.AdapterDeepSeek},
		testAdapter{id: model.AdapterOpenAI},
	)
	if err != nil {
		t.Fatal(err)
	}
	route := testRoute(t, model.AdapterDeepSeek, model.ProtocolOpenAIChat)
	routes, err := model.NewRouteSet(route, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	transport := &testTransport{}
	runtime, err := New(registry, routes, transport)
	if err != nil {
		t.Fatal(err)
	}
	other := testRoute(t, model.AdapterOpenAI, model.ProtocolOpenAIChat)
	message := provider.ProducedAssistant(
		other,
		[]provider.ContentBlock{{
			Type: provider.ContentReasoning, Text: "visible",
		}},
		1,
		&provider.ReplayState{
			Version: provider.ReplayVersion, Data: []byte(`{"items":[]}`),
		},
	)
	_, err = runtime.Stream(t.Context(), provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			message,
			provider.TextMessage(provider.RoleUser, "next"),
		},
		MaxOutputTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := transport.request.Messages[0]
	if got.Provenance == nil || got.Provenance.Replay != nil {
		t.Fatalf("cross-adapter replay reached transport: %+v", got)
	}
}

func TestRouterDropsReplayAfterAssistantContentRewrite(t *testing.T) {
	registry, err := NewRegistry(testAdapter{id: model.AdapterDeepSeek})
	if err != nil {
		t.Fatal(err)
	}
	route := testRoute(t, model.AdapterDeepSeek, model.ProtocolOpenAIChat)
	routes, err := model.NewRouteSet(route, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	transport := &testTransport{}
	runtime, err := New(registry, routes, transport)
	if err != nil {
		t.Fatal(err)
	}
	message := provider.ProducedAssistant(
		route,
		[]provider.ContentBlock{{
			Type: provider.ContentReasoning, Text: "original",
		}},
		1,
		&provider.ReplayState{
			Version: provider.ReplayVersion, Data: []byte(`{"items":[]}`),
		},
	)
	message.Blocks[0].Text = "rewritten"
	_, err = runtime.Stream(t.Context(), provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			message,
			provider.TextMessage(provider.RoleUser, "next"),
		},
		MaxOutputTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := transport.request.Messages[0].Provenance; got == nil ||
		got.Replay != nil {
		t.Fatalf("rewritten replay reached transport: %+v", got)
	}
}

type testStream struct{}

func (testStream) Recv() (provider.StreamEvent, error) { return provider.StreamEvent{}, io.EOF }
func (testStream) Close() error                        { return nil }

func TestRegistryRejectsDuplicateAdapter(t *testing.T) {
	if _, err := NewRegistry(
		testAdapter{id: model.AdapterOpenAI},
		testAdapter{id: model.AdapterOpenAI},
	); err == nil ||
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
	registry, err := NewRegistry(testAdapter{id: model.AdapterOpenAI})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(registry, routes, &testTransport{}); err == nil ||
		!strings.Contains(err.Error(), `adapter "deepseek" is not registered`) {
		t.Fatalf("New() error = %v, want missing adapter refusal", err)
	}
}

func TestRouterSelectsReadyRouteAdapter(t *testing.T) {
	registry, err := NewRegistry(
		testAdapter{id: model.AdapterOpenAI},
		testAdapter{id: model.AdapterDeepSeek},
	)
	if err != nil {
		t.Fatal(err)
	}
	route := testRoute(t, model.AdapterDeepSeek, model.ProtocolOpenAIChat)
	routes, err := model.NewRouteSet(route, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	transport := &testTransport{}
	runtime, err := New(registry, routes, transport)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.Stream(t.Context(), provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleUser, "test"),
		},
		MaxOutputTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if transport.calls != 1 || transport.id != model.AdapterDeepSeek {
		t.Fatalf("calls=%d adapter=%q", transport.calls, transport.id)
	}
}

func TestRouterRecordsMissingSessionTransportFallback(t *testing.T) {
	adapter := sessionTestAdapter{testAdapter{id: model.AdapterOpenAI}}
	registry, err := NewRegistry(adapter)
	if err != nil {
		t.Fatal(err)
	}
	route := testRoute(t, model.AdapterOpenAI, model.ProtocolOpenAIResponses)
	capabilities := route.Model().Capabilities
	capabilities.IncrementalResponses = true
	route = route.WithCapabilities(capabilities)
	routes, err := model.NewRouteSet(route, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	transport := &testTransport{}
	runtime, err := New(registry, routes, transport)
	if err != nil {
		t.Fatal(err)
	}
	stream, err := runtime.Stream(t.Context(), provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleUser, "test"),
		},
		MaxOutputTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if transport.call.Projection.FallbackReason !=
		provider.ProjectionFallbackSessionUnavailable {
		t.Fatalf("projection=%+v", transport.call.Projection)
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
