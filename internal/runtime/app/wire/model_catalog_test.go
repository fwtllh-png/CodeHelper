package wire

import (
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
)

func TestRuntimeModelCatalogMarksOnlySelectableProviderModelsHot(t *testing.T) {
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	selected, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "deepseek",
		ModelID:    "deepseek-chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	selectable, err := runtimeSelectableRoutes(selected, true)
	if err != nil {
		t.Fatal(err)
	}
	providers, models := runtimeModelCatalog(
		selected,
		selectedModelCapabilities(selected),
		selectable,
	)

	for _, provider := range providers.Providers {
		if provider.ID == "deepseek" {
			if provider.Availability != "available" {
				t.Fatalf("selected provider = %+v", provider)
			}
			continue
		}
		if provider.Availability != "unavailable" || provider.Reason == "" {
			t.Fatalf("non-selected provider = %+v", provider)
		}
	}
	hot := 0
	for _, entry := range models.Models {
		if entry.Provider == "deepseek" {
			if entry.Capabilities.SelectionMode != "hot" ||
				entry.Capabilities.Availability != "available" {
				t.Fatalf("deepseek model = %+v", entry)
			}
			hot++
		}
	}
	if hot != 2 {
		t.Fatalf("hot deepseek models = %d", hot)
	}
}

func TestRuntimeSelectableRoutesKeepsCustomRouteFixed(t *testing.T) {
	descriptor := &model.Model{
		ID: "future-model", CanonicalID: "vendor/future-model",
		WireID:       "future-model",
		Limits:       model.Limits{ContextTokens: 200_000, MaxOutputTokens: 24_000},
		Capabilities: model.Capabilities{Streaming: true, ToolCalls: true},
		MetadataProvenance: model.MetadataProvenance{
			CanonicalID:  model.ProvenanceOperatorConfig,
			WireID:       model.ProvenanceOperatorConfig,
			Limits:       model.ProvenanceOperatorConfig,
			Capabilities: model.ProvenanceOperatorConfig,
		},
		Provenance: model.ProvenanceOperatorConfig,
	}
	selected, err := resolveExecRoute(execRouteOptions{
		ProviderID: "openai-compatible", ModelID: "future-model",
		BaseURL:  "https://models.example.com/v1",
		Protocol: model.ProtocolOpenAIChat, Model: descriptor,
	})
	if err != nil {
		t.Fatal(err)
	}
	selectable, err := runtimeSelectableRoutes(selected, false)
	if err != nil {
		t.Fatal(err)
	}
	capabilities := selectedModelCapabilities(selected)
	capabilities.SelectionMode = "fixed"
	_, models := runtimeModelCatalog(
		selected, capabilities,
		selectable,
	)
	for _, entry := range models.Models {
		if !entry.Selected {
			continue
		}
		if entry.Capabilities.SelectionMode != "fixed" ||
			entry.Source != "connection_baseline" ||
			entry.Capabilities.MetadataProvenance.Limits !=
				string(model.ProvenanceOperatorConfig) {
			t.Fatalf("selected fixed model = %+v", entry)
		}
	}
	profiles, mutable := runtimeProfileModels(models, selected.ProviderID(), capabilities)
	if len(profiles) != 0 || len(mutable) != 0 {
		t.Fatalf("fixed route profiles=%+v mutable=%v", profiles, mutable)
	}
}
