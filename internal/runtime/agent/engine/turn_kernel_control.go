package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

var ErrTurnCoordinatorNotActive = errors.New("turn coordinator is not active")

type FrozenTerminalState = turnkernel.FrozenTerminalState

func (e *Engine) FrozenTerminalState(
	ctx context.Context,
) (FrozenTerminalState, error) {
	scope := e.currentScope()
	if scope == nil {
		return FrozenTerminalState{}, ErrTurnCoordinatorNotActive
	}
	kernel, err := scope.kernel()
	if err != nil {
		return FrozenTerminalState{}, err
	}
	return kernel.FrozenTerminalState(ctx)
}
