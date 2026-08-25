package wire

import (
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func runtimeModelCatalog(
	selectedRoute model.ReadyRoute,
	selectedCapabilities protocol.ModelCapabilities,
	selectable map[string]model.ReadyRoute,
) (protocol.ProviderCatalog, protocol.ModelCatalog) {
	catalog := model.DefaultCatalog()
	providers := catalog.Providers()
	providerEntries := make([]protocol.ProviderCatalogEntry, 0, len(providers)+1)
	modelEntries := make([]protocol.ModelCatalogEntry, 0, 128)
	selectedProvider := selectedRoute.ProviderID()
	selectedModel := selectedRoute.Model().ID
	selectedSeen := false
	for _, catalogProvider := range providers {
		providerEntry := protocol.ProviderCatalogEntry{
			ID: catalogProvider.ID, DisplayName: catalogProvider.ID,
			Selected:     catalogProvider.ID == selectedProvider,
			Availability: "unavailable",
			Reason:       "Restart Runtime to use this provider",
		}
		if catalogProvider.ID == selectedProvider {
			providerEntry.Availability = "available"
			providerEntry.Reason = ""
		}
		providerEntries = append(providerEntries, providerEntry)
		ids := make([]string, 0, len(catalogProvider.Models))
		for id := range catalogProvider.Models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			descriptor := catalogProvider.Models[id]
			selected := catalogProvider.ID == selectedProvider && id == selectedModel
			capabilities := catalogModelCapabilities(descriptor)
			if route, ok := selectable[model.RouteKey(catalogProvider.ID, id)]; ok {
				capabilities = catalogModelCapabilities(route.Model())
				capabilities.SelectionMode = "hot"
			} else {
				capabilities.Availability = "unavailable"
				capabilities.UnavailableReason =
					"Restart Runtime to use this model route"
				capabilities.SelectionMode = "restart_required"
			}
			if selected {
				capabilities = selectedCapabilities
				if _, ok := selectable[model.RouteKey(
					catalogProvider.ID,
					id,
				)]; ok {
					capabilities.SelectionMode = "hot"
				} else {
					capabilities.SelectionMode = "fixed"
				}
				selectedSeen = true
			}
			modelEntries = append(modelEntries, protocol.ModelCatalogEntry{
				Provider: catalogProvider.ID, ID: id, Source: "catalog",
				Selected: selected, Capabilities: capabilities,
			})
		}
	}
	if _, ok := catalog.Provider(selectedProvider); !ok {
		providerEntries = append(providerEntries, protocol.ProviderCatalogEntry{
			ID: selectedProvider, DisplayName: selectedProvider,
			Selected: true, Availability: "available",
		})
	}
	if !selectedSeen {
		selectedCapabilities.SelectionMode = "fixed"
		modelEntries = append(modelEntries, protocol.ModelCatalogEntry{
			Provider: selectedProvider, ID: selectedModel,
			Source:   "connection_baseline",
			Selected: true, Capabilities: selectedCapabilities,
		})
	}
	sort.Slice(providerEntries, func(left, right int) bool {
		return providerEntries[left].ID < providerEntries[right].ID
	})
	return protocol.ProviderCatalog{
			Version: protocol.ModelCatalogVersion, Providers: providerEntries,
		}, protocol.ModelCatalog{
			Version: protocol.ModelCatalogVersion, Models: modelEntries,
		}
}

func catalogModelCapabilities(descriptor model.Model) protocol.ModelCapabilities {
	capabilities := descriptor.Capabilities
	result := protocol.ModelCapabilities{
		DisplayName:       descriptor.ID,
		ContextWindow:     descriptor.Limits.ContextTokens,
		MaxOutputTokens:   descriptor.Limits.MaxOutputTokens,
		Streaming:         capabilities.Streaming,
		Reasoning:         capabilities.Reasoning,
		ToolCalls:         capabilities.ToolCalls,
		ParallelToolCalls: "unknown",
		NativeSearch:      capabilities.NativeSearch,
		Vision:            capabilities.Vision,
		ImageInput:        capabilities.ImageInput,
		PromptCache:       capabilities.PromptCache,
		ReasoningEfforts:  capabilities.ReasoningEffortLevels(),
		CredentialStatus:  "unknown",
		Availability:      "available",
		SelectionMode:     "restart_required",
	}
	if result.Reasoning {
		result.DefaultReasoningEffort = capabilities.DefaultReasoningEffort
	}
	return result
}

func runtimeSelectableRoutes(
	selected model.ReadyRoute,
	allowCatalogSelection bool,
) (map[string]model.ReadyRoute, error) {
	result := make(map[string]model.ReadyRoute)
	if !allowCatalogSelection {
		return result, nil
	}
	catalog := model.DefaultCatalog()
	provider, ok := catalog.Provider(selected.ProviderID())
	if !ok {
		return result, nil
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		return nil, err
	}
	for modelID := range provider.Models {
		route, err := resolver.Resolve(model.RouteRequest{
			ProviderID: selected.ProviderID(),
			ModelID:    modelID,
			Provenance: model.ProvenanceConfig,
		})
		if err != nil {
			return nil, err
		}
		route = route.WithCredential(selected.Credential())
		result[model.RouteKey(selected.ProviderID(), modelID)] = route
	}
	return result, nil
}

func runtimeProfileModels(
	catalog protocol.ModelCatalog,
	providerID string,
	selectedCapabilities protocol.ModelCapabilities,
) (map[string]protocol.ModelCapabilities, []string) {
	profiles := make(map[string]protocol.ModelCapabilities)
	reasoningMutable := selectedCapabilities.Reasoning
	for _, entry := range catalog.Models {
		if entry.Provider != providerID ||
			entry.Capabilities.Availability != "available" ||
			entry.Capabilities.SelectionMode != "hot" {
			continue
		}
		profiles[model.RouteKey(entry.Provider, entry.ID)] =
			entry.Capabilities
		reasoningMutable = reasoningMutable || entry.Capabilities.Reasoning
	}
	mutable := make([]string, 0, 2)
	mutable = append(mutable, "model")
	if reasoningMutable {
		mutable = append(mutable, "reasoning_effort")
	}
	return profiles, mutable
}
