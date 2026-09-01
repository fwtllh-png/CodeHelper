package assembly

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
)

func TestResponseAssemblyPersistsIncompleteParallelToolFragments(t *testing.T) {
	assembly := NewResponseAssembly("sample-1")
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID: "sample-1", TransportRequestID: "request-1",
		Attempt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	events := []StreamEvent{
		{Type: EventMessageStart, Sequenced: true, Sequence: 0},
		{
			Type: EventToolCallDelta, Sequenced: true, Sequence: 1,
			ToolCall: &ToolCallFragment{
				Index: 0, ID: "call-1", Name: "read",
				Arguments: `{"path":"a.go"}`,
			},
		},
		{
			Type: EventToolCallDelta, Sequenced: true, Sequence: 2,
			ToolCall: &ToolCallFragment{
				Index: 1, ID: "call-2", Name: "write",
				Arguments: `{"path":"b.go","content":`,
			},
		},
		{
			Type: EventMessageStop, Sequenced: true, Sequence: 3,
			StopReason: StopReasonMaxTokens,
		},
	}
	for _, event := range events {
		applied, err := assembly.Apply(event)
		if err != nil {
			t.Fatal(err)
		}
		if !applied {
			t.Fatalf("event %+v was not applied", event)
		}
	}
	if assembly.State != ResponseIncomplete {
		t.Fatalf("state = %q", assembly.State)
	}
	if calls, err := assembly.ExecutableToolCalls(); err == nil ||
		len(calls) != 0 {
		t.Fatalf("incomplete calls = %+v, error = %v", calls, err)
	}
	fragments := assembly.IncompleteToolFragments()
	if len(fragments) != 2 ||
		fragments[0].Arguments != `{"path":"a.go"}` ||
		fragments[1].Arguments != `{"path":"b.go","content":` {
		t.Fatalf("fragments = %+v", fragments)
	}
	data, err := json.Marshal(assembly)
	if err != nil {
		t.Fatal(err)
	}
	var restored ResponseAssembly
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := restored.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := restored.ValidateExtension(assembly); err != nil {
		t.Fatal(err)
	}
}

func TestResponseAssemblyDeduplicatesAndRejectsReorderedEvents(t *testing.T) {
	assembly := NewResponseAssembly("sample-1")
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID: "sample-1", TransportRequestID: "request-1",
		Attempt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	first := StreamEvent{
		Type: EventTextDelta, Text: "one",
		Sequenced: true, Sequence: 7,
	}
	if applied, err := assembly.Apply(first); err != nil || !applied {
		t.Fatalf("first apply = %t, %v", applied, err)
	}
	if applied, err := assembly.Apply(first); err != nil || applied {
		t.Fatalf("duplicate apply = %t, %v", applied, err)
	}
	reordered := StreamEvent{
		Type: EventTextDelta, Text: "old",
		Sequenced: true, Sequence: 6,
	}
	if _, err := assembly.Apply(reordered); err == nil {
		t.Fatal("reordered event was accepted")
	}
	conflict := first
	conflict.Text = "changed"
	if _, err := assembly.Apply(conflict); err == nil {
		t.Fatal("duplicate identity with changed payload was accepted")
	}
	if got := assembly.CurrentBlocks(); len(got) != 1 ||
		got[0].Text != "one" {
		t.Fatalf("blocks = %+v", got)
	}
}

func TestResponseAssemblyRejectsDurablePrefixRewrites(t *testing.T) {
	assembly := NewResponseAssembly("sample-prefix")
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID: "sample-prefix", TransportRequestID: "request-1",
		Attempt: 1, RequestBytes: 10,
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []StreamEvent{
		{Type: EventTextDelta, Text: "one"},
		{
			Type: EventToolCallDelta,
			ToolCall: &ToolCallFragment{
				Index: 0, ID: "call-1", Name: "read", Arguments: `{"a":1}`,
			},
		},
		{
			Type: EventReplayState,
			Replay: &ReplayState{
				Version: 1,
				Data:    json.RawMessage(`{"state":"one"}`),
			},
		},
		{
			Type: EventResponseState,
			Response: &ResponseState{
				ID: "response-1",
				Output: []json.RawMessage{
					json.RawMessage(`{"type":"one"}`),
				},
			},
		},
	} {
		if _, err := assembly.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name   string
		mutate func(*ResponseAssembly)
	}{
		{
			name: "transport",
			mutate: func(value *ResponseAssembly) {
				value.Segments[0].Transport.RequestBytes = 11
			},
		},
		{
			name: "block",
			mutate: func(value *ResponseAssembly) {
				value.Segments[0].Blocks[0].Text = "two"
			},
		},
		{
			name: "tool fragment",
			mutate: func(value *ResponseAssembly) {
				value.Segments[0].ToolFragments[0].Arguments = `{"a":2}`
			},
		},
		{
			name: "replay",
			mutate: func(value *ResponseAssembly) {
				value.Segments[0].Replay.Data =
					json.RawMessage(`{"state":"two"}`)
			},
		},
		{
			name: "response",
			mutate: func(value *ResponseAssembly) {
				value.Segments[0].Response.Output[0] =
					json.RawMessage(`{"type":"two"}`)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := CloneResponseAssembly(assembly)
			test.mutate(mutated)
			if err := mutated.ValidateExtension(assembly); err == nil {
				t.Fatal("durable prefix rewrite was accepted")
			}
		})
	}
	extended := CloneResponseAssembly(assembly)
	if _, err := extended.Apply(StreamEvent{
		Type: EventTextDelta,
		Text: " continued",
	}); err != nil {
		t.Fatal(err)
	}
	if err := extended.ValidateExtension(assembly); err != nil {
		t.Fatalf("legitimate extension rejected: %v", err)
	}
}

