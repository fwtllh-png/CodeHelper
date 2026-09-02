package wire

import (
	"github.com/fwtllh-png/QCode/internal/platform/process"
	agentengine "github.com/fwtllh-png/QCode/internal/runtime/agent/engine"
)

func (s *Session) releaseTurnProcesses(
	manager *process.SessionManager,
	identity agentengine.TurnIdentity,
	scope string,
) {
	if s == nil || manager == nil {
		return
	}
	if _, err := manager.CloseByTurn(identity.TurnID); err != nil {
		s.metrics.Error()
		if s.logger != nil {
			s.logger.Error(
				"release "+scope+" Turn process sessions",
				"turn_id", identity.TurnID,
				"error", err,
			)
		}
	}
}

func (s *Session) turnProcessReleaser(
	manager *process.SessionManager,
	scope string,
) func(agentengine.TurnIdentity) {
	return func(identity agentengine.TurnIdentity) {
		s.releaseTurnProcesses(manager, identity, scope)
	}
}
