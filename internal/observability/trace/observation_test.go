package trace

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestObservedRecorderPersistsStartBeforeEnd(t *testing.T) {
	observer := &recordingObserver{}
	recorder := NewObservedRecorder(
		func() time.Time { return time.Unix(1, 0).UTC() },
		observer,
		observation.Identity{
			RuntimeID: "runtime-test",
			TurnID:    protocol.TurnID("turn-test"),
		},
	)
	root := recorder.Start(NameTurn, 0, nil)
	model := recorder.Start(NameModelCall, root.ID(), map[string]any{
		"sample": uint32(1),
	})
	records := observer.snapshot()
	if len(records) != 2 ||
		records[0].Kind != observation.KindTurnStarted ||
		records[1].Kind != observation.KindModelRequestSent {
		t.Fatalf("start records = %+v", records)
	}
	if records[1].Trace.ParentSpan != records[0].Trace.SpanID ||
		records[1].Causality.ParentObservationID == "" {
		t.Fatalf("model start relation = %+v", records[1])
	}

	model.End(StatusOK)
	records = observer.snapshot()
	if len(records) != 3 ||
		records[2].Kind != observation.KindModelResponseCompleted ||
		records[2].Causality.ParentObservationID == "" {
		t.Fatalf("end records = %+v", records)
	}
}

func TestObservedRecorderLeavesUnendedSpanOpen(t *testing.T) {
	observer := &recordingObserver{}
	recorder := NewObservedRecorder(
		nil,
		observer,
		observation.Identity{
			RuntimeID: "runtime-test",
			TurnID:    protocol.TurnID("turn-test"),
		},
	)
	recorder.Start(NameTool, 0, map[string]any{
		"call_id": "call-1",
		"attempt": uint32(2),
	})
	if records := observer.snapshot(); len(records) != 1 ||
		records[0].Kind != observation.KindToolStarted ||
		records[0].Identity.Attempt != 2 {
		t.Fatalf("records = %+v", records)
	}
	recorder.Close()
	if records := observer.snapshot(); len(records) != 1 {
		t.Fatalf("close fabricated an end observation: %+v", records)
	}
}

func TestObservationFailureDoesNotChangeInMemorySpan(t *testing.T) {
	observer := &recordingObserver{fail: true}
	recorder := NewObservedRecorder(
		nil,
		observer,
		observation.Identity{
			RuntimeID: "runtime-test",
			TurnID:    protocol.TurnID("turn-test"),
		},
	)
	span := recorder.Start(NameTool, 0, map[string]any{"call_id": "call-1"})
	span.End(StatusOK)
	spans := recorder.Spans()
	if len(spans) != 1 || spans[0].Status != StatusOK {
		t.Fatalf("spans = %+v", spans)
	}
}

func TestRuntimeTerminalObservationClosesTurnTraceIdentity(t *testing.T) {
	observer := &recordingObserver{}
	contexts := newTurnContextRegistry()
	runtime := Runtime{
		Recorder: observer, RuntimeID: "runtime-test",
		contexts: contexts,
	}
	recorder := runtime.NewTurnRecorder(
		t.Context(),
		"session-test",
		"turn-test",
	)
	root := recorder.Start(NameTurn, 0, nil)
	turnContext := recorder.Context(t.Context(), root.ID())
	runtime.ObserveTransition(
		turnContext,
		"session-test",
		"turn-test",
		1,
	)
	prepared := runtime.ObserveTerminal(
		t.Context(),
		TerminalPrepared,
		protocol.ThreadID("thread-test"),
		protocol.TurnID("turn-test"),
		protocol.OperationID("operation-test"),
		"terminal:turn-test",
		"",
		"digest-test",
	)
	runtime.ObserveTerminal(
		t.Context(),
		TerminalCommitted,
		protocol.ThreadID("thread-test"),
		protocol.TurnID("turn-test"),
		protocol.OperationID("operation-test"),
		"terminal:turn-test",
		prepared,
		"digest-test",
	)
	records := observer.snapshot()
	if len(records) != 4 {
		t.Fatalf("records = %+v", records)
	}
	rootTrace := records[0].Trace
	for _, index := range []int{1, 2, 3} {
		if records[index].Trace == nil ||
			records[index].Trace.TraceID != rootTrace.TraceID ||
			records[index].Trace.SpanID != rootTrace.SpanID {
			t.Fatalf("root=%+v record[%d]=%+v", rootTrace, index, records[index])
		}
	}
	if records[2].Kind != observation.KindTurnTerminalPrepared ||
		records[3].Kind != observation.KindTurnTerminalCommitted {
		t.Fatalf("terminal records = %+v", records[2:])
	}
	recorder.Close()
	if contexts.lookup(protocol.TurnID("turn-test")) != nil {
		t.Fatal("closed turn retained its trace context")
	}
}

type recordingObserver struct {
	mu      sync.Mutex
	records []observation.Record
	next    byte
	fail    bool
}

func (o *recordingObserver) Record(
	_ context.Context,
	record observation.Record,
) observation.AdmissionReceipt {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, record.Clone())
	if o.fail {
		return observation.AdmissionReceipt{
			Status: observation.AdmissionWriterFailed,
		}
	}
	o.next++
	return observation.AdmissionReceipt{
		Status: observation.AdmissionAccepted,
		ID:     observation.ObservationID(fmt.Sprintf("obs_%032x", o.next)),
	}
}

func (o *recordingObserver) snapshot() []observation.Record {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]observation.Record(nil), o.records...)
}
