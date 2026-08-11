package turnkernel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
)

type routedEffect struct {
	effect    Effect
	submit    func(Command) error
	started   bool
	result    Command
	resolving bool
}

// DurableEffectDispatcher routes every production Effect through one durable
// start/result registry.
type DurableEffectDispatcher struct {
	mu     sync.Mutex
	routed map[string]*routedEffect
}

func NewDurableEffectDispatcher() *DurableEffectDispatcher {
	return &DurableEffectDispatcher{
		routed: make(map[string]*routedEffect),
	}
}

func (d *DurableEffectDispatcher) Dispatch(
	_ context.Context,
	effect Effect,
	submit func(Command) error,
) error {
	if !RoutedEffect(effect.Kind) {
		return fmt.Errorf("no durable route for effect kind %q", effect.Kind)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.routed[effect.ID]; exists {
		return fmt.Errorf("effect %q was dispatched twice", effect.ID)
	}
	d.routed[effect.ID] = &routedEffect{
		effect: effect,
		submit: submit,
	}
	return nil
}

func (d *DurableEffectDispatcher) Start(
	kind EffectKind,
	callID string,
) (Effect, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, err := d.findLocked(kind, callID)
	if err != nil {
		return Effect{}, err
	}
	if entry.started {
		return Effect{}, fmt.Errorf(
			"effect %q is already started",
			entry.effect.ID,
		)
	}
	attempt := entry.effect.Attempt + 1
	entry.started = true
	if err := entry.submit(EffectStarted{
		EffectID: entry.effect.ID,
		Attempt:  attempt,
	}); err != nil {
		entry.started = false
		return Effect{}, err
	}
	entry.effect.Status = EffectRunning
	entry.effect.Attempt = attempt
	return entry.effect, nil
}

func (d *DurableEffectDispatcher) Routed(
	kind EffectKind,
	callID string,
) (Effect, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, err := d.findLocked(kind, callID)
	if err != nil {
		return Effect{}, false, err
	}
	return entry.effect, entry.started, nil
}

// Resolve retains the first Result Command until Coordinator durably accepts
// it. Retrying after a sink failure resubmits the same command without
// executing the side effect again.
func (d *DurableEffectDispatcher) Resolve(command Command) error {
	effectID := resultEffectID(command)
	if effectID == "" {
		return ErrEffectResultIdentity
	}
	d.mu.Lock()
	entry, exists := d.routed[effectID]
	if !exists {
		d.mu.Unlock()
		return fmt.Errorf("effect %q is not routed", effectID)
	}
	if !entry.started {
		d.mu.Unlock()
		return fmt.Errorf("effect %q has not started", effectID)
	}
	if entry.result == nil {
		entry.result = command
	}
	if entry.resolving {
		d.mu.Unlock()
		return fmt.Errorf("effect %q result is already resolving", effectID)
	}
	entry.resolving = true
	submit := entry.submit
	result := entry.result
	d.mu.Unlock()

	err := submit(result)
	d.mu.Lock()
	defer d.mu.Unlock()
	entry.resolving = false
	if err != nil {
		return err
	}
	if d.routed[effectID] == entry {
		delete(d.routed, effectID)
	}
	return nil
}

func (d *DurableEffectDispatcher) Abort(
	kind EffectKind,
	callID string,
	reason string,
) error {
	if reason == "" {
		return errors.New("effect abort reason is empty")
	}
	for _, effect := range d.PendingRouted(kind) {
		if callID != "" && effect.CallID != callID {
			continue
		}
		if effect.Status == EffectRequested {
			if _, err := d.Start(kind, effect.CallID); err != nil {
				return err
			}
		}
		if err := d.Resolve(EffectResultReceived{
			EffectID: effect.ID,
			Success:  false,
			Error:    reason,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (d *DurableEffectDispatcher) PendingRouted(
	kind EffectKind,
) []Effect {
	d.mu.Lock()
	defer d.mu.Unlock()
	ids := make([]string, 0, len(d.routed))
	for effectID := range d.routed {
		ids = append(ids, effectID)
	}
	slices.Sort(ids)
	effects := make([]Effect, 0, len(ids))
	for _, effectID := range ids {
		effect := d.routed[effectID].effect
		if kind == "" || effect.Kind == kind {
			effects = append(effects, effect)
		}
	}
	return effects
}

func (d *DurableEffectDispatcher) findLocked(
	kind EffectKind,
	callID string,
) (*routedEffect, error) {
	ids := make([]string, 0, len(d.routed))
	for effectID := range d.routed {
		ids = append(ids, effectID)
	}
	slices.Sort(ids)
	for _, effectID := range ids {
		entry := d.routed[effectID]
		if entry.effect.Kind == kind &&
			(callID == "" || entry.effect.CallID == callID) {
			return entry, nil
		}
	}
	return nil, fmt.Errorf(
		"no routed effect for kind=%q call_id=%q",
		kind,
		callID,
	)
}

func C2RoutedEffect(kind EffectKind) bool {
	switch kind {
	case EffectExecuteTool, EffectAwaitApproval, EffectAwaitInput:
		return true
	default:
		return false
	}
}

func C3RoutedEffect(kind EffectKind) bool {
	switch kind {
	case EffectSampleProvider, EffectRunVerification:
		return true
	default:
		return false
	}
}

func C4RoutedEffect(kind EffectKind) bool {
	switch kind {
	case EffectCommitJournal, EffectRollbackJournal:
		return true
	default:
		return false
	}
}

func RoutedEffect(kind EffectKind) bool {
	return C2RoutedEffect(kind) ||
		C3RoutedEffect(kind) ||
		C4RoutedEffect(kind)
}
