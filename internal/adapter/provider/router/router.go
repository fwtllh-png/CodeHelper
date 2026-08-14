package router

import (
	"context"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type Registry struct {
	adapters map[model.AdapterID]providerwire.Adapter
}

func NewRegistry(adapters ...providerwire.Adapter) (*Registry, error) {
	registered := make(map[model.AdapterID]providerwire.Adapter, len(adapters))
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

func (r *Registry) resolve(route model.ReadyRoute) (providerwire.Adapter, error) {
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
	if !adapter.Supports(route.Protocol()) {
		return nil, fmt.Errorf("adapter %q does not support protocol %q", route.Adapter(), route.Protocol())
	}
	return adapter, nil
}

type Router struct {
	registry  *Registry
	transport providerwire.Transport
}

func New(
	registry *Registry,
	routes model.RouteSet,
	transport providerwire.Transport,
) (*Router, error) {
	if transport == nil {
		return nil, errors.New("provider transport is required")
	}
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
	return &Router{registry: registry, transport: transport}, nil
}

func (r *Router) Stream(ctx context.Context, request provider.ModelRequest) (provider.Stream, error) {
	adapter, err := r.registry.resolve(request.Route)
	if err != nil {
		return nil, err
	}
	request.Messages = provider.FilterReplayForAdapter(
		request.Messages, adapter.ID(),
	)
	if validationErr := request.Validate(); validationErr != nil {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			validationErr.Error(),
			false,
			validationErr,
		)
	}
	call, err := adapter.Prepare(request)
	if err != nil {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument, err.Error(), false, err,
		)
	}
	if call.Adapter != adapter.ID() || call.Protocol != request.Route.Protocol() {
		return nil, fmt.Errorf("adapter %q prepared a mismatched call", adapter.ID())
	}
	if sessionAdapter, ok := adapter.(providerwire.SessionAdapter); ok {
		sessionTransport, supported := r.transport.(providerwire.SessionTransport)
		if supported {
			stream, handled, err := sessionAdapter.TrySession(
				ctx, request, call, sessionTransport,
			)
			if handled || err != nil {
				return stream, err
			}
		}
	}
	return r.transport.Execute(ctx, request, call, adapter)
}

var _ provider.Provider = (*Router)(nil)
