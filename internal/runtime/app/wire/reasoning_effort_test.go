package wire

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func TestSelectedDeepSeekCapabilitiesAdvertiseNativeReasoningLevels(t *testing.T) {
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "deepseek-v4-flash",
		ModelID:    "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	capabilities := selectedModelCapabilities(route)
	if capabilities.DefaultReasoningEffort != "high" ||
		len(capabilities.ReasoningEfforts) != 4 ||
		capabilities.ReasoningEfforts[0] != "off" ||
		capabilities.ReasoningEfforts[2] != "high" ||
		capabilities.ReasoningEfforts[3] != "max" {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}

func TestDeepSeekDefaultReasoningEffortIsAppliedWithoutChangingExplicitValues(
	t *testing.T,
) {
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "deepseek-v4-flash",
		ModelID:    "deepseek-v4-flash",
	})
	if err != nil {
		t.Fatal(err)
	}
	if effort := effectiveReasoningEffort(route, ""); effort != "high" {
		t.Fatalf("default reasoning effort = %q, want high", effort)
	}
	if effort := effectiveReasoningEffort(route, "max"); effort != "max" {
		t.Fatalf("explicit reasoning effort = %q, want max", effort)
	}

	openAI, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "openai",
		ModelID:    "gpt-4.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if effort := effectiveReasoningEffort(openAI, ""); effort != "" {
		t.Fatalf("OpenAI adaptive reasoning effort = %q, want empty", effort)
	}
}