func TestResponseAssemblyAcceptsSparseMonotonicProviderSequences(t *testing.T) {
	assembly := NewResponseAssembly("sample-sparse")
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID:   "sample-sparse",
		TransportRequestID: "request-sparse",
		Attempt:            1,
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []StreamEvent{
		{
			Type: EventReasoningDelta, Text: "inspect",
			Sequenced: true, Sequence: 350,
		},
		{
			Type: EventToolCallDelta,
			ToolCall: &ToolCallFragment{
				Index: 0, ID: "call-1", Name: "read",
			},
			Sequenced: true, Sequence: 377,
		},
		{
			Type: EventToolCallDelta,
			ToolCall: &ToolCallFragment{
				Index: 0, Arguments: `{"path":"README.md"}`,
			},
			Sequenced: true, Sequence: 379,
		},
		{
			Type: EventMessageStop, StopReason: StopReasonToolUse,
			Sequenced: true, Sequence: 380,
		},
	} {
		if applied, err := assembly.Apply(event); err != nil || !applied {
			t.Fatalf("apply %+v = %t, %v", event, applied, err)
		}
	}
	calls, err := assembly.ExecutableToolCalls()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Name != "read" {
		t.Fatalf("calls = %+v", calls)
	}
	if segment := assembly.Segments[0]; !segment.HasSequence ||
		segment.LastSequence != 380 {
		t.Fatalf("segment = %+v", segment)
	}
}

func TestResponseAssemblyPreservesRepeatedReasoningDeltas(t *testing.T) {
	assembly := NewResponseAssembly("sample-repeated-reasoning")
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID:   "sample-repeated-reasoning",
		TransportRequestID: "request-repeated-reasoning",
		Attempt:            1,
	}); err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 3; sequence++ {
		if _, err := assembly.Apply(StreamEvent{
			Type: EventReasoningDelta, Text: "x",
			Sequenced: true, Sequence: sequence,
		}); err != nil {
			t.Fatal(err)
		}
	}
	blocks := assembly.CurrentBlocks()
	if len(blocks) != 1 || blocks[0].Text != "xxx" {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestResponseAssemblySeparatesInterruptedAndCompleteTransport(t *testing.T) {
	assembly := NewResponseAssembly("sample-1")
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID: "sample-1", TransportRequestID: "request-1",
		Attempt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	_, _ = assembly.Apply(StreamEvent{
		Type: EventTextDelta, Text: "partial",
	})
	_, _ = assembly.Apply(StreamEvent{
		Type: EventToolCallDelta,
		ToolCall: &ToolCallFragment{
			Index: 0, ID: "call-1", Name: "read",
			Arguments: `{"path":`,
		},
	})
	if err := assembly.Interrupt(errors.New("connection reset")); err != nil {
		t.Fatal(err)
	}
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID: "sample-1", TransportRequestID: "request-2",
		Attempt: 2,
	}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []StreamEvent{
		{
			Type: EventToolCallDelta,
			ToolCall: &ToolCallFragment{
				Index: 0, ID: "call-1", Name: "read",
				Arguments: `{"path":"a.go"}`,
			},
		},
		{Type: EventMessageStop, StopReason: StopReasonToolUse},
	} {
		if _, err := assembly.Apply(event); err != nil {
			t.Fatal(err)
		}
	}
	calls, err := assembly.ExecutableToolCalls()
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Arguments != `{"path":"a.go"}` {
		t.Fatalf("calls = %+v", calls)
	}
	if fragments := assembly.IncompleteToolFragments(); len(fragments) != 1 ||
		fragments[0].Arguments != `{"path":` {
		t.Fatalf("incomplete fragments = %+v", fragments)
	}
	if got := assembly.TotalUsage(); got != (Usage{}) {
		t.Fatalf("usage = %+v", got)
	}
}

