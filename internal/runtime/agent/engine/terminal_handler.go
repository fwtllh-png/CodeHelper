package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// turnEmitter deduplicates Engine Event projection for one turn.
type turnEmitter struct {
	turn          uint64
	emitted       bool
	contextBudget *ContextBudgetSnapshot
	primaryCode   protocol.ErrorCode
	primaryError  string
	secondary     []TerminalIssue
	emitFunc      func(Event) error
	committed     func() error
	cancelReason  func() string
	decision      func() (turnkernel.TerminalDecision, bool)
}

func newTurnEmitter(turn uint64, emit func(Event) error) *turnEmitter {
	return &turnEmitter{turn: turn, emitFunc: emit}
}

func (h *turnEmitter) setCancelReason(source func() string) {
	h.cancelReason = source
}

func (h *turnEmitter) setTerminalDecision(
	source func() (turnkernel.TerminalDecision, bool),
) {
	h.decision = source
}

func (h *turnEmitter) send(state State, event Event) error {
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
		if h.committed != nil {
			if err := h.committed(); err != nil {
				return err
			}
		}
		h.emitted = true
	}
	return nil
}

func (h *turnEmitter) setContextBudget(snapshot ContextBudgetSnapshot) {
	h.contextBudget = &snapshot
}

func (h *turnEmitter) setCommitted(apply func() error) {
	h.committed = apply
}

func (h *turnEmitter) setPrimary(err error) {
	if err == nil || h.primaryError != "" {
		return
	}
	primary := firstJoinedError(err)
	h.primaryCode = protocol.CodeOf(primary)
	h.primaryError = errorText(primary)
}

func (h *turnEmitter) addSecondary(phase string, err error) {
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
	usage provider.Usage,
	cost float64,
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
	delta, err := prepareSessionDelta(
		e.runningScope().spec.Identity.TurnID,
		e.sessionRevision,
		candidate,
		usage,
		cost,
		SessionStateDelta{
			WorkingSet: e.workingLedger().Delta(),
			Evidence:   e.evidenceSet().Delta(),
			Failures:   e.failureLedger().Delta(),
			Compaction: CompactionDelta{Count: e.compactionTotal()},
		},
	)
	if err != nil {
		return e.contextBudgetSnapshot(candidate), err
	}
	e.stageSessionDelta(delta)
	return e.contextBudgetSnapshot(candidate), nil
}

func (h *turnEmitter) finish(ctx context.Context, result *Result, resultErr *error) {
	if h.emitted {
		return
	}
	state, event := h.terminalEvent(ctx, *resultErr)
	if h.decision != nil {
		if decision, ok := h.decision(); ok {
			switch decision.Kind {
			case turnkernel.TerminalCompleted:
				state = Completed
				event = Event{}
			case turnkernel.TerminalCanceled:
				state = Canceled
				event = Event{
					Error:        decision.Message,
					CancelReason: decision.Message,
				}
			case turnkernel.TerminalFailed:
				state = Failed
				h.setPrimary(*resultErr)
				event = Event{
					ErrorCode: protocol.ErrorCode(decision.Code),
					Error:     decision.Message,
					SecondaryIssues: append(
						[]TerminalIssue(nil),
						h.secondary...,
					),
				}
			}
		}
	}
	_ = h.send(state, event)
	result.State = state
}

func (h *turnEmitter) terminalEvent(
	ctx context.Context,
	err error,
) (State, Event) {
	reason := ""
	if h.cancelReason != nil {
		reason = h.cancelReason()
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return Canceled, Event{
			Error:        "turn canceled",
			CancelReason: protocol.NormalizeCancelReason(reason),
		}
	}
	if reason == protocol.CancelReasonApprovalCanceled {
		return Canceled, Event{
			Error:        "approval canceled",
			CancelReason: reason,
		}
	}
	var decision *policy.DecisionError
	if errors.As(err, &decision) && decision.Code == "approval_canceled" {
		return Canceled, Event{
			Error:        "approval canceled",
			CancelReason: protocol.CancelReasonApprovalCanceled,
		}
	}
	h.setPrimary(err)
	return Failed, Event{
		ErrorCode: h.primaryCode, Error: h.primaryError,
		SecondaryIssues: append([]TerminalIssue(nil), h.secondary...),
	}
}

func (h *turnEmitter) terminalRequest(
	ctx context.Context,
	err error,
) (State, Event, turnkernel.TerminalRequested) {
	state, event := h.terminalEvent(ctx, err)
	request := turnkernel.TerminalRequested{}
	if state == Canceled {
		request.CancelReason = terminalValue(
			event.CancelReason,
			protocol.CancelReasonShutdown,
		)
	} else {
		request.FailureCode = string(event.ErrorCode)
		request.FailureMessage = terminalValue(event.Error, "turn failed")
	}
	return state, event, request
}

func terminalValue(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
