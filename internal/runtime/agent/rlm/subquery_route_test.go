package rlm

import (
	"context"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
)

type recordingSubQueryProvider struct {
	request provider.ModelRequest
}

func (p *recordingSubQueryProvider) Stream(
	_ context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	p.request = request
	return &providerfixture.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "answer"},
		{Type: provider.EventMessageStop},
	}}, nil
}

func TestRouteSubQueryUsesModelOutputCapacity(t *testing.T) {
	catalog, err := model.NewCatalog(model.Provider{
		ID:         "subquery",
		Adapter:    model.AdapterOpenAICompatible,
		Endpoint:   "http://127.0.0.1:1",
		Protocol:   model.ProtocolOpenAIChat,
		Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{
			"model": {
				ID: "model", CanonicalID: "model", WireID: "model",
				Limits: model.Limits{
					ContextTokens:   128_000,
					MaxOutputTokens: 16_384,
				},
				Capabilities: model.Capabilities{Streaming: true},
				Pricing: model.Pricing{
					Currency: "USD", Known: true,
					Provenance: model.ProvenanceFixture,
				},
				Provenance: model.ProvenanceFixture,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "subquery",
		ModelID:    "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := &recordingSubQueryProvider{}
	answer, err := (RouteSubQuery{
		Provider: recorder,
		Route:    route,
	}).Query(t.Context(), "question", "")
	if err != nil {
		t.Fatal(err)
	}
	if answer != "answer" ||
		recorder.request.MaxOutputTokens != 16_384 {
		t.Fatalf(
			"answer=%q max_output_tokens=%d",
			answer,
			recorder.request.MaxOutputTokens,
		)
	}
}
