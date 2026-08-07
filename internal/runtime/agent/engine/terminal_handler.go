package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// terminalHandler owns terminal-event deduplication and fallback projection for
// one turn. Journal cleanup remains registered after this handler so rollback
// errors are included in the terminal failure.
type terminalHandler struct {
	turn     uint64
	emitted  bool
	emitFunc func(Event) error
}

func newTerminalHandler(turn uint64, emit func(Event) error) *terminalHandler {
	return &terminalHandler{turn: turn, emitFunc: emit}
}

func (h *terminalHandler) send(state State, event Event) error {
	event.State, event.Turn = state, h.turn
	if state == Completed || state == Failed || state == Canceled {
		if h.emitted {
			return nil
		}
		h.emitted = true
	}
	return h.emitFunc(event)
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
