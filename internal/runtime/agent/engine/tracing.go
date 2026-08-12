package engine

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
)

// beginTrace opens the recorder and root span for a turn. The previous turn's
// recorder is replaced rather than cleared on the way out, so a caller can still
// read what the last turn spent — the same convention TurnDiff and
// ContextReceipts already follow.
func (e *Engine) beginTrace(purpose model.Purpose) (*trace.Recorder, *trace.Span) {
	route := e.activeRoute()
	recorder := trace.NewRecorder(e.options.Now)
	span := recorder.Start(trace.NameTurn, 0, map[string]any{
		"provider": route.ProviderID(),
		"model":    route.Model().ID,
		"purpose":  string(purpose),
	})
	if scope := e.runningScope(); scope != nil {
		scope.mu.Lock()
		scope.state.recorder = recorder
		scope.state.toolSpans = make(map[string]uint64)
		scope.mu.Unlock()
	}
	return recorder, span
}

// endTrace closes the turn and persists its spans. A sink that fails is counted
// and dropped: a trace nobody could write must not change what the turn did, and
// the turn has already reported its outcome by the time this runs.
func (e *Engine) endTrace(
	ctx context.Context,
	recorder *trace.Recorder,
	turn *trace.Span,
	turnID string,
	state State,
) {
	turn.End(traceStatus(state))
	spans := recorder.Close()
	if e.options.Trace == nil || turnID == "" || len(spans) == 0 {
		return
	}
	if err := e.options.Trace.Write(ctx, turnID, spans); err != nil {
		e.options.Metrics.Error()
	}
}

// tracer is the active turn's recorder. It is nil between turns, and every
// recorder method tolerates that.
func (e *Engine) tracer() *trace.Recorder {
	if e == nil {
		return nil
	}
	scope := e.currentScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return scope.state.recorder
}

// beginToolSpan opens a tool span and remembers it under the call id, which is
// how an approval wait finds the call it belongs to.
func (e *Engine) beginToolSpan(call provider.ToolCall) *trace.Span {
	recorder := e.tracer()
	span := recorder.Start(trace.NameTool, 0, map[string]any{
		"tool": call.Name, "call_id": call.ID,
	})
	if span.ID() == 0 || call.ID == "" {
		return span
	}
	if scope := e.runningScope(); scope != nil {
		scope.mu.Lock()
		if scope.state.toolSpans != nil {
			scope.state.toolSpans[call.ID] = span.ID()
		}
		scope.mu.Unlock()
	}
	return span
}

// endToolSpan closes a tool span. A tool that returned an error result is a
// failed span even though the call itself completed: the span reports what the
// turn got, not whether the plumbing worked.
func (e *Engine) endToolSpan(
	call provider.ToolCall, span *trace.Span, result tool.Result, err error,
) {
	if call.ID != "" {
		if scope := e.runningScope(); scope != nil {
			scope.mu.Lock()
			delete(scope.state.toolSpans, call.ID)
			scope.mu.Unlock()
		}
	}
	switch {
	case err != nil:
		span.Set("error", errorText(err))
		span.End(trace.StatusError)
	case result.IsError:
		span.End(trace.StatusError)
	default:
		span.End(trace.StatusOK)
	}
}

// observeApprovalWait records how long a tool sat waiting for a human. The guard
// reports the stretch because only the guard sees both ends of it: the engine
// emits the request and then hears nothing until the tool returns, by which time
// the tool has also run.
func (e *Engine) observeApprovalWait(wait toolguard.ApprovalWait) {
	scope := e.runningScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	recorder, parent := scope.state.recorder, scope.state.toolSpans[wait.CallID]
	scope.mu.Unlock()
	if recorder == nil {
		return
	}
	// The wait ended when the guard reported it, so the stretch is placed
	// backwards from now on the engine's own clock rather than trusting a second
	// clock's absolute times.
	ended := e.options.Now()
	status := trace.StatusOK
	if wait.Outcome != toolguard.ApprovalWaitDecided {
		status = trace.StatusError
	}
	recorder.Add(
		trace.NameApprovalWait, parent, ended.Add(-wait.Waited), ended, status,
		map[string]any{
			"tool": wait.Tool, "call_id": wait.CallID,
			"request_id": wait.RequestID, "outcome": string(wait.Outcome),
		},
	)
}

// TurnLatency reports where the last turn spent its wall clock. A nil return
// means nothing was measured, which is what separates "this turn had no
// approvals" from "this engine does not time approvals".
func (e *Engine) TurnLatency() *trace.Latency {
	recorder := e.tracer()
	if recorder == nil {
		return nil
	}
	latency := recorder.Latency()
	return &latency
}

// TurnSpans is the last turn's span tree.
func (e *Engine) TurnSpans() []trace.Record {
	return e.tracer().Spans()
}

func traceStatus(state State) trace.Status {
	switch state {
	case Completed:
		return trace.StatusOK
	case Canceled:
		return trace.StatusCanceled
	default:
		return trace.StatusError
	}
}
