package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// terminalHandler owns terminal-event deduplication and fallback projection for
// one turn. Journal cleanup remains registered after this handler so rollback
// errors are included in the terminal failure.
type terminalHandler struct {
	turn          uint64
	emitted       bool
	contextBudget *ContextBudgetSnapshot
	emitFunc      func(Event) error
}

func newTerminalHandler(turn uint64, emit func(Event) error) *terminalHandler {
	return &terminalHandler{turn: turn, emitFunc: emit}
}

func (h *terminalHandler) send(state State, event Event) error {
	event.State, event.Turn = state, h.turn
	terminal := state == Completed || state == Failed || state == Canceled
	if terminal {
		if h.emitted {
			return nil
		}
		event.ContextBudget = h.contextBudget
	}
	if err := h.emitFunc(event); err != nil {
		return err
	}
	if terminal {
		h.emitted = true
	}
	return nil
}

func (h *terminalHandler) setContextBudget(snapshot ContextBudgetSnapshot) {
	h.contextBudget = &snapshot
}

func (e *Engine) finalizeTerminalContext(
	transaction []provider.Message,
	completed, canceled bool,
	send func(State, Event) error,
) (ContextBudgetSnapshot, error) {
	candidate := cloneMessages(e.history)
	allowCurrentTurn := false
	switch {
	case completed:
		candidate = cloneMessages(transaction)
		allowCurrentTurn = true
	case canceled:
		candidate = retainCanceledHistory(transaction)
		allowCurrentTurn = true
	}
	if err := e.runTerminalCompactGate(&candidate, allowCurrentTurn, send); err != nil {
		return e.contextBudgetSnapshot(candidate), err
	}
	e.history = candidate
	return e.contextBudgetSnapshot(candidate), nil
}

func (h *terminalHandler) finish(ctx context.Context, result *Result, resultErr *error) {
	if h.emitted {
		return
	}
	err := *resultErr
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		_ = h.send(Canceled, Event{Error: "turn canceled"})
		result.State = Canceled
		return
	}
	var decision *policy.DecisionError
	if errors.As(err, &decision) && decision.Code == "approval_canceled" {
		_ = h.send(Canceled, Event{Error: "approval canceled"})
		result.State = Canceled
		return
	}
	_ = h.send(Failed, Event{ErrorCode: protocol.CodeOf(err), Error: errorText(err)})
	result.State = Failed
}
