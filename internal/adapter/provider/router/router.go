package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
)

func NewLegacyClient() *httpclient.Client { return httpclient.New() }

// Adapter is the provider-semantic owner selected by a ReadyRoute.
// P1 binds the legacy codec client; later stages replace those bindings.
type Adapter interface {
	ID() model.AdapterID
	Stream(context.Context, provider.ModelRequest) (provider.Stream, error)
}

type boundAdapter struct {
	id     model.AdapterID
	target provider.Provider
}

func BindAdapter(id model.AdapterID, target provider.Provider) (Adapter, error) {
	if id == "" || target == nil {
		return nil, errors.New("adapter id and provider are required")
	}
	return boundAdapter{id: id, target: target}, nil
}

func (a boundAdapter) ID() model.AdapterID { return a.id }

func (a boundAdapter) Stream(
	ctx context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	return a.target.Stream(ctx, request)
}

// Registry is immutable after construction.
type Registry struct {
	adapters map[model.AdapterID]Adapter
}

func NewRegistry(adapters ...Adapter) (*Registry, error) {
	registered := make(map[model.AdapterID]Adapter, len(adapters))
	for _, adapter := range adapters {
		if adapter == nil || adapter.ID() == "" {
			return nil, errors.New("adapter id is required")
		}
		if _, exists := registered[adapter.ID()]; exists {
			return nil, fmt.Errorf("adapter %q already registered", adapter.ID())
		}
		registered[adapter.ID()] = adapter
	}
	return &Registry{adapters: registered}, nil
}

func (r *Registry) resolve(route model.ReadyRoute) (Adapter, error) {
	if r == nil {
		return nil, errors.New("adapter registry is required")
	}
	if err := route.Validate(); err != nil {
		return nil, err
	}
	adapter, exists := r.adapters[route.Adapter()]
	if !exists {
		return nil, fmt.Errorf("adapter %q is not registered", route.Adapter())
	}
	return adapter, nil
}

type Router struct {
	registry *Registry
}

func New(registry *Registry, routes model.RouteSet) (*Router, error) {
	if !routes.Ready() {
		return nil, errors.New("ready route set is required")
	}
	if _, err := registry.resolve(routes.Act()); err != nil {
		return nil, fmt.Errorf("act route: %w", err)
	}
	for _, purpose := range routes.Slots() {
		route, _ := routes.For(purpose)
		if _, err := registry.resolve(route); err != nil {
			return nil, fmt.Errorf("%s route: %w", purpose, err)
		}
	}
	return &Router{registry: registry}, nil
}

func (r *Router) Stream(
	ctx context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	adapter, err := r.registry.resolve(request.Route)
	if err != nil {
		return nil, err
	}
	return adapter.Stream(ctx, request)
}

var _ provider.Provider = (*Router)(nil)
