package wire

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
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
	selectable, err := runtimeSelectableRoutes(selected, false)
	if err != nil {
		t.Fatal(err)
	}
	_, models := runtimeModelCatalog(
		selected,
		selectedModelCapabilities(selected),
		selectable,
	)
	for _, entry := range models.Models {
		if !entry.Selected {
			continue
		}
		if entry.Capabilities.SelectionMode != "fixed" {
			t.Fatalf("selected fixed model = %+v", entry)
		}
	}
}
