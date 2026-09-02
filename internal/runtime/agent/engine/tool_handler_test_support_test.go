package engine

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

// runTools preserves the focused test surface without leaving a production
// execution path that can construct a Tool-scoped Kernel.
func (e *Engine) runTools(
	ctx context.Context,
	turnID string,
	calls []provider.ToolCall,
	executed map[string]tool.Result,
	send func(State, Event) error,
) ([]tool.Result, error) {
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	return e.runToolsWithCache(
		ctx, turnID, calls, executed, &toolResultCache{}, kernel, send,
	)
}
