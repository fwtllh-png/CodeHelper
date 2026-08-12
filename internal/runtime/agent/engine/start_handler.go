package engine

import (
	"context"
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

func (e *Engine) attachPending(scope *Scope) {
	e.scopeMu.Lock()
	pending := append([]PendingInput(nil), e.mailboxHold...)
	e.mailboxHold = nil
	e.scopeMu.Unlock()
	scope.mu.Lock()
	scope.state.cancelReason = ""
	var overflow []PendingInput
	for _, item := range pending {
		if err := scope.state.mailbox.Offer(item); err != nil {
			overflow = append(overflow, item)
		}
	}
	scope.mu.Unlock()
	if len(overflow) != 0 {
		e.scopeMu.Lock()
		e.mailboxHold = append(e.mailboxHold, overflow...)
		e.scopeMu.Unlock()
	}
}
