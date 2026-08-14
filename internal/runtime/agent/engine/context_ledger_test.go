package engine

import (
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
	if err := registry.Register(&echoTool{}, nil); err != nil {
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
