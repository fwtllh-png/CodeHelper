package app

import (
	"errors"
	"fmt"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

type OutcomeKind string

const (
	OutcomeCommitted OutcomeKind = "committed"
	OutcomeRejected  OutcomeKind = "rejected"
	OutcomeAsync     OutcomeKind = "async"
	OutcomeTerminal  OutcomeKind = "terminal"
)

type OperationOutcome struct {
	Kind       OutcomeKind
	Events     []protocol.EventData
	Async      *AsyncTurn
	Problem    *protocol.Problem
	CommitMode CommitMode
}
type CommitMode string

const (
	CommitNow      CommitMode = "now"
	CommitDeferred CommitMode = "deferred"
)

type AsyncTurn struct {
	ThreadID    protocol.ThreadID
	TurnID      protocol.TurnID
	OperationID protocol.OperationID
	ItemID      protocol.ItemID
}
type operationDispatcher struct {
	runtime *Runtime
}

func (d operationDispatcher) Dispatch(accepted acceptedOperation) OperationOutcome {
	if d.runtime == nil {
		return OperationOutcome{
			Kind: OutcomeRejected, Problem: protocol.ProblemOf(errors.New("runtime is not configured")),
			CommitMode: CommitNow,
		}
	}
	operation := accepted.operation
	if d.runtime.engine == nil {
		return OperationOutcome{
			Kind:       OutcomeRejected,
			Problem:    protocol.ProblemOf(errors.New("runtime engine is not configured")),
			CommitMode: CommitNow,
		}
	}
	switch payload := operation.Payload.(type) {
	case *protocol.StartTurnPayload:
		return StartTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.CancelTurnPayload:
		return CancelTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.SteerTurnPayload:
		return SteerTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.EnqueueTurnPayload:
		return EnqueueTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.UpdateQueuedTurnPayload:
		return UpdateQueuedTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.RemoveQueuedTurnPayload:
		return RemoveQueuedTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.PromoteQueuedTurnPayload:
		return PromoteQueuedTurnHandler{d.runtime}.Handle(operation, payload)
	case *protocol.ApprovalDecisionPayload:
		return ApprovalHandler{d.runtime}.Handle(operation, payload)
	case *protocol.InputReplyPayload:
		return InputHandler{d.runtime}.Handle(operation, payload)
	case *protocol.CompactThreadPayload:
		if _, active := d.runtime.active.LookupThread(payload.ThreadID); active {
			return finishOutcome(ErrActiveTurn)
		}
		return finishOutcome(d.runtime.invoke(operation, func(sink EngineSink) error {
			return d.runtime.engine.CompactThread(d.runtime.ctx, payload, sink)
		}))
	case *protocol.ForkThreadPayload:
		if _, active := d.runtime.active.LookupThread(payload.ThreadID); active {
			return finishOutcome(ErrActiveTurn)
		}
		return finishOutcome(d.runtime.invoke(operation, func(sink EngineSink) error {
			return d.runtime.engine.ForkThread(d.runtime.ctx, payload, sink)
		}))
	case *protocol.RevertTurnPayload:
		if _, active := d.runtime.active.LookupThread(payload.ThreadID); active {
			return finishOutcome(ErrActiveTurn)
		}
		return finishOutcome(d.runtime.invoke(operation, func(sink EngineSink) error {
			return d.runtime.engine.RevertTurn(d.runtime.ctx, payload, sink)
		}))
	default:
		return finishOutcome(errors.New("operation payload is not supported"))
	}
}

type StartTurnHandler struct{ *Runtime }
type CancelTurnHandler struct{ *Runtime }
type SteerTurnHandler struct{ *Runtime }
type ApprovalHandler struct{ *Runtime }
type InputHandler struct{ *Runtime }

func (h SteerTurnHandler) Handle(operation protocol.Operation, payload *protocol.SteerTurnPayload) OperationOutcome {
	started, err := h.run(operation, payload)
	if err != nil {
		return finishOutcome(err)
	}
	if started != nil {
		return OperationOutcome{Kind: OutcomeAsync, Async: started, CommitMode: CommitDeferred}
	}
	return OperationOutcome{Kind: OutcomeCommitted, CommitMode: CommitNow}
}
func finishOutcome(err error) OperationOutcome {
	if err != nil {
		return OperationOutcome{Kind: OutcomeRejected, Problem: protocol.ProblemOf(err), CommitMode: CommitNow}
	}
	return OperationOutcome{Kind: OutcomeCommitted, CommitMode: CommitNow}
}
func (s *OperationService) Apply(operation protocol.Operation, outcome OperationOutcome) {
	if err := validateOperationOutcome(outcome); err != nil {
		if s.reject(operation, err) == nil {
			s.commit(operation.ID)
		}
		return
	}
	if outcome.Kind == OutcomeRejected {
		if s.reject(operation, outcome.Problem) == nil {
			s.commit(operation.ID)
		}
		return
	}
	if outcome.Kind == OutcomeCommitted {
		sink := &runtimeSink{runtime: s.Runtime, operation: operation}
		drainThread := protocol.ThreadID("")
		for _, event := range outcome.Events {
			if err := sink.Emit(event); err != nil {
				return
			}
			if _, queued := event.(*protocol.TurnQueuedData); queued {
				drainThread, _, _ = protocol.OperationReferences(operation)
			}
		}
		s.commit(operation.ID)
		if drainThread != "" {
			s.Runtime.TurnQueueService.Drain(drainThread)
		}
	}
}
func validateOperationOutcome(outcome OperationOutcome) error {
	noProblem, noAsync, noEvents := outcome.Problem == nil, outcome.Async == nil, len(outcome.Events) == 0
	valid := outcome.Kind == OutcomeCommitted && noProblem && noAsync && outcome.CommitMode == CommitNow ||
		outcome.Kind == OutcomeRejected && !noProblem && noAsync && noEvents && outcome.CommitMode == CommitNow ||
		outcome.Kind == OutcomeAsync && noProblem && !noAsync && noEvents && outcome.CommitMode == CommitDeferred ||
		outcome.Kind == OutcomeTerminal && noProblem && noAsync && noEvents && outcome.CommitMode == CommitDeferred
	if valid {
		return nil
	}
	return fmt.Errorf(
		"invalid operation outcome kind=%q problem=%t",
		outcome.Kind,
		outcome.Problem != nil,
	)
}
