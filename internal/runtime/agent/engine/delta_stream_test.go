package engine

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
)

func TestDeltaCoalescingStreamPreservesTextAndReducesEvents(t *testing.T) {
	const count = 10_000
	events := make([]provider.StreamEvent, 0, count+1)
	for range count {
		events = append(events, provider.StreamEvent{
			Type: provider.EventTextDelta,
			Text: "x",
			Block: &provider.ContentBlock{
				Type: provider.ContentText,
				Text: "x",
			},
		})
	}
	events = append(events, provider.StreamEvent{Type: provider.EventMessageStop})
	stream := newDeltaCoalescingStream(&providerfixture.SliceStream{Events: events})
	defer stream.Close()

	var text strings.Builder
	var deltas int
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Type == provider.EventTextDelta {
			deltas++
			text.WriteString(event.Text)
		}
	}
	if text.Len() != count || text.String() != strings.Repeat("x", count) {
		t.Fatalf("coalesced text length = %d", text.Len())
	}
	if deltas >= count/20 {
		t.Fatalf("delta events = %d, want at least 95%% reduction", deltas)
	}
}

func TestDeltaCoalescingStreamFlushesBeforeEventBoundaries(t *testing.T) {
	source := &providerfixture.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventReasoningDelta, Index: 0, Text: "a"},
		{Type: provider.EventReasoningDelta, Index: 0, Text: "b"},
		{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
			Index: 0, ID: "call_1", Name: "read", Arguments: `{"path":`,
		}},
		{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
			Index: 0, Arguments: `"one"}`,
		}},
		{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
			Index: 1, ID: "call_2", Name: "read", Arguments: `{"path":"two"}`,
		}},
		{Type: provider.EventTextDelta, Index: 1, Text: "c"},
		{Type: provider.EventTextDelta, Index: 1, Text: "d"},
		{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 1}},
		{Type: provider.EventMessageStop},
	}}
	stream := newDeltaCoalescingStream(source)
	defer stream.Close()
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]provider.StreamEventType, len(events))
	for index, event := range events {
		got[index] = event.Type
	}
	want := []provider.StreamEventType{
		provider.EventReasoningDelta,
		provider.EventToolCallDelta,
		provider.EventToolCallDelta,
		provider.EventTextDelta,
		provider.EventUsage,
		provider.EventMessageStop,
	}
	if len(got) != len(want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event types = %v, want %v", got, want)
		}
	}
	if events[0].Text != "ab" || events[3].Text != "cd" {
		t.Fatalf("boundary text = %+v", events)
	}
	if events[1].ToolCall == nil ||
		events[1].ToolCall.ID != "call_1" ||
		events[1].ToolCall.Name != "read" ||
		events[1].ToolCall.Arguments != `{"path":"one"}` {
		t.Fatalf("coalesced Tool Call = %+v", events[1].ToolCall)
	}
	if events[2].ToolCall == nil || events[2].ToolCall.ID != "call_2" {
		t.Fatalf("parallel Tool Call boundary = %+v", events[2].ToolCall)
	}
}

func TestDeltaCoalescingStreamFlushesWhenProviderPauses(t *testing.T) {
	source := &pausingDeltaStream{closed: make(chan struct{})}
	stream := newDeltaCoalescingStream(source)
	defer stream.Close()

	started := time.Now()
	event, err := stream.Recv()
	if err != nil {
		t.Fatal(err)
	}
	if event.Type != provider.EventReasoningDelta ||
		event.Text != "confirmed" {
		t.Fatalf("event = %+v", event)
	}
	if elapsed := time.Since(started); elapsed > 10*deltaFlushWindow {
		t.Fatalf("flush took %v, want <= %v", elapsed, 10*deltaFlushWindow)
	}
}

func TestDeltaCoalescingStreamReducesFineGrainedToolArguments(t *testing.T) {
	const count = 10_000
	events := make([]provider.StreamEvent, 0, count+2)
	events = append(events, provider.StreamEvent{
		Type: provider.EventToolCallDelta,
		ToolCall: &provider.ToolCallFragment{
			Index: 0, ID: "call_1", Name: "read",
		},
	})
	for range count {
		events = append(events, provider.StreamEvent{
			Type: provider.EventToolCallDelta,
			ToolCall: &provider.ToolCallFragment{
				Index: 0, Arguments: "x",
			},
		})
	}
	events = append(events, provider.StreamEvent{Type: provider.EventMessageStop})
	stream := newDeltaCoalescingStream(&providerfixture.SliceStream{Events: events})
	defer stream.Close()

	var arguments strings.Builder
	var deltas int
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if event.Type != provider.EventToolCallDelta {
			continue
		}
		deltas++
		if event.ToolCall == nil {
			t.Fatal("coalesced Tool Call fragment is nil")
		}
		arguments.WriteString(event.ToolCall.Arguments)
	}
	if arguments.String() != strings.Repeat("x", count) {
		t.Fatalf("coalesced arguments length = %d", arguments.Len())
	}
	if deltas >= count/20 {
		t.Fatalf("Tool Call delta events = %d, want at least 95%% reduction", deltas)
	}
}

type pausingDeltaStream struct {
	once   sync.Once
	closed chan struct{}
	calls  int
}

func (s *pausingDeltaStream) Recv() (provider.StreamEvent, error) {
	s.calls++
	if s.calls == 1 {
		return provider.StreamEvent{
			Type: provider.EventReasoningDelta,
			Text: "confirmed",
		}, nil
	}
	<-s.closed
	return provider.StreamEvent{}, io.EOF
}

func (s *pausingDeltaStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}
