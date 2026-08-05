package wire

import (
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
)

type routeSetOptions struct {
	// Act resolves the act route, which every purpose without a slot of its own
	// falls back to.
	Act execRouteOptions
	// Slots is the configured [route.*] table, keyed by purpose name.
	Slots map[string]config.RouteSlot
	Lock  bool
}

// resolveRouteSet resolves the session's whole routing table.
//
// A session without any [route.*] slot produces a set holding only the act
// route, which resolves every purpose to exactly what a single-route session
// used before per-purpose routing existed.
func resolveRouteSet(options routeSetOptions) (model.RouteSet, error) {
	act, err := resolveExecRoute(options.Act)
	if err != nil {
		return model.RouteSet{}, err
	}
	if len(options.Slots) == 0 {
		return model.NewRouteSet(act, nil, options.Lock)
	}
	slots := make(map[model.Purpose]model.ReadyRoute, len(options.Slots))
	for name, slot := range options.Slots {
		purpose, err := model.ParsePurpose(name)
		if err != nil {
			return model.RouteSet{}, err
		}
		route, err := resolveSlotRoute(options.Act, slot, purpose)
		if err != nil {
			return model.RouteSet{}, fmt.Errorf("route.%s: %w", name, err)
		}
		slots[purpose] = route
	}
	return model.NewRouteSet(act, slots, options.Lock)
}

// resolveSlotRoute resolves one purpose's slot.
//
// A slot names a provider and a model and nothing else, so it can only be
// resolved where that is enough to reach a model: the bundled catalog. The two
// endpoint-overriding session shapes are handled deliberately rather than by
// accident, because both would otherwise send a slot somewhere surprising —
// a fixture session could quietly dial a real provider, which would make a
// hermetic test's central claim false.
func resolveSlotRoute(
	act execRouteOptions, slot config.RouteSlot, purpose model.Purpose,
) (model.ReadyRoute, error) {
	if act.BaseURL == "" {
		resolver, err := model.NewResolver(model.DefaultCatalog())
		if err != nil {
			return model.ReadyRoute{}, err
		}
		return resolver.Resolve(model.RouteRequest{
			ProviderID: slot.Provider, ModelID: slot.Model,
			Provenance: model.ProvenanceConfig,
			Require:    model.PurposeRequiredCapabilities(purpose),
		})
	}
	if !act.Fixture {
		return model.ReadyRoute{}, errors.New(
			"a --base-url session carries explicit metadata for one model only, " +
				"so no purpose can be routed to a second one",
		)
	}
	if slot.Provider != act.ProviderID {
		return model.ReadyRoute{}, fmt.Errorf(
			"a fixture session routes every purpose through the fixture provider %q, not %q",
			act.ProviderID, slot.Provider,
		)
	}
	fixture := act
	fixture.ModelID = slot.Model
	fixture.Model = fixtureModel(slot.Model)
	route, err := resolveExecRoute(fixture)
	if err != nil {
		return model.ReadyRoute{}, err
	}
	if err := model.RequireCapabilities(
		route.Model().ID, route.Model().Capabilities,
		model.PurposeRequiredCapabilities(purpose),
	); err != nil {
		return model.ReadyRoute{}, err
	}
	return route, nil
}
