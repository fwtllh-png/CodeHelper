package engine

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
)

func TestTokenWindowIncludesStableDynamicToolsAndOutputReserve(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	input := contextstore.New(contextstore.Input{
		Stable:  []provider.Message{provider.TextMessage(provider.RoleSystem, strings.Repeat("s", 400))},
		History: []provider.Message{provider.TextMessage(provider.RoleUser, strings.Repeat("h", 400))},
		Dynamic: []provider.Message{provider.TextMessage(provider.RoleSystem, strings.Repeat("d", 400))},
		Definitions: []provider.ToolDefinition{{
			Name: "lookup", Description: strings.Repeat("schema", 100),
		}},
	}).Snapshot()
	total, err := engine.measureTokenWindow(input, 128)
	if err != nil {
		t.Fatal(err)
	}
	engine.options.CompactWindow.Scope = compactScopeBodyAfterPrefix
	body, err := engine.measureTokenWindow(input, 128)
	if err != nil {
		t.Fatal(err)
	}
	if total.total != total.estimated+128 || total.active <= body.active ||
		body.active <= 128 {
		t.Fatalf("total=%+v body=%+v", total, body)
	}
}

func TestTokenWindowCalibratesFromPriorActualUsage(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	attachTestScope(t, engine)
	engine.noteInputUsage(100, 150)
	if got := engine.calibrateInput(200); got != 300 {
		t.Fatalf("calibrated input = %d, want 300", got)
	}
	engine.noteInputUsage(100, 1000)
	if got := engine.calibrateInput(200); got != 400 {
		t.Fatalf("bounded calibration = %d, want 400", got)
	}
}

func TestBodyScopeStillCompactsBeforeTheHardTotalWindow(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.CompactWindow.Scope = compactScopeBodyAfterPrefix
	engine.options.SummaryMaxBytes = 100
	history := []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("h", 4000), 1),
		messageWithText(provider.RoleUser, "current", 2),
	}
	var receipt *CompactionReceipt
	input := contextstore.New(contextstore.Input{
		Stable: []provider.Message{
			provider.TextMessage(provider.RoleSystem, strings.Repeat("s", 13_000)),
		},
	}).Snapshot()
	window, err := engine.runCompactGate(&history, input, 128, CompactionPhasePreSampling, false, func(_ State, event Event) error {
		receipt = event.Compaction
		return nil
	})
	if err != nil || receipt == nil || window.total > window.hardLimit {
		t.Fatalf("window=%+v receipt=%+v error=%v", window, receipt, err)
	}
}

func TestTokenWindowFinishOnlyRemovesAllReadOnlyToolsAtEightyFivePercent(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{textStream("done")}}
	engine := newEngine(t, runtime, declarationRegistry(t, true))

	if _, err := engine.Run(t.Context(), strings.Repeat("x", 12_100), nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("requests = %+v", runtime.requests)
	}
	if len(runtime.requests[0].Tools) != 0 {
		t.Fatalf("requests = %+v, want no tools", runtime.requests)
	}
}
