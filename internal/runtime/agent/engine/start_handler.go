package engine

import (
	"context"
	"errors"
	"sync"
)

// WorkspaceTurnGate serializes whole turns that share one writable workspace.
// A channel rather than sync.Mutex makes admission cancelable: a queued child
// must stop waiting when its turn is interrupted or its wall-time expires.
type WorkspaceTurnGate struct {
	token chan struct{}
}

func NewWorkspaceTurnGate() *WorkspaceTurnGate {
	gate := &WorkspaceTurnGate{token: make(chan struct{}, 1)}
	gate.token <- struct{}{}
	return gate
}

func (g *WorkspaceTurnGate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.token:
	}
	var once sync.Once
	return func() {
		once.Do(func() { g.token <- struct{}{} })
	}, nil
}

func (e *Engine) beginTurn() error {
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	if e.running {
		return errors.New("engine turn is already running")
	}
	e.running = true
	e.cancelReason = ""

	e.pending = append([]PendingInput(nil), e.mailboxHold...)
	e.mailboxHold = nil
	return nil
}

func (e *Engine) endTurn() {
	e.steerMu.Lock()
	for _, item := range e.pending {
		if item.Source == PendingMailbox {
			e.mailboxHold = append(e.mailboxHold, item)
		}
	}
	e.running = false
	e.pending = nil
	e.cancel = nil
	e.steerMu.Unlock()
}
