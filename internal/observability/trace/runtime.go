package trace

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/tracecontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// Runtime is the Engine-facing observation boundary. It groups the trace clock
// with incremental observation identity without making Engine depend on the
// observation protocol package.
type Runtime struct {
	Clock     func() time.Time
	Recorder  observation.Recorder
	RuntimeID string
	contexts  *turnContextRegistry
}

type turnContextRegistry struct {
	mu    sync.RWMutex
	turns map[protocol.TurnID]observation.TraceContext
}

func newTurnContextRegistry() *turnContextRegistry {
	return &turnContextRegistry{
		turns: make(map[protocol.TurnID]observation.TraceContext),
	}
}

func (r *turnContextRegistry) bind(
	turnID protocol.TurnID,
	value observation.TraceContext,
) {
	if r == nil || turnID == "" {
		return
	}
	r.mu.Lock()
	r.turns[turnID] = value
	r.mu.Unlock()
}

func (r *turnContextRegistry) lookup(
	turnID protocol.TurnID,
) *observation.TraceContext {
	if r == nil || turnID == "" {
		return nil
	}
	r.mu.RLock()
	value, ok := r.turns[turnID]
	r.mu.RUnlock()
	if !ok {
		return nil
	}
	return &value
}

func (r *turnContextRegistry) release(turnID protocol.TurnID) {
	if r == nil || turnID == "" {
		return
	}
	r.mu.Lock()
	delete(r.turns, turnID)
	r.mu.Unlock()
}

type TerminalPhase string

const (
	TerminalPrepared  TerminalPhase = "prepared"
	TerminalCommitted TerminalPhase = "committed"
)

type TerminalOutcome = observation.TerminalOutcome

const (
	TerminalCompleted = observation.TerminalCompleted
	TerminalFailed    = observation.TerminalFailed
	TerminalCanceled  = observation.TerminalCanceled
)

func (r Runtime) Now() time.Time {
	if r.Clock == nil {
		return time.Now()
	}
	return r.Clock()
}

func (r Runtime) Enabled() bool {
	return r.Recorder != nil
}

func (r Runtime) NewTurnRecorder(
	ctx context.Context,
	sessionID, turnID string,
) *Recorder {
	recorder := NewObservedRecorderContext(
		ctx,
		r.Clock,
		r.Recorder,
		observation.Identity{
			RuntimeID: r.RuntimeID,
			SessionID: sessionID,
			TurnID:    protocol.TurnID(turnID),
		},
	)
	typedTurnID := protocol.TurnID(turnID)
	if r.contexts != nil {
		recorder.onRoot = func(value observation.TraceContext) {
			r.contexts.bind(typedTurnID, value)
		}
		recorder.onClose = func() {
			r.contexts.release(typedTurnID)
		}
	}
	return recorder
}

func (r Runtime) ObserveTransition(
	ctx context.Context,
	sessionID, turnID string,
	factSequence uint64,
) {
	if r.Recorder == nil {
		return
	}
	_ = r.Recorder.Record(ctx, observation.Record{
		Kind: observation.KindTurnTransitionCommitted,
		Identity: observation.Identity{
			RuntimeID:    r.RuntimeID,
			SessionID:    sessionID,
			TurnID:       protocol.TurnID(turnID),
			FactSequence: factSequence,
		},
		Trace: tracecontext.ToObservation(ctx),
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	})
}

func (r Runtime) ObserveRecovery(
	ctx context.Context,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	resumeID protocol.OperationID,
	sourceTurnID protocol.TurnID,
) {
	if r.Recorder == nil {
		return
	}
	summary, err := json.Marshal(struct {
		SourceTurnID protocol.TurnID `json:"source_turn_id"`
	}{SourceTurnID: sourceTurnID})
	if err != nil {
		return
	}
	_ = r.Recorder.Record(ctx, observation.Record{
		Kind: observation.KindTurnRecovered,
		Identity: observation.Identity{
			RuntimeID: r.RuntimeID, ThreadID: threadID, TurnID: turnID,
			ResumeID: resumeID,
		},
		Trace:   tracecontext.ToObservation(ctx),
		Summary: summary,
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	})
}

func (r Runtime) ObserveTerminal(
	ctx context.Context,
	phase TerminalPhase,
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	operationID protocol.OperationID,
	effectID string,
	parent string,
	measurementDigest string,
	outcome observation.TerminalOutcome,
) string {
	if r.Recorder == nil {
		return ""
	}
	kind := observation.KindTurnTerminalPrepared
	if phase == TerminalCommitted {
		kind = observation.KindTurnTerminalCommitted
	}
	var causality *observation.Causality
	if parent != "" {
		causality = &observation.Causality{
			ParentObservationID: observation.ObservationID(parent),
		}
	}
	summary, err := observation.EncodeTerminalSummary(
		measurementDigest,
		outcome,
	)
	if err != nil {
		return ""
	}
	traceContext := r.contexts.lookup(turnID)
	if traceContext == nil {
		traceContext = tracecontext.ToObservation(ctx)
	}
	receipt := r.Recorder.Record(ctx, observation.Record{
		Kind: kind,
		Identity: observation.Identity{
			RuntimeID:   r.RuntimeID,
			ThreadID:    threadID,
			TurnID:      turnID,
			OperationID: operationID,
			EffectID:    protocol.EffectID(effectID),
		},
		Trace:     traceContext,
		Causality: causality,
		Summary:   summary,
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	})
	if receipt.Status == observation.AdmissionAccepted ||
		receipt.Status == observation.AdmissionPayloadDropped {
		return string(receipt.ID)
	}
	return ""
}
