package app

import (
	"crypto/sha256"
	"encoding/hex"
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
	switch payload := operation.Payload.(type) {
	case *protocol.SubmitRunPayload:
		return (OrchestrationHandler{d.runtime}).Submit(operation, payload)
	case *protocol.CancelRunPayload:
		return (OrchestrationHandler{d.runtime}).Cancel(operation, payload)
	case *protocol.ResumeRunPayload:
		return (OrchestrationHandler{d.runtime}).Resume(operation, payload)
	case *protocol.RetryNodePayload:
		return (OrchestrationHandler{d.runtime}).RetryNode(operation, payload)
	case *protocol.SkipNodePayload:
		return (OrchestrationHandler{d.runtime}).SkipNode(operation, payload)
	}
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
		for index, event := range outcome.Events {
			var err error
			if protocol.IsWorkGraphOperation(operation.Kind) {
				err = sink.EmitStable(
					workGraphEventID(operation.ID, index),
					event,
				)
			} else {
				err = sink.Emit(event)
			}
			if err != nil {
				return
			}
		}
		s.commit(operation.ID)
		if protocol.IsWorkGraphOperation(operation.Kind) {
			if err := s.DrainWorkGraphEffects(s.ctx); err != nil {
				s.logger.Warn(
					"drain WorkGraph effects after operation",
					"operation", operation.ID,
					"error", err,
				)
			}
		}
	}
}

func workGraphEventID(
	operationID protocol.OperationID,
	index int,
) protocol.EventID {
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"work-graph-event\x00%s\x00%d",
		operationID,
		index,
	)))
	return protocol.EventID("evt_" + hex.EncodeToString(sum[:]))
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
