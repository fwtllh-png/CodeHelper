package engine

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestModelSamplesUseMonotonicContextLedgerSnapshots(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{
				Type: provider.EventToolCallDelta,
				ToolCall: &provider.ToolCallFragment{
					Index: 0, ID: "call-ledger", Name: "echo",
					Arguments: `{"text":"hello"}`,
				},
			},
			{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 100}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 120}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	var contexts []*protocol.SampleContextData
	if _, err := engine.Run(t.Context(), "work", func(event Event) error {
		if event.SampleContext != nil {
			copy := *event.SampleContext
			contexts = append(contexts, &copy)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 2 || len(contexts) != 2 {
		t.Fatalf("requests=%d contexts=%d", len(runtime.requests), len(contexts))
	}
	for index, context := range contexts {
		if context.ContextRevision == 0 || context.ContextDigest == "" {
			t.Fatalf("sample %d missing ledger identity: %+v", index+1, context)
		}
		if context.MessageCount != len(runtime.requests[index].Messages) ||
			context.ToolDefinitionCount != len(runtime.requests[index].Tools) {
			t.Fatalf(
				"sample %d attribution/request mismatch: context=%+v request=%+v",
				index+1, context, runtime.requests[index],
			)
		}
		projection := runtime.requests[index].Projection
		if projection.ContextRevision != context.ContextRevision ||
			projection.WindowID != context.WindowID ||
			projection.WindowNumber != context.WindowNumber ||
			projection.Retry ||
			projection.RecoveryID != "" {
			t.Fatalf(
				"sample %d projection continuity mismatch: context=%+v projection=%+v",
				index+1,
				context,
				projection,
			)
		}
	}
	if contexts[1].ContextRevision <= contexts[0].ContextRevision {
		t.Fatalf(
			"context revision did not advance: first=%d second=%d",
			contexts[0].ContextRevision,
			contexts[1].ContextRevision,
		)
	}
	if contexts[0].ContextDigest == contexts[1].ContextDigest {
		t.Fatal("context digest did not change after Tool Call/Result history")
	}
	assertRequestMessagePrefix(t, runtime.requests[0], runtime.requests[1])
	if contexts[0].PrefixCompared || !contexts[1].PrefixCompared ||
		!contexts[1].PrefixMonotonic ||
		contexts[1].PrefixCommonItems == 0 ||
		contexts[1].PrefixCommonTokens == 0 ||
		contexts[1].StablePrefixDigest == "" ||
		contexts[1].PreviousContextDigest != contexts[0].ContextDigest ||
		contexts[1].RouteDigest == "" ||
		contexts[1].RequestPropertyDigest == "" ||
		contexts[0].UncachedInputTokens != 100 ||
		contexts[1].UncachedInputTokens != 120 {
		t.Fatalf("prefix attribution = %+v", contexts)
	}
	if contexts[0].WindowID == "" ||
		contexts[0].WindowID != contexts[1].WindowID ||
		contexts[0].WindowNumber != 1 || contexts[1].WindowNumber != 1 ||
		!contexts[0].WindowObserved || !contexts[1].WindowObserved ||
		contexts[0].WindowPrefillTokens != 100 ||
		contexts[0].WindowFullActiveTokens != 100 ||
		contexts[0].WindowProjectedTokens == 0 ||
		contexts[0].WindowPendingTokens == 0 ||
		contexts[1].WindowPrefillTokens != 100 ||
		contexts[1].WindowFullActiveTokens != 120 ||
		contexts[1].WindowBodyTokens != 20 ||
		contexts[1].WindowProjectedTokens == 0 ||
		contexts[1].WindowPendingTokens == 0 ||
		contexts[0].WindowOutputReserve == 0 {
		t.Fatalf("window projections=%+v", contexts)
	}
	if contexts[0].WorldRevision != 1 || contexts[0].WorldMode != "full" ||
		contexts[0].WorldChangedSections == 0 ||
		contexts[1].WorldRevision != 1 || contexts[1].WorldMode != "patch" ||
		contexts[1].WorldChangedSections != 0 ||
		contexts[0].WorldDigest == "" ||
		contexts[0].WorldDigest != contexts[1].WorldDigest {
		t.Fatalf("world projections=%+v", contexts)
	}
}

func TestPrefixManifestContinuesAcrossTurns(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "first"},
			{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 100}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "second"},
			{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 120}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	var contexts []*protocol.SampleContextData
	capture := func(event Event) error {
		if event.SampleContext != nil {
			copy := *event.SampleContext
			contexts = append(contexts, &copy)
		}
		return nil
	}
	if _, err := engine.Run(t.Context(), "first", capture); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context(), "second", capture); err != nil {
		t.Fatal(err)
	}
	if len(contexts) != 2 ||
		contexts[0].PrefixCompared ||
		!contexts[1].PrefixCompared ||
		!contexts[1].PrefixMonotonic ||
		contexts[1].PreviousContextDigest != contexts[0].ContextDigest {
		t.Fatalf("cross-turn prefix attribution = %+v", contexts)
	}
	if len(runtime.requests) != 2 ||
		len(runtime.requests[1].Messages) <= len(runtime.requests[0].Messages) {
		t.Fatalf("cross-turn requests = %+v", runtime.requests)
	}
	assertRequestMessagePrefix(t, runtime.requests[0], runtime.requests[1])
}

