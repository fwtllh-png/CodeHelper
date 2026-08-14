package wire

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/anthropic"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/deepseek"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openaicompat"
	providerrouter "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/router"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
)

func newProviderRouter(
	client *httpclient.Client,
	routes model.RouteSet,
) (provider.Provider, error) {
	openAI, err := openai.NewAdapter(model.AdapterOpenAI)
	if err != nil {
		return nil, err
	}
	compatible, err := openaicompat.NewAdapter()
	if err != nil {
		return nil, err
	}
	adapters := []providerwire.Adapter{
		openAI, deepseek.NewAdapter(), anthropic.NewAdapter(), compatible,
	}
	registry, err := providerrouter.NewRegistry(adapters...)
	if err != nil {
		return nil, err
	}
	return providerrouter.New(registry, routes, client)
}
