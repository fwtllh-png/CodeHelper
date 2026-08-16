package app

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// commitStartupTerminal closes a durable Turn accepted before its Engine
// coordinator could start. It is deliberately limited to Turns with no domain
// facts so it cannot overwrite or fork an Engine-owned state machine.
func (r *Runtime) commitStartupTerminal(
	payload *protocol.StartTurnPayload,
	sink *runtimeSink,
	cause error,
) error {
	if r == nil || r.lifecycle == nil || r.terminalStore == nil || sink == nil {
		return errors.New("durable startup terminal dependencies are unavailable")
	}
	ctx := context.Background()
	facts, err := r.terminalStore.LoadDomainFacts(ctx, string(payload.TurnID))
	if err != nil {
		return err
	}
	if len(facts) != 0 {
		return errors.New("turn coordinator already owns durable domain facts")
	}

	revision := r.defaultProfile.Revision
	if revision == 0 {
		revision = 1
	}
	mode := r.defaultProfile.Mode
	if mode == "" {
		mode = "act"
	}
	state := turnkernel.NewState(
		protocol.NormalizeTurnIntent(payload.Intent),
		mode,
		revision,
	)
	coordinator, err := turnkernel.NewTurnCoordinator(
		string(payload.TurnID),
		state,
		r.terminalStore,
		turnkernel.NewDurableEffectDispatcher(),
	)
	if err != nil {
		return err
	}

	message := "turn engine failed before coordinator startup"
	if cause != nil {
		message = cause.Error()
	}
	canceled := errors.Is(cause, context.Canceled)
	request := turnkernel.TerminalRequested{
		FailureCode:    string(protocol.CodeOf(cause)),
		FailureMessage: message,
	}
	var terminal protocol.EventData = &protocol.TurnFailedData{
		Code: protocol.CodeOf(cause), Message: message,
	}
	if canceled {
		request = turnkernel.TerminalRequested{
			CancelReason: protocol.CancelReasonHostInterrupted,
		}
		terminal = &protocol.TurnCanceledData{
			Reason: protocol.CancelReasonHostInterrupted,
		}
	}
	if err := coordinator.Submit(ctx, request); err != nil {
		return err
	}
	if err := coordinator.Submit(ctx, turnkernel.FinishTerminal{}); err != nil {
		return err
	}
	domainFacts, err := coordinator.DomainFacts(ctx)
	if err != nil {
		return err
	}
	return sink.CommitTerminal(TerminalMaterial{
		FrozenState: coordinator.Snapshot(),
		DomainFacts: domainFacts,
		Receipt: &protocol.ExecutionReceiptData{
			Goal: payload.Prompt,
			Orchestration: protocol.CloneOrchestrationCorrelation(
				payload.Orchestration,
			),
			Intent: protocol.NormalizeTurnIntent(payload.Intent),
			Mode:   mode,
		},
		Terminal: terminal,
	})
}
