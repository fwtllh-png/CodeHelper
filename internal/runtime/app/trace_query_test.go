package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestProjectTraceTurnUsesPublicKindsAndAllowlistedIdentity(t *testing.T) {
	started := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	projected := projectTraceTurn("turn-1", []tracestate.Record{
		{
			ID: 1, Name: tracestate.NameTurn, Started: started,
			Ended: started.Add(time.Second), Status: tracestate.StatusOK,
			Attributes: map[string]any{
				"prompt": "secret prompt",
			},
		},
		{
			ID: 2, ParentID: 1, Name: tracestate.NameTool, Started: started,
			Ended: started.Add(250 * time.Millisecond), Status: tracestate.StatusOK,
			Attributes: map[string]any{
				"call_id": "call-1",
				"tool":    "file_read",
				"output":  "secret output",
			},
		},
		{
			ID: 3, ParentID: 1, Name: tracestate.NameTurnKernelTransition,
			Started: started, Ended: started, Status: tracestate.StatusOK,
		},
	})

	if projected.Status != "ok" || len(projected.Spans) != 2 {
		t.Fatalf("projected trace = %+v", projected)
	}
	if projected.Spans[1].Kind != "tool" ||
		projected.Spans[1].CallID != "call-1" ||
		projected.Spans[1].DurationMS == nil ||
		*projected.Spans[1].DurationMS != 250 {
		t.Fatalf("tool span = %+v", projected.Spans[1])
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("trace query leaked non-allowlisted attributes: %s", encoded)
	}
}

func TestUnsigned32RejectsOverflowAndFractions(t *testing.T) {
	for _, value := range []any{-1, 1.5, uint64(1) << 40, "1"} {
		if _, ok := unsigned32(value); ok {
			t.Fatalf("unsigned32(%v) accepted invalid value", value)
		}
	}
	if value, ok := unsigned32(uint32(7)); !ok || value != 7 {
		t.Fatalf("unsigned32 valid = %d, %t", value, ok)
	}
}

func TestTraceServiceValidatesSessionOwnershipAndDeduplicatesTurns(t *testing.T) {
	lifecycle := &memorySessionLifecycleStore{summary: protocol.SessionSummary{
		Version:   protocol.SessionLifecycleVersion,
		Revision:  1,
		SessionID: "session-1",
		ThreadID:  "thread-1",
		Status:    protocol.SessionStatusIdle,
	}}
	store := &traceQueryStore{records: map[protocol.TurnID][]tracestate.Record{
		"turn-1": {{
			ID: 1, Name: tracestate.NameTurn,
			Started: time.Now().UTC(), Status: tracestate.StatusOpen,
		}},
	}}
	runtime := NewRuntime(Options{
		SessionLifecycle: lifecycle,
		TraceStore:       store,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })

	result, err := runtime.TraceService.Query(t.Context(), TraceQuery{
		SessionID: "session-1",
		TurnIDs:   []protocol.TurnID{"turn-1", "turn-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 1 || store.calls != 1 {
		t.Fatalf("result=%+v calls=%d", result, store.calls)
	}

	store.foreign = true
	if _, err := runtime.TraceService.Query(t.Context(), TraceQuery{
		SessionID: "session-1",
		TurnIDs:   []protocol.TurnID{"turn-foreign"},
	}); err == nil {
		t.Fatal("foreign turn was accepted")
	}
}

type traceQueryStore struct {
	records map[protocol.TurnID][]tracestate.Record
	calls   int
	foreign bool
}

func (s *traceQueryStore) QueryTurnInSession(
	_ context.Context,
	_ string,
	turnID protocol.TurnID,
) ([]tracestate.Record, error) {
	s.calls++
	if s.foreign {
		return nil, tracestate.ErrNotFound
	}
	records, ok := s.records[turnID]
	if !ok {
		return nil, errors.New("trace unavailable")
	}
	return records, nil
}
