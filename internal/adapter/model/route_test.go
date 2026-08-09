package model

import (
	"strings"
	"testing"
)

func TestResolverCreatesReadyRouteWithMetadata(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}

	route, err := resolver.Resolve(RouteRequest{
		ProviderID: "openai",
		ModelID:    "gpt-4.1",
		Provenance: ProvenanceCLI,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := route.Validate(); err != nil {
		t.Fatal(err)
	}
	if route.ProviderKind() != ProviderOpenAI || route.Protocol() != ProtocolOpenAIChat {
		t.Fatalf("unexpected route: provider=%q protocol=%q", route.ProviderKind(), route.Protocol())
	}
	model := route.Model()
	if model.WireID != "gpt-4.1" || !model.Capabilities.NativeSearch {
		t.Fatalf("unexpected model metadata: %+v", model)
	}
	if model.Pricing.Provenance != ProvenanceBundled || route.Provenance() != ProvenanceCLI {
		t.Fatalf("unexpected provenance: model=%q route=%q", model.Pricing.Provenance, route.Provenance())
	}
}

func TestResolverDoesNotInferProviderFromModelName(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}

	_, err = resolver.Resolve(RouteRequest{ModelID: "claude-sonnet"})

	if err == nil || !strings.Contains(err.Error(), "provider id is required") {
		t.Fatalf("Resolve() error = %v, want explicit provider error", err)
	}
}

func TestResolverRejectsForeignModel(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}

	_, err = resolver.Resolve(RouteRequest{ProviderID: "openai", ModelID: "claude-sonnet"})

	if err == nil || !strings.Contains(err.Error(), "does not offer model") {
		t.Fatalf("Resolve() error = %v, want foreign model error", err)
	}
}

func TestResolverAutoRouteRequiresExplicitUniqueMatch(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(RouteRequest{ModelID: "claude-sonnet", Auto: true})
	if err != nil {
		t.Fatal(err)
	}
	if route.ProviderID() != "anthropic" || route.Provenance() != ProvenanceBundled {
		t.Fatalf("auto route = %+v", route)
	}
	if route.Model().MetadataProvenance.Limits != ProvenanceBundled {
		t.Fatalf("metadata provenance = %+v", route.Model().MetadataProvenance)
	}
}

// TestAutoRouteForAGPTModelIsAmbiguousBetweenChatAndResponses pins D6: the
// Responses provider shares the gpt-4.1 model id with the chat provider, so
// Auto must refuse rather than guess which protocol the operator wanted.
func TestAutoRouteForAGPTModelIsAmbiguousBetweenChatAndResponses(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(RouteRequest{ModelID: "gpt-4.1", Auto: true})
	if err == nil || !strings.Contains(err.Error(), "exactly one provider") {
		t.Fatalf("Resolve() error = %v, want an ambiguous-provider refusal", err)
	}
}

func TestBundledResponsesRouteIsReachableWithoutACustomEndpoint(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(RouteRequest{
		ProviderID: "openai-responses", ModelID: "gpt-4.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.Protocol() != ProtocolOpenAIResponses {
		t.Fatalf("protocol = %q, want openai_responses", route.Protocol())
	}
	if route.Endpoint() != "https://api.openai.com/v1" {
		t.Fatalf("endpoint = %q", route.Endpoint())
	}
	if !route.Model().Capabilities.PromptCache {
		t.Fatal("Responses gpt-4.1 must advertise prompt_cache: that is where the sticky key is sent")
	}
	if route.Credential().Name != "OPENAI_API_KEY" {
		t.Fatalf("credential = %+v, want the shared OpenAI key", route.Credential())
	}
}

func TestDeepSeekV4FlashKeepsResponsesProtocol(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(RouteRequest{
		ProviderID: "deepseek-v4-flash", ModelID: "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.Protocol() != ProtocolOpenAIResponses {
		t.Fatalf("protocol = %q, want openai_responses", route.Protocol())
	}
}

func TestZeroReadyRouteIsInvalid(t *testing.T) {
	var route ReadyRoute
	if err := route.Validate(); err == nil {
		t.Fatal("zero ReadyRoute is valid")
	}
}

func TestCatalogDefensivelyCopiesProvider(t *testing.T) {
	catalog := DefaultCatalog()
	provider, ok := catalog.Provider("openai")
	if !ok {
		t.Fatal("openai provider missing")
	}
	delete(provider.Models, "gpt-4.1")
	if second, _ := catalog.Provider("openai"); len(second.Models) != 1 {
		t.Fatal("catalog was mutated through returned provider")
	}
}
