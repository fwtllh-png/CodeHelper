package engine

import (
	"io"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
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
	stream := newDeltaCoalescingStream(&provider.SliceStream{Events: events})
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
	source := &provider.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventReasoningDelta, Index: 0, Text: "a"},
		{Type: provider.EventReasoningDelta, Index: 0, Text: "b"},
		{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
			Index: 0, ID: "call_1", Name: "read",
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
		provider.EventReasoningDelta,
		provider.EventToolCallDelta,
		provider.EventTextDelta,
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
	if events[0].Text != "a" || events[1].Text != "b" ||
		events[3].Text != "c" || events[4].Text != "d" {
		t.Fatalf("boundary text = %+v", events)
	}
}
