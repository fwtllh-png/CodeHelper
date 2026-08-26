package engine

import (
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (e *Engine) currentWindowLedger() agentcontext.WindowLedger {
	if scope := e.runningScope(); scope != nil {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		return scope.state.context.Window()
	}
	return e.context.Window()
}

func (e *Engine) projectTokenWindow(
	context *protocol.SampleContextData,
	outputReserve uint64,
) agentcontext.WindowProjection {
	window := e.currentWindowLedger()
	capacity := e.contextCapacity()
	return window.Prepare(context, outputReserve, e.autoCompactLimit(), capacity.ContextTokens)
}

func (e *Engine) prepareTokenWindow(
	context *protocol.SampleContextData,
	outputReserve uint64,
) agentcontext.WindowProjection {
	capacity := e.contextCapacity()
	scope := e.runningScope()
	if scope == nil {
		window := e.context.Window()
		projection := window.Prepare(
			context, outputReserve, e.autoCompactLimit(),
			capacity.ContextTokens,
		)
		agentcontext.ApplyWindowProjection(context, projection)
		agentcontext.ApplyCapacity(context, capacity)
		return projection
	}
	scope.mu.Lock()
	window := scope.state.context.Window()
	projection := window.Prepare(
		context, outputReserve, e.autoCompactLimit(),
		capacity.ContextTokens,
	)
	scope.mu.Unlock()
	agentcontext.ApplyWindowProjection(context, projection)
	agentcontext.ApplyCapacity(context, capacity)
	return projection
}

func (e *Engine) observeTokenWindow(
	context *protocol.SampleContextData,
	inputTokens uint64,
	cachedTokens uint64,
) {
	if context == nil {
		return
	}
	hardLimit := e.contextCapacity().ContextTokens
	if hardLimit != 0 && inputTokens > hardLimit {
		return
	}
	projectedTokens := context.WindowProjectedTokens
	pendingTokens := context.WindowPendingTokens
	scope := e.runningScope()
	if scope == nil {
		window := e.context.Window()
		window.Observe(*context, inputTokens, cachedTokens)
		e.context.SetWindow(window)
		projection := window.Prepare(
			context, context.WindowOutputReserve, e.autoCompactLimit(),
			hardLimit,
		)
		agentcontext.ApplyWindowProjection(context, projection)
		context.WindowProjectedTokens = projectedTokens
		context.WindowPendingTokens = pendingTokens
		return
	}
	scope.mu.Lock()
	window := scope.state.context.Window()
	window.Observe(*context, inputTokens, cachedTokens)
	scope.state.context.SetWindow(window)
	projection := window.Prepare(
		context, context.WindowOutputReserve, e.autoCompactLimit(),
		hardLimit,
	)
	scope.mu.Unlock()
	agentcontext.ApplyWindowProjection(context, projection)
	context.WindowProjectedTokens = projectedTokens
	context.WindowPendingTokens = pendingTokens
}

func (e *Engine) advanceTokenWindow() agentcontext.WindowLedger {
	scope := e.runningScope()
	if scope != nil {
		scope.mu.Lock()
		current := scope.state.context.Window()
		next, err := agentcontext.CreateWindowLedger(current.Number + 1)
		if err != nil {
			next = agentcontext.FallbackWindowLedger(current, e.options.SessionID)
		}
		scope.state.context.SetWindow(next)
		scope.mu.Unlock()
		return next
	}
	current := e.context.Window()
	next, err := agentcontext.CreateWindowLedger(current.Number + 1)
	if err != nil {
		next = agentcontext.FallbackWindowLedger(current, e.options.SessionID)
	}
	e.context.SetWindow(next)
	return next
}

func (e *Engine) TokenWindowIdentity() (string, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	window := e.context.Window()
	return window.ID, window.Number
}

func (e *Engine) AdvanceTokenWindow() (string, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	next := e.advanceTokenWindow()
	return next.ID, next.Number
}

func (e *Engine) RestoreTokenWindow(id string, number uint64) error {
	value, err := agentcontext.NewWindowLedger(id, number)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.context.SetWindow(value)
	e.mu.Unlock()
	return nil
}

func (e *Engine) autoCompactLimit() uint64 {
	_, compact, _ := agentcontext.WindowThresholds(
		e.options.Context.Window, e.contextCapacity().HardInputTokens,
	)
	return compact
}
