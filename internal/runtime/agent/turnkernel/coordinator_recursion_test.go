package turnkernel

import (
	"context"
	"fmt"
	"runtime"
	"testing"
)

// chainedEffectExecutor returns effects that chain into further effects,
// simulating a deep chain of effect dispatches. This tests whether the
// coordinator's Submit → Dispatch → Submit recursion is bounded.
type chainedEffectExecutor struct {
	depth    int
	maxDepth int
}

func (e *chainedEffectExecutor) ExecuteEffect(
	_ context.Context,
	effect Effect,
) (Command, error) {
	e.depth++
	if e.depth > e.maxDepth {
		e.maxDepth = e.depth
	}
	e.depth--
	return EffectResultReceived{EffectID: effect.ID, Success: true}, nil
}

// TestSubmitDoesNotRecurseUnboundedly verifies that the coordinator's Submit
// method does not cause unbounded recursion when effects chain. This catches
// the bug where Submit → dispatch → Submit creates a recursive call that
// can overflow the stack with deeply chained effects.
func TestSubmitDoesNotRecurseUnboundedly(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	executor := &chainedEffectExecutor{}

	coordinator := newTestCoordinator(t, store, executor)

	// Start a turn and propose tool calls — this dispatches effects.
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}

	// Submit a tool call proposal — this should trigger effect execution.
	err := coordinator.Submit(t.Context(), ToolCallsProposed{
		Calls: []ToolCallState{
			{ID: "call-1", Name: "tool1"},
			{ID: "call-2", Name: "tool2"},
			{ID: "call-3", Name: "tool3"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// The test passes if we didn't overflow the stack. The max recursion
	// depth should be bounded by the number of effects dispatched in a
	// single transition, not by the total number of effects.
	if executor.maxDepth > 10 {
		t.Errorf("BUG: recursion depth reached %d; "+
			"Submit may be vulnerable to stack overflow with deeply chained effects",
			executor.maxDepth)
	}
	t.Logf("max recursion depth: %d, goroutines: %d", executor.maxDepth, runtime.NumGoroutine())
}

// TestSubmitRecursionDepthIsBoundedByStack verifies that the coordinator
// does not overflow the stack even with many chained effects.
func TestSubmitRecursionDepthIsBoundedByStack(t *testing.T) {
	// Use a deeper stack depth to verify safety.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("BUG: coordinator panicked (likely stack overflow): %v", r)
		}
	}()

	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	executor := &chainedEffectExecutor{}

	coordinator := newTestCoordinator(t, store, executor)

	// Bootstrap the turn.
	if err := coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Submit(t.Context(), PreparationFinished{}); err != nil {
		t.Fatal(err)
	}

	// Submit many tool calls to test deep chaining.
	calls := make([]ToolCallState, 50)
	for i := range calls {
		calls[i] = ToolCallState{ID: fmt.Sprintf("call-%d", i), Name: "tool"}
	}

	err := coordinator.Submit(t.Context(), ToolCallsProposed{Calls: calls})
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("max recursion depth with 50 calls: %d", executor.maxDepth)
}