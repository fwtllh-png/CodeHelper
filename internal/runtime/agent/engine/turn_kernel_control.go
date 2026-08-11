package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type FrozenTerminalState struct {
	TurnID      string
	State       turnkernel.State
	DomainFacts []turnkernel.DomainFact
}

func (e *Engine) setTurnKernel(kernel *engineTurnKernel) {
	e.turnKernelMu.Lock()
	e.turnKernel = kernel
	e.turnKernelMu.Unlock()
}

func (e *Engine) clearTurnKernel(kernel *engineTurnKernel) {
	e.turnKernelMu.Lock()
	if e.turnKernel == kernel {
		e.turnKernel = nil
	}
	e.turnKernelMu.Unlock()
}

func (e *Engine) activeTurnKernel() (*engineTurnKernel, error) {
	e.turnKernelMu.Lock()
	defer e.turnKernelMu.Unlock()
	if e.turnKernel == nil {
		return nil, errors.New("turn coordinator is not active")
	}
	return e.turnKernel, nil
}

func (e *Engine) AcceptApprovalResult(
	requestID string,
	canceled bool,
) error {
	kernel, err := e.activeTurnKernel()
	if err != nil {
		return err
	}
	if err := kernel.resolveApproval(requestID, canceled); err != nil {
		return err
	}
	if canceled {
		e.steerMu.Lock()
		e.cancelReason = protocol.CancelReasonApprovalCanceled
		e.steerMu.Unlock()
	}
	return nil
}

func (e *Engine) AcceptInputResult(requestID string) error {
	kernel, err := e.activeTurnKernel()
	if err != nil {
		return err
	}
	return kernel.resolveInput(requestID)
}

func (e *Engine) AcceptCancel(reason string) error {
	kernel, err := e.activeTurnKernel()
	if err != nil {
		return err
	}
	return kernel.requestCancel(reason)
}

func (e *Engine) FrozenTerminalState(
	ctx context.Context,
) (FrozenTerminalState, error) {
	kernel, err := e.activeTurnKernel()
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
