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
	primaryCode   protocol.ErrorCode
	primaryError  string
	secondary     []TerminalIssue
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

func (h *terminalHandler) setPrimary(err error) {
	if err == nil || h.primaryError != "" {
		return
	}
	primary := firstJoinedError(err)
	h.primaryCode = protocol.CodeOf(primary)
	h.primaryError = errorText(primary)
}

func (h *terminalHandler) addSecondary(phase string, err error) {
	if err == nil {
		return
	}
	h.secondary = append(h.secondary, TerminalIssue{
		Phase: phase, Code: protocol.CodeOf(err), Message: errorText(err),
	})
}

func firstJoinedError(err error) error {
	for err != nil {
		joined, ok := err.(interface{ Unwrap() []error })
		if !ok {
			return err
		}
		children := joined.Unwrap()
		if len(children) == 0 {
			return err
		}
		err = children[0]
	}
	return nil
}

func (e *Engine) finalizeTerminalContext(
	transaction []provider.Message,
	completed, canceled bool,
	send func(State, Event) error,
) (ContextBudgetSnapshot, error) {
	candidate := cloneMessages(e.history)
	// A failed transaction is deliberately excluded from candidate. Its last turn
	// is therefore the most recent durable completed turn, which is safe to compact
	// within as long as the compactor preserves closed tool pairs.
	allowCurrentTurn := true
	switch {
	case completed:
		candidate = cloneMessages(transaction)
	case canceled:
		candidate = retainCanceledHistory(transaction)
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
	h.setPrimary(err)
	_ = h.send(Failed, Event{
		ErrorCode: h.primaryCode, Error: h.primaryError,
		SecondaryIssues: append([]TerminalIssue(nil), h.secondary...),
	})
	result.State = Failed
}
