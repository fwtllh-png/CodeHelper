package engine

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestLegacyToolResultIsAdmittedBeforeContextProjection(t *testing.T) {
	results := tool.NewResultStore(32 << 10)
	engine := newEngine(
		t,
		&scriptedProvider{},
		tool.NewRegistry(nil, results),
	)
	payload := strings.Repeat("0123456789abcdef", 6400)
	history := []provider.Message{
		toolCallMessage(1, "legacy-call", "exec_command", `{}`),
		toolResultMessage(1, "legacy-call", payload),
	}
	admitted, err := engine.admitToolResultHistory(history)
	if err != nil {
		t.Fatal(err)
	}
	block := admitted[1].Blocks[0].ToolResult
	if block.Admission == nil || !block.Admission.Truncated ||
		block.Admission.OriginalBytes != 100<<10 ||
		block.Admission.RetainedTokens > 10_000 ||
		block.Admission.Handle == "" ||
		len(block.Content)*5 > len(payload) {
		t.Fatalf("admitted block=%+v", block)
	}
	full, ok := results.Get(block.Admission.Handle)
	if !ok || full != payload {
		t.Fatalf("full bytes=%d found=%t", len(full), ok)
	}
	second, err := engine.admitToolResultHistory(admitted)
	if err != nil {
		t.Fatal(err)
	}
	secondReceipt := second[1].Blocks[0].ToolResult.Admission
	if secondReceipt == nil ||
		secondReceipt.Handle != block.Admission.Handle ||
		secondReceipt.Digest != block.Admission.Digest {
		t.Fatalf("second admission=%+v", secondReceipt)
	}
}

func TestBlindModelReceivesTextProjectionInsteadOfImage(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	_, err := engine.RunForTurnWithAttachments(
		t.Context(),
		"turn-image",
		"inspect",
		[]provider.Attachment{{
			Name: "screen.png", MediaType: "image/png", Data: []byte("png"),
		}},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range runtime.requests[0].Messages {
		for _, block := range message.Blocks {
			if block.Type == provider.ContentImage {
				t.Fatalf("blind model received image block: %+v", block)
			}
			if block.Type == provider.ContentText &&
				strings.Contains(block.Text, "screen.png") {
				return
			}
		}
	}
	t.Fatalf("request has no image placeholder: %+v", runtime.requests[0])
}

func TestProviderProjectionDropsOrphanedToolBlocks(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.history = []provider.Message{
		toolCallMessage(1, "call-without-result", "file_read", `{}`),
		toolResultMessage(1, "result-without-call", `{"content":"orphan"}`),
	}
	if _, err := engine.Run(t.Context(), "continue", nil); err != nil {
		t.Fatal(err)
	}
	for _, message := range runtime.requests[0].Messages {
		for _, block := range message.Blocks {
			if block.ToolCall != nil || block.ToolResult != nil {
				t.Fatalf("provider received orphan block: %+v", block)
			}
		}
	}
}
