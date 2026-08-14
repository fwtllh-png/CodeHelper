package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func createWindowLedger(number uint64) (contextstore.WindowLedger, error) {
	id, err := protocol.NewWindowID()
	if err != nil {
		return contextstore.WindowLedger{}, err
	}
	return contextstore.NewWindowLedger(id, number)
}

func fallbackWindowLedger(
	current contextstore.WindowLedger,
	seed string,
) contextstore.WindowLedger {
	number := current.Number + 1
	if number == 1 {
		number = 1
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf(
		"%s:%d:%s", current.ID, number, seed,
	)))
	value, _ := contextstore.NewWindowLedger(
		"window_"+hex.EncodeToString(sum[:16]),
		number,
	)
	return value
}

func (e *Engine) currentWindowLedger() contextstore.WindowLedger {
	if scope := e.runningScope(); scope != nil {
		scope.mu.Lock()
		defer scope.mu.Unlock()
		return contextstore.CloneWindowLedger(scope.state.window)
	}
	return contextstore.CloneWindowLedger(e.window)
}

func (e *Engine) projectTokenWindow(
	context *protocol.SampleContextData,
	outputReserve uint64,
) contextstore.WindowProjection {
	window := e.currentWindowLedger()
	return window.Prepare(
		context,
		outputReserve,
		e.autoCompactLimit(),
		e.activeRoute().Model().Limits.ContextTokens,
	)
}

func (e *Engine) prepareTokenWindow(
	context *protocol.SampleContextData,
	outputReserve uint64,
) contextstore.WindowProjection {
	scope := e.runningScope()
	if scope == nil {
		projection := e.window.Prepare(
			context, outputReserve, e.autoCompactLimit(),
			e.activeRoute().Model().Limits.ContextTokens,
		)
		applyWindowProjection(context, projection)
		return projection
	}
	scope.mu.Lock()
	projection := scope.state.window.Prepare(
		context, outputReserve, e.autoCompactLimit(),
		e.activeRoute().Model().Limits.ContextTokens,
	)
	scope.mu.Unlock()
	applyWindowProjection(context, projection)
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
	hardLimit := e.activeRoute().Model().Limits.ContextTokens
	if hardLimit != 0 && inputTokens > hardLimit {
		return
	}
	projectedTokens := context.WindowProjectedTokens
	pendingTokens := context.WindowPendingTokens
	scope := e.runningScope()
	if scope == nil {
		e.window.Observe(*context, inputTokens, cachedTokens)
		projection := e.window.Prepare(
			context, context.WindowOutputReserve, e.autoCompactLimit(),
			hardLimit,
		)
		applyWindowProjection(context, projection)
		context.WindowProjectedTokens = projectedTokens
		context.WindowPendingTokens = pendingTokens
		return
	}
	scope.mu.Lock()
	scope.state.window.Observe(*context, inputTokens, cachedTokens)
	projection := scope.state.window.Prepare(
		context, context.WindowOutputReserve, e.autoCompactLimit(),
		hardLimit,
	)
	scope.mu.Unlock()
	applyWindowProjection(context, projection)
	context.WindowProjectedTokens = projectedTokens
	context.WindowPendingTokens = pendingTokens
}

func (e *Engine) advanceTokenWindow() contextstore.WindowLedger {
	scope := e.runningScope()
	if scope != nil {
		scope.mu.Lock()
		next, err := createWindowLedger(scope.state.window.Number + 1)
		if err != nil {
			next = fallbackWindowLedger(scope.state.window, e.options.SessionID)
		}
		scope.state.window = next
		scope.mu.Unlock()
		return next
	}
	next, err := createWindowLedger(e.window.Number + 1)
	if err != nil {
		next = fallbackWindowLedger(e.window, e.options.SessionID)
	}
	e.window = next
	return next
}

func (e *Engine) TokenWindowIdentity() (string, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.window.ID, e.window.Number
}

func (e *Engine) AdvanceTokenWindow() (string, uint64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	next := e.advanceTokenWindow()
	return next.ID, next.Number
}

func (e *Engine) RestoreTokenWindow(id string, number uint64) error {
	value, err := contextstore.NewWindowLedger(id, number)
	if err != nil {
		return err
	}
	e.mu.Lock()
	e.window = value
	e.mu.Unlock()
	return nil
}

func (e *Engine) autoCompactLimit() uint64 {
	limit := e.activeRoute().Model().Limits.ContextTokens
	compact := e.options.CompactWindow.AutoTokens
	if compact == 0 {
		compact = limit * 65 / 100
	}
	return min(compact, limit)
}

func applyWindowProjection(
	context *protocol.SampleContextData,
	value contextstore.WindowProjection,
) {
	if context == nil {
		return
	}
	context.WindowID = value.ID
	context.WindowNumber = value.Number
	context.WindowObserved = value.Observed
	context.WindowProjectedTokens = value.FullActiveTokens
	context.WindowFullActiveTokens = value.FullActiveTokens
	context.WindowPrefillTokens = value.PrefillTokens
	context.WindowBodyTokens = value.BodyTokens
	context.WindowPendingTokens = value.PendingTokens
	context.WindowOutputReserve = value.OutputReserve
}
