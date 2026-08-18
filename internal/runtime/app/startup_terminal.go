package app

import (
	"context"
	"errors"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// commitStartupTerminal closes a durable Turn accepted before its Engine could
// publish a terminal envelope. Existing facts are only reused after their
// coordinator has already reached a terminal state.
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
	coordinator, err := turnkernel.StartupTerminalCoordinator(
		ctx,
		payload.TurnID,
		payload.Intent,
		r.defaultProfile.Mode,
		r.defaultProfile.Revision,
		r.terminalStore,
		facts,
	)
	if err != nil {
		return err
	}
	if len(facts) == 0 {
		if err := turnkernel.RequestStartupTerminal(ctx, coordinator, cause); err != nil {
			return err
		}
	}
	domainFacts, err := coordinator.DomainFacts(ctx)
	if err != nil {
		return err
	}
	frozen := coordinator.Snapshot()
	measurement, err := turnkernel.NewTerminalMeasurementSnapshot(
		time.Now().UTC(),
		nil,
		frozen.Usage,
		true,
	)
	if err != nil {
		return err
	}
	terminal, err := turnkernel.ProtocolTerminalEvent(frozen)
	if err != nil {
		return err
	}
	return sink.CommitTerminal(TerminalMaterial{
		FrozenState: frozen,
		DomainFacts: domainFacts,
		Measurement: measurement,
		Receipt: &protocol.ExecutionReceiptData{
			Goal: payload.Prompt,
			Orchestration: protocol.CloneOrchestrationCorrelation(
				payload.Orchestration,
			),
			Intent:            protocol.NormalizeTurnIntent(payload.Intent),
			Mode:              frozen.Mode,
			MeasurementDigest: measurement.Digest,
			UsageDigest:       measurement.UsageDigest,
		},
		Terminal: terminal,
	})
}
