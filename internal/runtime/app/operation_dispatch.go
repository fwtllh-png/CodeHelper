package app

import (
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type OutcomeKind string

const (
	OutcomeCommitted OutcomeKind = "committed"
	OutcomeRejected  OutcomeKind = "rejected"
	OutcomeAsync     OutcomeKind = "async"
	OutcomeTerminal  OutcomeKind = "terminal"
)

type OperationOutcome struct {
	Kind    OutcomeKind
	Problem error
}

type operationDispatcher struct {
	runtime *Runtime
}

func (d operationDispatcher) Dispatch(accepted acceptedOperation) OperationOutcome {
	if d.runtime == nil || d.runtime.engine == nil {
		return OperationOutcome{
			Kind: OutcomeRejected, Problem: errors.New("runtime engine is not configured"),
		}
	}
	operation := accepted.operation
	switch payload := operation.Payload.(type) {
	case *protocol.StartTurnPayload:
		return StartTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.CancelTurnPayload:
		return CancelTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.SteerTurnPayload:
		return SteerTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.ApprovalDecisionPayload:
		return ApprovalHandler{d.runtime}.Handle(operation, payload)
	case *protocol.InputReplyPayload:
		return InputHandler{d.runtime}.Handle(operation, payload)
	case *protocol.CompactThreadPayload:
		return CompactThreadHandler{d.runtime}.Handle(operation, payload)
	case *protocol.ForkThreadPayload:
		return ForkThreadHandler{d.runtime}.Handle(operation, payload)
	case *protocol.RevertTurnPayload:
		return RevertTurnHandler{d.runtime}.Handle(operation, payload)
	default:
		return finishOutcome(errors.New("operation payload is not supported"))
	}
}

type StartTurnHandler struct{ *Runtime }
type CancelTurnHandler struct{ *Runtime }
type SteerTurnHandler struct{ *Runtime }
type ApprovalHandler struct{ *Runtime }
type InputHandler struct{ *Runtime }
type CompactThreadHandler struct{ *Runtime }
type ForkThreadHandler struct{ *Runtime }
type RevertTurnHandler struct{ *Runtime }

func (h SteerTurnHandler) Handle(operation protocol.Operation, payload *protocol.SteerTurnPayload) OperationOutcome {
	started, err := h.run(operation, payload)
	if err != nil {
		return finishOutcome(err)
	}
	if started {
		return OperationOutcome{Kind: OutcomeAsync}
	}
	return OperationOutcome{Kind: OutcomeCommitted}
}

func (h CompactThreadHandler) Handle(operation protocol.Operation, payload *protocol.CompactThreadPayload) OperationOutcome {
	return finishOutcome(h.invoke(operation, func(sink EngineSink) error {
		return h.engine.CompactThread(h.ctx, payload, sink)
	}))
}

func (h ForkThreadHandler) Handle(operation protocol.Operation, payload *protocol.ForkThreadPayload) OperationOutcome {
	return finishOutcome(h.invoke(operation, func(sink EngineSink) error {
		return h.engine.ForkThread(h.ctx, payload, sink)
	}))
}

func (h RevertTurnHandler) Handle(operation protocol.Operation, payload *protocol.RevertTurnPayload) OperationOutcome {
	return finishOutcome(h.invoke(operation, func(sink EngineSink) error {
		return h.engine.RevertTurn(h.ctx, payload, sink)
	}))
}

func finishOutcome(err error) OperationOutcome {
	if err != nil {
		return OperationOutcome{Kind: OutcomeRejected, Problem: err}
	}
	return OperationOutcome{Kind: OutcomeCommitted}
}

func (d operationDispatcher) Apply(operation protocol.Operation, outcome OperationOutcome) {
	if err := validateOperationOutcome(outcome); err != nil {
		if d.runtime.reject(operation, err) == nil {
			d.runtime.commit(operation.ID)
		}
		return
	}
	if outcome.Kind == OutcomeRejected {
		if d.runtime.reject(operation, outcome.Problem) == nil {
			d.runtime.commit(operation.ID)
		}
		return
	}
	if outcome.Kind == OutcomeCommitted {
		d.runtime.commit(operation.ID)
	}
}

func validateOperationOutcome(outcome OperationOutcome) error {
	valid := outcome.Problem == nil
	switch outcome.Kind {
	case OutcomeCommitted, OutcomeAsync, OutcomeTerminal:
		if valid {
			return nil
		}
	case OutcomeRejected:
		if !valid {
			return nil
		}
	}
	return fmt.Errorf(
		"invalid operation outcome kind=%q problem=%t",
		outcome.Kind,
		outcome.Problem != nil,
	)
}