func TestToolResultRequestRemainsPrefixOfNextTurn(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{
				Type: provider.EventToolCallDelta,
				ToolCall: &provider.ToolCallFragment{
					Index: 0, ID: "call-prefix", Name: "echo",
					Arguments: `{"text":"hello"}`,
				},
			},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "first complete"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "second complete"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	if _, err := engine.Run(t.Context(), "first", func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context(), "second", func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 3 {
		t.Fatalf("requests=%d", len(runtime.requests))
	}
	assertRequestMessagePrefix(t, runtime.requests[0], runtime.requests[1])
	assertRequestMessagePrefix(t, runtime.requests[1], runtime.requests[2])
	var found bool
	for _, message := range runtime.requests[2].Messages {
		if messageToolResultID(message) == "call-prefix" {
			found = true
			if !strings.Contains(message.Blocks[0].ToolResult.Content, "hello") {
				t.Fatalf(
					"tool result was rewritten: %+v",
					message.Blocks[0].ToolResult,
				)
			}
		}
	}
	if !found {
		t.Fatal("third request lost the consumed Tool Result")
	}
}

func assertRequestMessagePrefix(
	t *testing.T,
	previousRequest provider.ModelRequest,
	currentRequest provider.ModelRequest,
) {
	t.Helper()
	if len(currentRequest.Messages) < len(previousRequest.Messages) {
		t.Fatalf(
			"request shrank: previous=%d current=%d",
			len(previousRequest.Messages),
			len(currentRequest.Messages),
		)
	}
	previous, err := json.Marshal(previousRequest.Messages)
	if err != nil {
		t.Fatal(err)
	}
	currentPrefix, err := json.Marshal(
		currentRequest.Messages[:len(previousRequest.Messages)],
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(previous, currentPrefix) {
		t.Fatalf(
			"second Turn rewrote the previous request prefix:\nprevious=%s\ncurrent=%s",
			previous,
			currentPrefix,
		)
	}
}

func TestProjectionRecoveryIdentityBindsActionAndSourceTurn(t *testing.T) {
	recovery := &protocol.TurnRecoveryContext{
		Action: protocol.TurnRecoveryContinue, SourceTurnID: "turn_blocked",
	}
	if got := projectionRecoveryID(recovery); got != "continue\x00turn_blocked" {
		t.Fatalf("recovery identity=%q", got)
	}
	if got := projectionRecoveryID(nil); got != "" {
		t.Fatalf("nil recovery identity=%q", got)
	}
}

// TestContextPartitionPurityKeepsWorldSectionsAfterStablePrefix locks the §5.2
// layout contract: the Stable partition is byte-identical to StaticContext and
// volatile world sections (tool catalog etc.) are appended to History after the
// stable prefix, never injected into the leading prefix.
func TestContextPartitionPurityKeepsWorldSectionsAfterStablePrefix(t *testing.T) {
	static := []provider.Message{
		provider.TextMessage(provider.RoleSystem, "You are a static base system."),
	}
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventUsage, Usage: &provider.Usage{InputTokens: 50}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{
		ProviderConfig: ProviderConfig{
			Provider: runtime, Route: testRoute(t), MaxOutputTokens: 128,
		},
		ToolConfig: ToolConfig{
			Tools:     registry,
			Authorize: func(provider.ToolCall) bool { return true },
		},
		ContextConfig: ContextConfig{StaticContext: static},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context(), "work", func(Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("requests=%d", len(runtime.requests))
	}
	messages := runtime.requests[0].Messages
	if len(messages) <= len(static) {
		t.Fatalf("request has %d messages, must exceed %d static messages", len(messages), len(static))
	}
	// Stable prefix must be byte-identical to StaticContext and free of any
	// volatile world-section marker.
	for index, expected := range static {
		got, _ := json.Marshal(messages[index])
		want, _ := json.Marshal(expected)
		if !bytes.Equal(got, want) {
			t.Fatalf("stable message %d changed: got=%s want=%s", index, got, want)
		}
		if strings.Contains(messages[index].Text(), "[tool_catalog]") {
			t.Fatalf("stable message %d contains world content", index)
		}
	}
	// The volatile world section must appear only after the stable prefix
	// (appended to History), keeping the prefix stable for context caches.
	found := false
	for index := len(static); index < len(messages); index++ {
		if strings.Contains(messages[index].Text(), "[tool_catalog]") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("world section not found after stable prefix in %d messages", len(messages))
	}
}
