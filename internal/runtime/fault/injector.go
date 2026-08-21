package fault

import (
	"context"
	"math/rand"
	"sync"
)

// InjectionRule describes a fault that may be injected at a specific stage.
type InjectionRule struct {
	Probability float64
	Code        Code
	Message     string
	Retryable   bool
	Origin      Origin
	Disposition Disposition
	SideEffects SideEffectState
}

// Injector is a global registry for fault injection. In production, it is a
// no-op. In tests, it allows injecting typed faults at specific stages. The
// injector is the single point where the runtime can be taught to fail in
// controlled ways, enabling systematic resilience testing through the
// benchmark harness.
type Injector struct {
	mu    sync.RWMutex
	rules map[Stage][]InjectionRule
}

// GlobalInjector is the process-wide fault injector. Tests and benchmarks
// register rules on it; production code calls Inject at each stage.
var GlobalInjector = &Injector{rules: make(map[Stage][]InjectionRule)}

// Register adds a fault injection rule for the given stage. Rules are
// evaluated in order; the first rule whose probability fires wins.
func (i *Injector) Register(stage Stage, rule InjectionRule) {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.rules[stage] = append(i.rules[stage], rule)
}

// Clear removes all registered rules. Call this in test cleanup.
func (i *Injector) Clear() {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.rules = make(map[Stage][]InjectionRule)
}

// Snapshot returns a copy of the current rules for inspection.
func (i *Injector) Snapshot() map[Stage][]InjectionRule {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	result := make(map[Stage][]InjectionRule, len(i.rules))
	for stage, rules := range i.rules {
		result[stage] = append([]InjectionRule(nil), rules...)
	}
	return result
}

// Inject checks the global injector for a fault at the given stage. If a
// matching rule is registered and its probability fires, it returns a typed
// *Problem. Otherwise, it returns nil.
//
// Callers should call Inject at every stage boundary and, if a Problem is
// returned, propagate it as the error for that operation. This ensures that
// injected faults go through the same Decide(), DispositionOf(), and
// CodeOf() paths as real errors.
func (i *Injector) Inject(ctx context.Context, stage Stage, operationID string) *Problem {
	if i == nil {
		return nil
	}
	i.mu.RLock()
	rules := i.rules[stage]
	i.mu.RUnlock()

	for _, rule := range rules {
		if rule.Probability >= 1.0 || roll(rule.Probability) {
			return NewClassified(
				rule.Code,
				rule.Message,
				rule.Retryable,
				Metadata{
					Origin:      rule.Origin,
					Stage:       stage,
					OperationID: operationID,
					Disposition: rule.Disposition,
					SideEffects: rule.SideEffects,
				},
				nil,
			)
		}
	}
	return nil
}

// roll returns true with the given probability (0.0 to 1.0).
func roll(probability float64) bool {
	return rand.Float64() < probability
}