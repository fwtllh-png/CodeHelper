package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

type FrozenTerminalState struct {
	TurnID      string
	State       turnkernel.State
	DomainFacts []turnkernel.DomainFact
}

func (e *Engine) FrozenTerminalState(
	ctx context.Context,
) (FrozenTerminalState, error) {
	scope := e.currentScope()
	if scope == nil {
		return FrozenTerminalState{}, errors.New("turn coordinator is not active")
	}
	kernel, err := scope.kernel()
	if err != nil {
		return FrozenTerminalState{}, err
	}
	kernel.mu.Lock()
	defer kernel.mu.Unlock()
	facts, err := kernel.coordinator.DomainFacts(ctx)
	if err != nil {
		return FrozenTerminalState{}, err
	}
	state := kernel.coordinator.Snapshot()
	if !state.Phase.Terminal() {
		return FrozenTerminalState{}, errors.New("turn kernel is not terminal")
	}
	return FrozenTerminalState{
		TurnID:      kernel.coordinator.TurnID(),
		State:       state,
		DomainFacts: facts,
	}, nil
}
