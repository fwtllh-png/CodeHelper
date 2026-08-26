package engine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func TestCompactGatePrunesToolResultBeforeSummaryReplacement(t *testing.T) {
	results := tool.NewResultStore(32 << 10)
	engine := newEngine(
		t,
		&scriptedProvider{},
		tool.NewRegistry(nil, results),
	)
	engine.options.Context.Window.AutoTokens = 1800
	content := "HEAD-" + strings.Repeat("model-visible-result ", 700) + "-TAIL"
	encoded, err := json.Marshal(tool.Result{Content: content})
	if err != nil {
		t.Fatal(err)
	}
	history := []provider.Message{
		messageWithText(provider.RoleUser, "inspect the output", 1),
		toolCallMessage(1, "call-large", "file_read", `{"path":"large.txt"}`),
		toolResultMessage(1, "call-large", string(encoded)),
	}
	original := cloneMessages(history)
	var receipt *CompactionReceipt
	window, err := engine.runCompactGate(
		t.Context(),
		&history,
		agentcontext.NewMessageLedger(agentcontext.LedgerInput{}).Snapshot(),
		128,
		CompactionPhaseMidTurn,
		true,
		func(_ State, event Event) error {
			receipt = event.Compaction
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil ||
		receipt.RemovedMessages != 0 ||
		receipt.PrunedToolResults != 1 ||
		receipt.PrunedBytes <= 0 ||
		receipt.TruncationReason != "tool_result_surface_pruning" ||
		receipt.AuthorityDigest == "" ||
		!receipt.AuthorityEquivalent ||
		window.active > window.compactLimit {
		t.Fatalf("window=%+v receipt=%+v", window, receipt)
	}
	if len(history) != len(original) ||
		messageToolCalls(history[1])[0].ID != "call-large" ||
		messageToolResultID(history[2]) != "call-large" {
		t.Fatalf("tool pairing changed: %+v", history)
	}
	var projected tool.Result
	if err := json.Unmarshal(
		[]byte(history[2].Blocks[0].ToolResult.Content),
		&projected,
	); err != nil {
		t.Fatal(err)
	}
	if projected.Handle == "" ||
		!strings.Contains(projected.Content, "HEAD-") ||
		!strings.Contains(projected.Content, "-TAIL") {
		t.Fatalf("projected result = %+v", projected)
	}
	full, found := results.Get(projected.Handle)
	if !found || full != content {
		t.Fatalf("full result bytes=%d found=%t", len(full), found)
	}
	if original[2].Blocks[0].ToolResult.Content ==
		history[2].Blocks[0].ToolResult.Content {
		t.Fatal("model-visible result was not pruned")
	}
}

func TestStatelessDefaultCompactsLargeHistoryIntoTruthCapsule(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, nil)
	engine.options.Route = reasoningRoute(t)
	engine.options.Context.Window.AutoTokens = 24 << 10
	history := []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("first context ", 5000), 1),
		messageWithText(provider.RoleAssistant, "first answer", 1),
		messageWithText(provider.RoleUser, strings.Repeat("second context ", 5000), 2),
		messageWithText(provider.RoleAssistant, "second answer", 2),
		messageWithText(provider.RoleUser, "current request", 3),
	}
	var receipt *CompactionReceipt
	window, err := engine.runCompactGate(
		t.Context(),
		&history,
		agentcontext.NewMessageLedger(agentcontext.LedgerInput{}).Snapshot(),
		0,
		CompactionPhasePreSampling,
		true,
		func(_ State, event Event) error {
			receipt = event.Compaction
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.RemovedMessages == 0 {
		t.Fatalf("large stateless history was not replaced: %+v", receipt)
	}
	if window.active > engine.autoCompactLimit() {
		t.Fatalf(
			"compacted active tokens = %d, want <= %d",
			window.active,
			engine.autoCompactLimit(),
		)
	}
	if !strings.Contains(history[0].Text(), "<codehelper_truth_capsule>") {
		t.Fatalf("first retained message is not a truth capsule: %q", history[0].Text())
	}
}

func TestToolResultPruningSkipsMalformedAndRetrievalResults(t *testing.T) {
	results := tool.NewResultStore(32 << 10)
	engine := newEngine(
		t,
		&scriptedProvider{},
		tool.NewRegistry(nil, results),
	)
	history := []provider.Message{
		toolCallMessage(1, "call-malformed", "file_read", `{}`),
		toolResultMessage(1, "call-malformed", "not-json"),
		toolCallMessage(1, "call-retrieval", "result_get", `{}`),
		toolResultMessage(
			1,
			"call-retrieval",
			string(mustJSON(t, tool.Result{
				Content: strings.Repeat("retrieved", 2000),
			})),
		),
	}
	before := cloneMessages(history)
	stats, _, err := engine.pruneToolResultSurfaces(
		&history,
		agentcontext.NewMessageLedger(agentcontext.LedgerInput{}).Snapshot(),
		128,
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stats.results != 0 ||
		agentcontext.HistoryBytes(history) != agentcontext.HistoryBytes(before) {
		t.Fatalf("stats=%+v history=%+v", stats, history)
	}
}

func TestCompactGateKeepsConsumedResultsBelowDynamicPressureThreshold(t *testing.T) {
	results := tool.NewResultStore(32 << 10)
	engine := newEngine(
		t,
		&scriptedProvider{},
		tool.NewRegistry(nil, results),
	)
	engine.options.Context.Window.AutoTokens = 3500
	large := strings.Repeat("result ", 160)
	encoded, err := json.Marshal(tool.Result{Content: large})
	if err != nil {
		t.Fatal(err)
	}
	history := []provider.Message{
		toolCallMessage(1, "old-1", "file_read", `{}`),
		toolResultMessage(1, "old-1", string(encoded)),
		toolCallMessage(1, "old-2", "file_read", `{}`),
		toolResultMessage(1, "old-2", string(encoded)),
		toolCallMessage(1, "latest", "file_read", `{}`),
		toolResultMessage(1, "latest", string(encoded)),
	}
	latest := history[len(history)-1].Blocks[0].ToolResult.Content
	before := cloneMessages(history)
	var receipt *CompactionReceipt
	_, err = engine.runCompactGate(
		t.Context(),
		&history,
		agentcontext.NewMessageLedger(agentcontext.LedgerInput{}).Snapshot(),
		0,
		CompactionPhaseMidTurn,
		true,
		func(_ State, event Event) error {
			receipt = event.Compaction
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt != nil || agentcontext.HistoryBytes(history) !=
		agentcontext.HistoryBytes(before) ||
		history[len(history)-1].Blocks[0].ToolResult.Content != latest {
		t.Fatalf("below-pressure history changed: receipt=%+v history=%+v", receipt, history)
	}
}

func TestToolPairIdentityEquivalenceRejectsRewrittenCalls(t *testing.T) {
	before := []provider.Message{
		toolCallMessage(1, "call-stable", "file_read", `{}`),
		toolResultMessage(1, "call-stable", "result"),
	}
	after := cloneMessages(before)
	after[1].Blocks[0].ToolResult.CallID = "call-drifted"
	if agentcontext.ToolPairIdentityEquivalent(before, after) {
		t.Fatal("rewritten Tool Call/Result identity was accepted")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
