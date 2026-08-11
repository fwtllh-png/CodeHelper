package app

import (
	"context"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestRuntimeCompactThreadEmitsThreadCompacted(t *testing.T) {
	seed, err := newTestAgentEngine(agentengine.Options{
		Provider: &threadEchoProvider{}, Route: runtimeTestRoute(t),
		Tools: tool.NewRegistry(nil, nil), Metrics: telemetry.NewMetrics(),
		MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewThreadManager(func() (*EngineAdapter, error) {
		clone, err := seed.CloneEmpty()
		if err != nil {
			return nil, err
		}
		return AdaptEngine(clone), nil
	})
	runtime := NewRuntime(Options{Engine: manager})
	defer runtime.Close(context.Background())

	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}

	for _, turn := range []struct {
		turnID, itemID, prompt string
	}{
		{"turn-1", "item-1", "first turn prompt for compact"},
		{"turn-2", "item-2", "second turn prompt for compact"},
	} {
		start, err := protocol.NewOperation(&protocol.StartTurnPayload{
			ThreadID: "thread-1", TurnID: protocol.TurnID(turn.turnID),
			ItemID: protocol.ItemID(turn.itemID), Prompt: turn.prompt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Submit(t.Context(), start); err != nil {
			t.Fatal(err)
		}
		waitTerminal(t, events, 2*time.Second)
	}

	before, err := manager.History("thread-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(before) < 2 {
		t.Fatalf("history before compact = %d messages", len(before))
	}

	compact, err := protocol.NewOperation(&protocol.CompactThreadPayload{
		ThreadID: "thread-1", TurnID: "turn-2", ItemID: "compact-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), compact); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Kind == protocol.EventOperationRejected {
				t.Fatalf("compact rejected: %#v", event.Data)
			}
			if event.Kind != protocol.EventThreadCompacted {
				continue
			}
			data, ok := event.Data.(*protocol.ThreadCompactedData)
			if !ok || data.Summary == "" {
				t.Fatalf("compacted = %#v", event.Data)
			}
			after, err := manager.History("thread-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(after) == 0 {
				t.Fatal("history empty after compact")
			}
			return
		case <-deadline:
			t.Fatal("thread.compacted was not emitted")
		}
	}
}

func waitTerminal(t *testing.T, events <-chan protocol.Event, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case event := <-events:
			if protocol.IsTerminalEvent(event.Kind) {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for terminal turn event")
		}
	}
}
