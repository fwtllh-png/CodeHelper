package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestRuntimeEmitsTurnCompactionOnPreSamplingGate(t *testing.T) {
	worker, err := newTestAgentEngine(agentengine.Options{
		Provider: &threadEchoProvider{}, Route: runtimeTestRoute(t),
		Tools: tool.NewRegistry(nil, nil), Metrics: telemetry.NewMetrics(),
		MaxOutputTokens: 128, CompactWindow: agentengine.CompactWindowPolicy{
			AutoTokens: 300,
		}, SummaryMaxBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.ReplaceHistory([]provider.Message{
		{Role: provider.RoleUser, Turn: 1, Blocks: []provider.ContentBlock{{
			Type: provider.ContentText, Text: strings.Repeat("old ", 100),
		}}},
		{Role: provider.RoleAssistant, Turn: 1, Blocks: []provider.ContentBlock{{
			Type: provider.ContentText, Text: strings.Repeat("answer ", 100),
		}}},
	})
	runtime := NewRuntime(Options{Engine: AdaptEngine(worker)})
	defer runtime.Close(context.Background())
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "item", Prompt: "new request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(3 * time.Second)
	var sawCompaction bool
	for {
		select {
		case event := <-events:
			if event.Kind == protocol.EventTurnCompaction {
				data, ok := event.Data.(*protocol.TurnCompactionData)
				if !ok || data.Phase != agentengine.CompactionPhasePreSampling || data.Summary == "" {
					t.Fatalf("turn.compaction = %#v", event.Data)
				}
				sawCompaction = true
			}
			if protocol.IsTerminalEvent(event.Kind) {
				if !sawCompaction {
					t.Fatal("expected turn.compaction before terminal")
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for turn compaction")
		}
	}
}
