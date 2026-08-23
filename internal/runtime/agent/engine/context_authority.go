package engine

import (
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func (e *Engine) contextAuthority() *agentcontext.Authority {
	if scope := e.runningScope(); scope != nil {
		return &scope.state.context
	}
	return &e.context
}
