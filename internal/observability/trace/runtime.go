package trace

import (
	"context"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// Runtime creates per-turn trace recorders and exposes active span snapshots.
// Completed spans are persisted by the engine's trace sink.
type Runtime struct {
	Clock  func() time.Time
	active *activeRecorderRegistry
}

type activeRecorderRegistry struct {
	mu    sync.RWMutex
	turns map[protocol.TurnID]*Recorder
}

func NewRuntime(clock func() time.Time) Runtime {
	return Runtime{Clock: clock, active: newActiveRecorderRegistry()}
}

func newActiveRecorderRegistry() *activeRecorderRegistry {
	return &activeRecorderRegistry{turns: make(map[protocol.TurnID]*Recorder)}
}

func (r *activeRecorderRegistry) bind(
	turnID protocol.TurnID,
	recorder *Recorder,
) {
	if r == nil || turnID == "" || recorder == nil {
		return
	}
	r.mu.Lock()
	r.turns[turnID] = recorder
	r.mu.Unlock()
}

func (r *activeRecorderRegistry) snapshot(
	turnID protocol.TurnID,
) []Record {
	if r == nil || turnID == "" {
		return nil
	}
	r.mu.RLock()
	recorder := r.turns[turnID]
	r.mu.RUnlock()
	if recorder == nil {
		return nil
	}
	return recorder.Spans()
}

func (r *activeRecorderRegistry) release(turnID protocol.TurnID) {
	if r == nil || turnID == "" {
		return
	}
	r.mu.Lock()
	delete(r.turns, turnID)
	r.mu.Unlock()
}

func (r Runtime) Now() time.Time {
	if r.Clock == nil {
		return time.Now()
	}
	return r.Clock()
}

func (r Runtime) NewTurnRecorder(
	ctx context.Context,
	_ string,
	turnID string,
) *Recorder {
	recorder := NewRecorderContext(ctx, r.Clock)
	typedTurnID := protocol.TurnID(turnID)
	r.active.bind(typedTurnID, recorder)
	recorder.onClose = func() {
		r.active.release(typedTurnID)
	}
	return recorder
}

// ActiveTurnSpans returns a detached snapshot for a currently running turn.
// Completed turns are read from the durable trace repository.
func (r Runtime) ActiveTurnSpans(turnID protocol.TurnID) []Record {
	return r.active.snapshot(turnID)
}
