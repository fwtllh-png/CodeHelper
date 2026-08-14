package wire

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerrouter "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/router"
)

func newProviderRouter(
	client provider.Provider,
	routes model.RouteSet,
) (provider.Provider, error) {
	ids := []model.AdapterID{
		model.AdapterOpenAI, model.AdapterDeepSeek,
		model.AdapterAnthropic, model.AdapterOpenAICompatible,
	}
	adapters := make([]providerrouter.Adapter, 0, len(ids))
	for _, id := range ids {
		adapter, err := providerrouter.BindAdapter(id, client)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, adapter)
	}
	registry, err := providerrouter.NewRegistry(adapters...)
	if err != nil {
		return nil, err
	}
	return providerrouter.New(registry, routes)
}
