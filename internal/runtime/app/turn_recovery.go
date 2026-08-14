package app

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
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
	for _, event := range events {
		if event.TurnID != source {
			continue
		}
		if event.ThreadID != payload.ThreadID {
			return protocol.NewProblem(
				protocol.CodeConflict,
				"Turn recovery source belongs to another Thread",
				false,
				nil,
			)
		}
		terminal = terminal || protocol.IsTerminalEvent(event.Kind)
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
