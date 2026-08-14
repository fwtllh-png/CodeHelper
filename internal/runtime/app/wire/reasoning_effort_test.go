package wire

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func TestSelectedModelCapabilitiesAdvertiseAdaptiveReasoning(t *testing.T) {
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
	if capabilities.DefaultReasoningEffort != "low" ||
		len(capabilities.ReasoningEfforts) != 4 ||
		capabilities.ReasoningEfforts[3] != "max" {
		t.Fatalf("capabilities = %+v", capabilities)
	}
}
