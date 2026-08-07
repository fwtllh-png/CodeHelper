package wire

import (
	"sort"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func runtimeModelCatalog(
	selectedProvider, selectedModel string,
	selectedCapabilities protocol.ModelCapabilities,
) (protocol.ProviderCatalog, protocol.ModelCatalog) {
	catalog := model.DefaultCatalog()
	providers := catalog.Providers()
	providerEntries := make([]protocol.ProviderCatalogEntry, 0, len(providers)+1)
	modelEntries := make([]protocol.ModelCatalogEntry, 0, 128)
	selectedSeen := false
	for _, catalogProvider := range providers {
		providerEntries = append(providerEntries, protocol.ProviderCatalogEntry{
			ID: catalogProvider.ID, DisplayName: catalogProvider.ID,
			Selected:     catalogProvider.ID == selectedProvider,
			Availability: "available",
		})
		ids := make([]string, 0, len(catalogProvider.Models))
		for id := range catalogProvider.Models {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			descriptor := catalogProvider.Models[id]
			selected := catalogProvider.ID == selectedProvider && id == selectedModel
			capabilities := catalogModelCapabilities(descriptor)
			if selected {
				capabilities = selectedCapabilities
				selectedSeen = true
			}
			modelEntries = append(modelEntries, protocol.ModelCatalogEntry{
				Provider: catalogProvider.ID, ID: id,
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
		modelEntries = append(modelEntries, protocol.ModelCatalogEntry{
			Provider: selectedProvider, ID: selectedModel,
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
	var efforts []string
	if capabilities.Reasoning {
		efforts = []string{"minimal", "low", "medium", "high", "xhigh"}
	}
	return protocol.ModelCapabilities{
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
		ReasoningEfforts:  efforts,
		CredentialStatus:  "unknown",
		Availability:      "available",
		SelectionMode:     "restart_required",
	}
}