func TestResponseAssemblyAttributesUsageAcrossTransportAttempts(t *testing.T) {
	assembly := NewResponseAssembly("sample-usage")
	for attempt, usage := range []Usage{
		{InputTokens: 10, OutputTokens: 2},
		{InputTokens: 12, OutputTokens: 3},
	} {
		if err := assembly.BeginTransport(TransportMetadata{
			LogicalRequestID:   "sample-usage",
			TransportRequestID: "transport-" + string(rune('1'+attempt)),
			Attempt:            uint32(attempt + 1),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := assembly.Apply(StreamEvent{
			Type: EventUsage, Usage: &usage,
		}); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			if err := assembly.Interrupt(errors.New("disconnect")); err != nil {
				t.Fatal(err)
			}
		} else if _, err := assembly.Apply(StreamEvent{
			Type: EventMessageStop, StopReason: StopReasonEndTurn,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if got := assembly.TotalUsage(); got.InputTokens != 22 ||
		got.OutputTokens != 5 {
		t.Fatalf("usage = %+v", got)
	}
	if len(assembly.Segments) != 2 ||
		assembly.Segments[0].Transport.LogicalRequestID !=
			assembly.Segments[1].Transport.LogicalRequestID ||
		assembly.Segments[0].Transport.TransportRequestID ==
			assembly.Segments[1].Transport.TransportRequestID {
		t.Fatalf("segments = %+v", assembly.Segments)
	}
}

func TestResponseAssemblyRetainsLatestCumulativeUsageWithinTransport(t *testing.T) {
	assembly := NewResponseAssembly("sample-cumulative")
	if err := assembly.BeginTransport(TransportMetadata{
		LogicalRequestID: "sample-cumulative", TransportRequestID: "transport-1",
	}); err != nil {
		t.Fatal(err)
	}
	for _, usage := range []Usage{
		{InputTokens: 10},
		{OutputTokens: 2},
		{InputTokens: 10, OutputTokens: 2},
		{InputTokens: 10, OutputTokens: 2},
	} {
		if _, err := assembly.Apply(StreamEvent{Type: EventUsage, Usage: &usage}); err != nil {
			t.Fatal(err)
		}
	}
	if got := assembly.CurrentUsage(); got.InputTokens != 10 || got.OutputTokens != 2 {
		t.Fatalf("usage = %+v, want max-consistent cumulative snapshot", got)
	}
}

func TestResponseAssemblyDisconnectAtEveryBoundaryRetainsConfirmedData(
	t *testing.T,
) {
	events := []StreamEvent{
		{Type: EventTextDelta, Text: "answer"},
		{Type: EventReasoningDelta, Text: "reason"},
		{
			Type: EventToolCallDelta,
			ToolCall: &ToolCallFragment{
				Index: 0, ID: "call-1", Name: "read",
				Arguments: `{"path":`,
			},
		},
		{
			Type: EventToolCallDelta,
			ToolCall: &ToolCallFragment{
				Index: 1, ID: "call-2", Name: "search",
				Arguments: `{"query":"R3"}`,
			},
		},
		{
			Type:  EventUsage,
			Usage: &Usage{InputTokens: 11, OutputTokens: 4},
		},
	}
	for boundary := 0; boundary <= len(events); boundary++ {
		t.Run(fmt.Sprintf("boundary-%d", boundary), func(t *testing.T) {
			assembly := NewResponseAssembly("sample-boundary")
			if err := assembly.BeginTransport(TransportMetadata{
				LogicalRequestID:   "sample-boundary",
				TransportRequestID: "transport-1",
				Attempt:            1,
			}); err != nil {
				t.Fatal(err)
			}
			for _, event := range events[:boundary] {
				if _, err := assembly.Apply(event); err != nil {
					t.Fatal(err)
				}
			}
			if err := assembly.Interrupt(io.ErrUnexpectedEOF); err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(assembly)
			if err != nil {
				t.Fatal(err)
			}
			var restored ResponseAssembly
			if err := json.Unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if err := restored.Validate(); err != nil {
				t.Fatal(err)
			}
			if restored.EventCount() != uint64(boundary) ||
				restored.State != ResponseIncomplete {
				t.Fatalf(
					"boundary=%d restored=%+v",
					boundary,
					restored,
				)
			}
			wantBlocks := 0
			if boundary >= 1 {
				wantBlocks++
			}
			if boundary >= 2 {
				wantBlocks++
			}
			if len(restored.ConfirmedBlocks()) != wantBlocks {
				t.Fatalf(
					"boundary=%d blocks=%+v",
					boundary,
					restored.ConfirmedBlocks(),
				)
			}
			wantFragments := max(0, min(boundary-2, 2))
			if len(restored.IncompleteToolFragments()) !=
				wantFragments {
				t.Fatalf(
					"boundary=%d fragments=%+v",
					boundary,
					restored.IncompleteToolFragments(),
				)
			}
			if boundary == len(events) &&
				(restored.TotalUsage().InputTokens != 11 ||
					restored.TotalUsage().OutputTokens != 4) {
				t.Fatalf(
					"boundary=%d usage=%+v",
					boundary,
					restored.TotalUsage(),
				)
			}
		})
	}
}
