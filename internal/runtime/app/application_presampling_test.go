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
	worker, err := newTestAgentEngine(agentengine.Options{ProviderConfig: agentengine.ProviderConfig{Provider: &threadEchoProvider{}, Route: runtimeTestRoute(t),

		MaxOutputTokens: 128}, ContextConfig: agentengine.ContextConfig{Context: agentengine.ContextPolicy{
		Window: agentengine.CompactWindowPolicy{AutoTokens: 500},
	}, SummaryMaxBytes: 2 << 10}, ToolConfig: agentengine.ToolConfig{Tools: tool.NewRegistry(nil, nil)}, TelemetryConfig: agentengine.TelemetryConfig{Metrics: telemetry.NewMetrics()},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker.ReplaceHistory([]provider.Message{
		{Role: provider.RoleUser, Turn: 1, Blocks: []provider.ContentBlock{{
			Type: provider.ContentText, Text: strings.Repeat("old ", 200),
		}}},
		{Role: provider.RoleAssistant, Turn: 1, Blocks: []provider.ContentBlock{{
			Type: provider.ContentText, Text: strings.Repeat("answer ", 200),
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
	var sawFold bool
	for {
		select {
		case event := <-events:
			if event.Kind == protocol.EventTurnCompaction {
				data, ok := event.Data.(*protocol.TurnCompactionData)
				if ok && data.Phase == agentengine.CompactionPhasePreSampling &&
					data.Status == "folded" && data.Mode == "view" &&
					data.Summary != "" && data.RetainedBytes < data.OriginalBytes {
					sawFold = true
				}
			}
			if protocol.IsTerminalEvent(event.Kind) {
				if !sawFold {
					t.Fatal("expected pre-sampling visible tail fold")
				}
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for turn compaction")
		}
	}
}
