package engine

import (
	"context"
	"errors"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func (e *Engine) Steer(prompt string) error {
	if prompt == "" {
		return errors.New("steering prompt is required")
	}
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	if !e.running {
		return errors.New("no active turn to steer")
	}
	e.pending = append(e.pending, PendingInput{Source: PendingSteer, Prompt: prompt})
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

// EnqueueMailbox queues an inter-agent mailbox message.
// triggerTurn=true injects into the current turn (cancels sampling, like Steer).
// triggerTurn=false buffers until the next turn begins (avoids late-mail pollution).
func (e *Engine) EnqueueMailbox(prompt string, triggerTurn bool) error {
	if prompt == "" {
		return errors.New("mailbox prompt is required")
	}
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	item := PendingInput{Source: PendingMailbox, Prompt: prompt, TriggerTurn: triggerTurn}
	if !triggerTurn {
		e.mailboxHold = append(e.mailboxHold, item)
		return nil
	}
	if !e.running {

		e.mailboxHold = append(e.mailboxHold, item)
		return nil
	}
	e.pending = append(e.pending, item)
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *Engine) appendSteering(history *[]provider.Message) bool {
	pending := e.drainPending()
	e.appendPendingInputs(history, pending)
	return len(pending) != 0
}

func (e *Engine) drainPending() []PendingInput {
	e.steerMu.Lock()
	pending := e.pending
	e.pending = nil
	e.steerMu.Unlock()
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

// appendSteeringPrompts keeps the historical name used by modelStep drain path.
func (e *Engine) appendSteeringPrompts(history *[]provider.Message, pending []PendingInput) {
	e.appendPendingInputs(history, pending)
}

func (e *Engine) setActiveCancel(cancel context.CancelFunc) {
	e.steerMu.Lock()
	e.cancel = cancel
	e.steerMu.Unlock()
}

func (e *Engine) clearActiveCancel() {
	e.steerMu.Lock()
	e.cancel = nil
	e.steerMu.Unlock()
}

// RequestCancel aborts the active model/tool phase if one is running (N14 Abort).
func (e *Engine) RequestCancel() {
	e.steerMu.Lock()
	cancel := e.cancel
	e.steerMu.Unlock()
	if cancel != nil {
		cancel()
	}
}
