package tui

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestContextSlashReportsTheLastReceiptSections(t *testing.T) {
	host := &granularHost{}
	model := NewModel(Options{}, host)

	before := model.dispatchSlash(commands.Action{Kind: commands.KindContext, Name: "context"})
	if !strings.Contains(before.buildTranscriptView(), "no turn has reported") {
		t.Fatalf("before any turn: %q", before.buildTranscriptView())
	}

	message := mapRuntimeEvent(protocol.Event{
		Kind: protocol.EventExecutionReceipt,
		Data: &protocol.ExecutionReceiptData{
			ContextSections: []protocol.ReceiptContextSection{
				{Kind: "repo_map", RetainedBytes: 512},
				{
					Kind: "working_set_ledger", RetainedBytes: 64,
					OriginalBytes: 900, Truncated: true, TruncationReason: "budget",
				},
			},
			ContextSelections: []protocol.ReceiptContextSelection{{
				Path: "internal/a_test.go", Kind: "test", Reasons: []string{"search"},
				Evidence: []protocol.ReceiptContextSelectionEvidence{{
					Kind: "test", Tool: "search_related_tests",
				}},
				Included: false, Truncated: true, TruncationReason: "byte_budget",
			}},
			ReadPaths: []string{"internal/a.go", "internal/b.go"},
		},
	})
	stream, ok := message.(streamMsg)
	if !ok {
		t.Fatalf("receipt mapped to %T", message)
	}

	updated, _ := model.Update(stream)
	after := updated.(Model).dispatchSlash(commands.Action{
		Kind: commands.KindContext, Name: "context",
	})
	view := after.buildTranscriptView()
	normalized := strings.Join(strings.Fields(view), " ")
	for _, want := range []string{
		"repo_map 512B", "working_set_ledger 64B", "(cut:budget)", "read 2 path(s)",
		"internal/a_test.go [test] via search",
		"evidence=test/search_related_tests", "(cut:byte_budget)",
	} {
		if !strings.Contains(normalized, want) {
			t.Fatalf("view %q missing %q", view, want)
		}
	}
}

func TestReceiptWithoutContextSectionsSaysNothing(t *testing.T) {
	message := mapRuntimeEvent(protocol.Event{
		Kind: protocol.EventExecutionReceipt,
		Data: &protocol.ExecutionReceiptData{},
	})
	stream, ok := message.(streamMsg)
	if !ok {
		t.Fatalf("receipt mapped to %T", message)
	}
	if stream.contextSummary != "" {
		t.Fatalf("summary = %q", stream.contextSummary)
	}
}
