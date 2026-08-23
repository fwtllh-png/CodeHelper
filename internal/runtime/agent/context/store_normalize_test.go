package agentcontext

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestNormalizePreservesOnlyUniqueOrderedToolPairs(t *testing.T) {
	snapshot := NewMessageLedger(LedgerInput{History: []provider.Message{
		toolCallContextMessage("paired", 1),
		toolResultContextMessage("paired", 1),
		toolCallContextMessage("missing", 2),
		toolResultContextMessage("result-only", 2),
		toolCallContextMessage("duplicate", 3),
		toolCallContextMessage("duplicate", 3),
		toolResultContextMessage("duplicate", 3),
		toolResultContextMessage("reversed", 4),
		toolCallContextMessage("reversed", 4),
	}}).Snapshot()
	normalized, receipt, err := snapshot.Normalize(model.Capabilities{
		ToolCalls: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ToolCalls != 5 || receipt.ToolResults != 4 ||
		receipt.PairedCalls != 1 || receipt.DroppedOrphans != 7 ||
		receipt.ModelVisibleOrphans != 0 {
		t.Fatalf("receipt=%+v", receipt)
	}
	messages := normalized.Messages()
	if len(messages) != 2 ||
		messages[0].Blocks[0].ToolCall.ID != "paired" ||
		messages[1].Blocks[0].ToolResult.CallID != "paired" {
		t.Fatalf("normalized=%+v", messages)
	}
}

func TestNormalizeProjectsUnsupportedModalities(t *testing.T) {
	snapshot := NewMessageLedger(LedgerInput{History: []provider.Message{{
		Role: provider.RoleUser,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentImage,
			Attachment: &provider.Attachment{
				Name: "screen.png", MediaType: "image/png", Data: []byte("png"),
			},
		}},
	}, {
		Role: provider.RoleAssistant,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentReasoning, Text: "private reasoning",
		}, {
			Type: provider.ContentText, Text: "public answer",
		}},
	}}}).Snapshot()
	normalized, receipt, err := snapshot.Normalize(model.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	messages := normalized.Messages()
	if receipt.ProjectedImages != 1 || receipt.DroppedReasoning != 1 ||
		len(messages) != 2 ||
		messages[0].Blocks[0].Type != provider.ContentText ||
		!strings.Contains(messages[0].Blocks[0].Text, "screen.png") ||
		messages[1].Text() != "public answer" {
		t.Fatalf("receipt=%+v messages=%+v", receipt, messages)
	}
	supported, supportedReceipt, err := snapshot.Normalize(model.Capabilities{
		ImageInput: true, Reasoning: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if supportedReceipt.ProjectedImages != 0 ||
		supportedReceipt.DroppedReasoning != 0 ||
		supported.Messages()[0].Blocks[0].Type != provider.ContentImage {
		t.Fatalf(
			"supported receipt=%+v messages=%+v",
			supportedReceipt, supported.Messages(),
		)
	}
}

func TestProjectStatelessHistoryCollapsesWorldAndConsumedReasoning(t *testing.T) {
	worldV1 := provider.TextMessage(provider.RoleSystem, "world v1")
	full, err := ProjectWorld([]WorldSection{{
		ID: "workspace", Digest: "v1", Message: &worldV1,
	}}, WorldBaseline{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	worldV2 := provider.TextMessage(provider.RoleSystem, "world v2")
	patch, err := ProjectWorld([]WorldSection{{
		ID: "workspace", Digest: "v2", Message: &worldV2,
	}}, full.Baseline, full.Messages)
	if err != nil {
		t.Fatal(err)
	}
	call := toolCallContextMessage("closed", 1)
	call.Blocks = append([]provider.ContentBlock{
		{Type: provider.ContentReasoning, Text: "consumed reasoning"},
		{Type: provider.ContentText, Text: "running the tool"},
	}, call.Blocks...)
	pending := toolCallContextMessage("pending", 1)
	pending.Blocks = append([]provider.ContentBlock{{
		Type: provider.ContentReasoning, Text: "pending reasoning",
	}}, pending.Blocks...)
	history := append(CloneMessages(full.Messages), call)
	history = append(history, toolResultContextMessage("closed", 1))
	history = append(history, patch.Messages...)
	history = append(history, pending)

	projected := ProjectStatelessHistory(history)
	if len(projected) != 4 {
		t.Fatalf("projected messages = %+v", projected)
	}
	if projected[0].Blocks[0].ToolCall == nil ||
		len(projected[0].Blocks) != 1 {
		t.Fatalf("closed tool message = %+v", projected[0])
	}
	if projected[2].Text() != "world v2" {
		t.Fatalf("world projection = %+v", projected[2])
	}
	if projected[3].Blocks[0].Type != provider.ContentReasoning {
		t.Fatalf("pending reasoning was removed: %+v", projected[3])
	}
	if history[1].Blocks[0].Type != provider.ContentReasoning {
		t.Fatal("durable history was mutated")
	}
}

func toolCallContextMessage(id string, turn uint64) provider.Message {
	return provider.Message{
		Role: provider.RoleAssistant, Turn: turn,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolCall,
			ToolCall: &provider.ToolCall{
				ID: id, Name: "fixture", Arguments: `{}`,
			},
		}},
	}
}

func toolResultContextMessage(id string, turn uint64) provider.Message {
	return provider.Message{
		Role: provider.RoleTool, Turn: turn,
		Blocks: []provider.ContentBlock{{
			Type:       provider.ContentToolResult,
			ToolResult: &provider.ToolResult{CallID: id, Content: "ok"},
		}},
	}
}
