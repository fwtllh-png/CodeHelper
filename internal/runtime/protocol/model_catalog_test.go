package protocol

import "testing"

func TestModelCatalogRequiresHonestSelectionAndAvailabilityMetadata(t *testing.T) {
	capabilities := ModelCapabilities{
		DisplayName:       "Fixture Model",
		ContextWindow:     128_000,
		MaxOutputTokens:   8_192,
		Streaming:         true,
		ToolCalls:         true,
		ParallelToolCalls: "unknown",
		CredentialStatus:  "unknown",
		Availability:      "available",
		SelectionMode:     "restart_required",
	}
	catalog := ModelCatalog{
		Version: ModelCatalogVersion,
		Models: []ModelCatalogEntry{{
			Provider: "fixture", ID: "fixture-model", Selected: true,
			Capabilities: capabilities,
		}},
	}
	if err := catalog.Validate(); err != nil {
		t.Fatal(err)
	}
	catalog.Models[0].Capabilities.SelectionMode = "hot"
	catalog.Models[0].Capabilities.Availability = "unavailable"
	if err := catalog.Validate(); err == nil {
		t.Fatal("unavailable model without a reason was accepted")
	}
}

func TestProviderCatalogRejectsDuplicateOrUnexplainedUnavailableEntries(t *testing.T) {
	catalog := ProviderCatalog{
		Version: ModelCatalogVersion,
		Providers: []ProviderCatalogEntry{{
			ID: "fixture", DisplayName: "Fixture",
			Availability: "unavailable",
		}},
	}
	if err := catalog.Validate(); err == nil {
		t.Fatal("unavailable provider without a reason was accepted")
	}
	catalog.Providers[0].Availability = "available"
	catalog.Providers = append(catalog.Providers, catalog.Providers[0])
	if err := catalog.Validate(); err == nil {
		t.Fatal("duplicate provider was accepted")
	}
}
