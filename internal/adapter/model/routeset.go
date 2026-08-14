package model

import (
	"errors"
	"fmt"
)

// RouteSet holds the act route and optional one-level purpose overrides.
type RouteSet struct {
	act    ReadyRoute
	slots  map[Purpose]ReadyRoute
	locked bool
	ready  bool
}

// NewRouteSet validates the act route and every configured slot.
func NewRouteSet(act ReadyRoute, slots map[Purpose]ReadyRoute, locked bool) (RouteSet, error) {
	if err := act.Validate(); err != nil {
		return RouteSet{}, fmt.Errorf("act route: %w", err)
	}
	resolved := make(map[Purpose]ReadyRoute, len(slots))
	for purpose, route := range slots {
		if _, err := ParsePurpose(string(purpose)); err != nil {
			return RouteSet{}, err
		}
		if purpose == PurposeAct {
			return RouteSet{}, errors.New(
				"the act route comes from execution.provider and execution.model, not from a route slot",
			)
		}
		if !purpose.Wired() {
			return RouteSet{}, fmt.Errorf(
				"route purpose %q is registered but nothing samples on it yet", purpose,
			)
		}
		if err := route.Validate(); err != nil {
			return RouteSet{}, fmt.Errorf("%s route: %w", purpose, err)
		}
		if err := RequireCapabilities(
			route.Model().ID, route.Model().Capabilities,
			PurposeRequiredCapabilities(purpose),
		); err != nil {
			return RouteSet{}, fmt.Errorf("%s route: %w", purpose, err)
		}
		resolved[purpose] = route
	}
	return RouteSet{act: act, slots: resolved, locked: locked, ready: true}, nil
}

// Ready reports whether NewRouteSet produced the value.
func (s RouteSet) Ready() bool { return s.ready }

// Locked reports whether fallback is forbidden.
func (s RouteSet) Locked() bool { return s.locked }

// Act is the route every purpose without a slot of its own falls back to.
func (s RouteSet) Act() ReadyRoute { return s.act }

// For resolves a purpose override or the act fallback.
func (s RouteSet) For(purpose Purpose) (ReadyRoute, error) {
	if !s.ready {
		return ReadyRoute{}, errors.New("route set was not produced by NewRouteSet")
	}
	if _, err := ParsePurpose(string(purpose)); err != nil {
		return ReadyRoute{}, err
	}
	if !purpose.Wired() {
		return ReadyRoute{}, fmt.Errorf(
			"route purpose %q is registered but nothing samples on it yet", purpose,
		)
	}
	if route, exists := s.slots[purpose]; exists {
		return route, nil
	}
	if purpose == PurposeAct {
		return s.act, nil
	}
	if s.locked {
		return ReadyRoute{}, fmt.Errorf(
			"route lock: no %s route is configured and falling back to act is not allowed", purpose,
		)
	}
	if err := RequireCapabilities(
		s.act.Model().ID, s.act.Model().Capabilities,
		PurposeRequiredCapabilities(purpose),
	); err != nil {
		return ReadyRoute{}, fmt.Errorf("fallback act route for %s: %w", purpose, err)
	}
	return s.act, nil
}

// Slots reports configured overrides in stable purpose order.
func (s RouteSet) Slots() []Purpose {
	if len(s.slots) == 0 {
		return nil
	}
	configured := make([]Purpose, 0, len(s.slots))
	for _, purpose := range Purposes() {
		if _, exists := s.slots[purpose]; exists {
			configured = append(configured, purpose)
		}
	}
	return configured
}
