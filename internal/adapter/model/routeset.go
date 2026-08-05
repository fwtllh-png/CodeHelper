package model

import (
	"errors"
	"fmt"
)

// RouteSet is a session's routing table: the act route plus the purposes that
// were given a model of their own.
//
// The fallback chain is one level deep — a named slot, otherwise act. Deeper
// inheritance would turn "why did this turn use that model" into a derivation,
// and the point of routing per purpose is that the answer stays readable.
//
// Act is held apart from the slots rather than stored as one of them because
// configuration says it apart: execution.provider and execution.model are the
// act route. One field for it means there is no way to configure act twice and
// no rule needed for which of the two wins.
type RouteSet struct {
	act    ReadyRoute
	slots  map[Purpose]ReadyRoute
	locked bool
	ready  bool
}

// NewRouteSet validates the act route and every slot. A slot for an unwired
// purpose is refused here rather than dropped, so that naming one fails at
// startup instead of looking configured for the rest of the session.
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
		// A slot is only useful if the model can do what the purpose asks. Refusing
		// here means a typo in [route.vision] fails at session start rather than on
		// the first image_analyze, which is when the operator is still looking at
		// the config they just wrote.
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

// Ready reports whether the set came from NewRouteSet. A zero RouteSet is not a
// set with no slots: it is one that was never resolved, and asking it for a
// route is a programming error rather than a fallback.
func (s RouteSet) Ready() bool { return s.ready }

// Locked reports whether fallback is forbidden.
func (s RouteSet) Locked() bool { return s.locked }

// Act is the route every purpose without a slot of its own falls back to.
func (s RouteSet) Act() ReadyRoute { return s.act }

// For resolves the route a purpose samples on.
//
// Locking turns the fallback into an error. That is the whole point of it: a run
// meant to be reproducible cannot have a missing slot quietly become the act
// model, because the transcript would then not say which model answered.
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
	// Falling back to act still has to cover what the purpose needs. Without this
	// check a session with no [route.vision] would quietly send images to a
	// text-only act model the moment something asked For(vision).
	if err := RequireCapabilities(
		s.act.Model().ID, s.act.Model().Capabilities,
		PurposeRequiredCapabilities(purpose),
	); err != nil {
		return ReadyRoute{}, fmt.Errorf("fallback act route for %s: %w", purpose, err)
	}
	return s.act, nil
}

// Slots reports the purposes with a route of their own, in the order Purposes
// declares them. Act is not among them: it is always present, so listing it
// would not tell a reader anything.
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
