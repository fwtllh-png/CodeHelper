package wire

import (
	"context"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
)

// overlayProbeCapabilities applies SQLite probe observations onto every route
// in the set. Without a persistent store the set is returned unchanged: probe
// results only exist beside --data-dir.
func overlayProbeCapabilities(
	ctx context.Context,
	routes model.RouteSet,
	store *state.Store,
	trustProbe bool,
) (model.RouteSet, error) {
	if store == nil || !routes.Ready() {
		return routes, nil
	}
	repo := model.NewCapabilityRepository(store.SQLite().DB())
	act, err := overlayRouteProbe(ctx, repo, routes.Act(), trustProbe)
	if err != nil {
		return model.RouteSet{}, err
	}
	slots := make(map[model.Purpose]model.ReadyRoute)
	for _, purpose := range routes.Slots() {
		route, err := routes.For(purpose)
		if err != nil {
			return model.RouteSet{}, err
		}
		overlaid, err := overlayRouteProbe(ctx, repo, route, trustProbe)
		if err != nil {
			return model.RouteSet{}, fmt.Errorf("%s route: %w", purpose, err)
		}
		slots[purpose] = overlaid
	}
	return model.NewRouteSet(act, slots, routes.Locked())
}

func overlayRouteProbe(
	ctx context.Context,
	repo *model.CapabilityRepository,
	route model.ReadyRoute,
	trustProbe bool,
) (model.ReadyRoute, error) {
	observations, err := repo.List(ctx, route.ProviderID(), route.Model().ID)
	if err != nil {
		return model.ReadyRoute{}, err
	}
	if len(observations) == 0 {
		return route, nil
	}
	return route.WithCapabilities(model.ApplyProbe(
		route.Model().Capabilities, observations, trustProbe,
	)), nil
}
