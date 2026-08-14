package model

import (
	"strings"
	"testing"
)

func TestRequireCapabilitiesNamesWhatIsMissing(t *testing.T) {
	have := Capabilities{Streaming: true, ToolCalls: true}
	err := RequireCapabilities("local", have, []Capability{CapVision, CapToolCalls})
	if err == nil || !strings.Contains(err.Error(), "vision") || !strings.Contains(err.Error(), `"local"`) {
		t.Fatalf("RequireCapabilities() error = %v, want the model and the missing bit", err)
	}
	if err := RequireCapabilities("local", have, []Capability{CapToolCalls}); err != nil {
		t.Fatal(err)
	}
}

func TestAVisionSlotWithoutVisionIsRefusedAtConstruction(t *testing.T) {
	act := testRoute(t, "anthropic", "claude-sonnet")
	// deepseek-chat is an ordinary chat model: no vision bit in the catalog.
	blind := testRoute(t, "deepseek", "deepseek-chat")

	_, err := NewRouteSet(act, map[Purpose]ReadyRoute{PurposeVision: blind}, false)
	if err == nil || !strings.Contains(err.Error(), "vision") {
		t.Fatalf("NewRouteSet() error = %v, want a vision capability refusal", err)
	}
}

func TestFallingBackToABlindActForVisionIsRefused(t *testing.T) {
	act := testRoute(t, "deepseek", "deepseek-chat")

	routes, err := NewRouteSet(act, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	// plan and subquery are ordinary chat, so they still fall back.
	for _, purpose := range []Purpose{PurposePlan, PurposeSubquery} {
		if _, err := routes.For(purpose); err != nil {
			t.Fatalf("For(%q) error = %v", purpose, err)
		}
	}
	_, err = routes.For(PurposeVision)
	if err == nil || !strings.Contains(err.Error(), "vision") {
		t.Fatalf("For(vision) error = %v, want a capability refusal on the act fallback", err)
	}
}

func TestResolverRequireRefusesAModelMissingTheBit(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	_, err = resolver.Resolve(RouteRequest{
		ProviderID: "deepseek", ModelID: "deepseek-chat",
		Require: []Capability{CapVision},
	})
	if err == nil || !strings.Contains(err.Error(), "vision") {
		t.Fatalf("Resolve() error = %v, want a vision refusal", err)
	}
	route, err := resolver.Resolve(RouteRequest{
		ProviderID: "openai", ModelID: "gpt-4.1",
		Require: []Capability{CapVision},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !route.Model().Capabilities.Vision {
		t.Fatal("gpt-4.1 should advertise vision in the catalog")
	}
}

func TestPurposeRequiredCapabilitiesOnlyVisionAsksForVision(t *testing.T) {
	if got := PurposeRequiredCapabilities(PurposeVision); len(got) != 1 || got[0] != CapVision {
		t.Fatalf("vision requirements = %v", got)
	}
	for _, purpose := range []Purpose{PurposeAct, PurposePlan, PurposeSubquery} {
		if got := PurposeRequiredCapabilities(purpose); got != nil {
			t.Fatalf("%s requirements = %v, want none", purpose, got)
		}
	}
}

func TestIncrementalResponsesIsAdvertisedOnlyByBundledResponsesRoute(t *testing.T) {
	resolver, err := NewResolver(DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	responses, err := resolver.Resolve(RouteRequest{
		ProviderID: "openai-responses", ModelID: "gpt-4.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	chat, err := resolver.Resolve(RouteRequest{
		ProviderID: "openai", ModelID: "gpt-4.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !responses.Model().Capabilities.IncrementalResponses {
		t.Fatal("bundled Responses route must advertise incremental transport")
	}
	if chat.Model().Capabilities.IncrementalResponses {
		t.Fatal("Chat route must not advertise Responses transport")
	}
}

func TestBundledReasoningEffortsAreExplicitAndIsolated(t *testing.T) {
	catalog := DefaultCatalog()
	deepseek, ok := catalog.Provider("deepseek-v4-flash")
	if !ok {
		t.Fatal("deepseek-v4-flash provider is missing")
	}
	levels := deepseek.Models["deepseek-v4-flash"].
		Capabilities.ReasoningEffortLevels()
	if len(levels) != 5 || levels[0] != "off" || levels[4] != "max" {
		t.Fatalf("reasoning efforts = %v", levels)
	}
	levels[4] = "mutated"
	again, _ := catalog.Provider("deepseek-v4-flash")
	if got := again.Models["deepseek-v4-flash"].
		Capabilities.ReasoningEffortLevels()[4]; got != "max" {
		t.Fatalf("catalog reasoning efforts mutated to %q", got)
	}
}
