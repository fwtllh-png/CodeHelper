package engine

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
)

// EnqueueMailbox queues an inter-agent mailbox message.
// triggerTurn=true injects into the current turn (cancels sampling, like Steer).
// triggerTurn=false buffers until the next turn begins (avoids late-mail pollution).
func (e *Engine) EnqueueMailbox(prompt string, triggerTurn bool) error {
	if prompt == "" {
		return errors.New("mailbox prompt is required")
	}
	item := PendingInput{Source: PendingMailbox, Prompt: prompt, TriggerTurn: triggerTurn}
	if !triggerTurn {
		e.scopeMu.Lock()
		e.mailboxHold = append(e.mailboxHold, item)
		e.scopeMu.Unlock()
		return nil
	}
	scope := e.runningScope()
	if scope == nil {
		e.scopeMu.Lock()
		e.mailboxHold = append(e.mailboxHold, item)
		e.scopeMu.Unlock()
		return nil
	}
	scope.mu.Lock()
	err := scope.state.mailbox.Offer(item)
	cancel := scope.state.cancel
	scope.mu.Unlock()
	if err != nil {
		return err
	}
	if cancel != nil {
		cancel(errors.New("mailbox input"))
	}
	return nil
}

func (e *Engine) appendSteering(history *[]provider.Message) bool {
	pending := e.drainPending()
	e.appendPendingInputs(history, pending)
	return len(pending) != 0
}

func (e *Engine) drainPending() []PendingInput {
	scope := e.executionScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	pending := scope.state.mailbox.Drain()
	scope.mu.Unlock()
	return pending
}

func (e *Engine) appendPendingInputs(history *[]provider.Message, pending []PendingInput) {
	for _, item := range pending {
		text := item.Prompt
		if item.Source == PendingMailbox {
			text = "[mailbox] " + item.Prompt
		}
		message := provider.TextMessage(provider.RoleUser, text)
		message.Turn = e.turn
		*history = append(*history, message)
	}
}

func (e *Engine) setActiveCancel(cancel context.CancelCauseFunc) {
	scope := e.runningScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.cancel = cancel
	scope.mu.Unlock()
}

func (e *Engine) clearActiveCancel() {
	scope := e.runningScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.cancel = nil
	scope.mu.Unlock()
}

func (e *Engine) cancellationReason() string {
	scope := e.executionScope()
	if scope == nil {
		return ""
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.state.cancelReason
}

func retainCanceledHistory(messages []provider.Message) []provider.Message {
	retained, _, err := contextstore.NormalizePairs(messages)
	if err != nil {
		return nil
	}
	return retained
}
