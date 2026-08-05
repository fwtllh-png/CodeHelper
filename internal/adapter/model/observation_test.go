package model_test

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func TestApplyProbeTightensWithoutTrustAndWidensOnlyWithTrust(t *testing.T) {
	base := model.Capabilities{Streaming: true, Vision: true, Reasoning: false}
	observations := []model.CapabilityObservation{
		{Capability: model.CapVision, Supported: false, Source: "probe"},
		{Capability: model.CapReasoning, Supported: true, Source: "probe"},
	}

	tightened := model.ApplyProbe(base, observations, false)
	if tightened.Vision {
		t.Fatal("probe unsupported must clear vision")
	}
	if tightened.Reasoning {
		t.Fatal("probe supported must not widen without --trust-probe")
	}
	if !tightened.Streaming {
		t.Fatal("unrelated bits must stay")
	}

	widened := model.ApplyProbe(base, observations, true)
	if widened.Vision {
		t.Fatal("unsupported still clears under trust")
	}
	if !widened.Reasoning {
		t.Fatal("supported must widen with --trust-probe")
	}
}

func TestReadyRouteWithCapabilitiesDoesNotMutateOriginal(t *testing.T) {
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "openai", ModelID: "gpt-4.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !route.Model().Capabilities.Vision {
		t.Fatal("fixture: gpt-4.1 should advertise vision")
	}
	updated := route.WithCapabilities(model.ApplyProbe(
		route.Model().Capabilities,
		[]model.CapabilityObservation{{Capability: model.CapVision, Supported: false}},
		false,
	))
	if updated.Model().Capabilities.Vision {
		t.Fatal("updated route should have vision cleared")
	}
	if !route.Model().Capabilities.Vision {
		t.Fatal("original route must keep catalog capabilities")
	}
}
