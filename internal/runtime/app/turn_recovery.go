package app

import (
	"fmt"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func (r StartTurnHandler) validateStart(payload *protocol.StartTurnPayload) error {
	if payload.Idle {
		if checker, ok := r.engine.(interface{ AllowIdleTurn() error }); ok {
			if err := checker.AllowIdleTurn(); err != nil {
				return err
			}
		}
	}
	if payload.Recovery == nil {
		return nil
	}
	events, err := r.events.Replay(r.ctx, 0)
	if err != nil {
		return fmt.Errorf("validate Turn recovery source: %w", err)
	}
	source := payload.Recovery.SourceTurnID
	terminal := false
	var startedOperationID protocol.OperationID
	var sourceThreadID protocol.ThreadID
	for _, event := range events {
		if event.TurnID != source {
			continue
		}
		if sourceThreadID == "" {
			sourceThreadID = event.ThreadID
		} else if event.ThreadID != sourceThreadID {
			return protocol.NewProblem(
				protocol.CodeConflict,
				"Turn recovery source has inconsistent Thread identity",
				false,
				nil,
			)
		}
		switch data := event.Data.(type) {
		case *protocol.TurnStartedData:
			startedOperationID = event.OperationID
		case *protocol.OperationRejectedData:
			terminal = terminal ||
				(event.OperationID == startedOperationID &&
					protocol.FaultAllowsTurnRecovery(data.Fault))
		default:
			terminal = terminal || protocol.IsTerminalEvent(event.Kind)
		}
	}
	if sourceThreadID != "" && sourceThreadID != payload.ThreadID {
		if r.sessionLifecycle == nil {
			return protocol.NewProblem(
				protocol.CodeConflict,
				"Turn recovery source belongs to another Thread",
				false,
				nil,
			)
		}
		sourceSession, sourceErr := r.sessionLifecycle.SessionForThread(
			r.ctx,
			sourceThreadID,
		)
		targetSession, targetErr := r.sessionLifecycle.SessionForThread(
			r.ctx,
			payload.ThreadID,
		)
		if sourceErr != nil || targetErr != nil || sourceSession != targetSession {
			return protocol.NewProblem(
				protocol.CodeConflict,
				"Turn recovery source belongs to another Session",
				false,
				nil,
			)
		}
	}
	if !terminal {
		return protocol.NewProblem(
			protocol.CodeConflict,
			"Turn recovery source is unavailable or not terminal",
			false,
			nil,
		)
	}
	return nil
}
