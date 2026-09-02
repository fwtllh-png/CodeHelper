package engine

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/QCode/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
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

func TestStatelessVisionModelKeepsHistoricalImageUntilExplicitCompaction(t *testing.T) {
	registry := declarationRegistry(t, false)
	if err := tool.RegisterImageReopen(registry); err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{textStream("first")}}
	engine := newEngine(t, runtime, registry)
	route := engine.options.Route
	caps := route.Model().Capabilities
	caps.ImageInput, caps.Vision = true, true
	route = route.WithCapabilities(caps)
	engine.options.Route = route
	engine.options.Routes, _ = model.NewRouteSet(route, nil, false)
	engine.options.Context.RecentTailTurns = 3
	image := provider.Attachment{
		Name: "screen.png", MediaType: "image/png", Data: []byte("png"),
	}
	if _, err := engine.RunForTurnWithAttachments(
		t.Context(), "image-turn", "inspect this", []provider.Attachment{image}, nil,
	); err != nil {
		t.Fatal(err)
	}
	var handle string
	for _, message := range engine.History() {
		for _, block := range message.Blocks {
			if block.Attachment != nil {
				handle = block.Attachment.Handle
			}
		}
	}
	if handle == "" {
		t.Fatal("image was not bound to a reopen handle")
	}
	runtime.streams = append(runtime.streams,
		textStream("second"),
		toolCallStream("reopen-image", tool.ImageReopenToolName,
			fmt.Sprintf(`{"handle":%q}`, handle)),
		toolCallStream("complete", "turn_complete", `{
			"status":"complete",
			"summary":"done with the original image",
			"pending_actions":[]
		}`),
	)
	if _, err := engine.RunForTurnWithAttachments(
		t.Context(), "second-turn", "continue", nil, nil,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunForTurnWithAttachments(
		t.Context(), "third-turn", "reinspect", nil, nil,
	); err != nil {
		t.Fatal(err)
	}
	request := runtime.requests[len(runtime.requests)-1]
	var reopened bool
	for _, message := range request.Messages {
		for _, block := range message.Blocks {
			if block.Type == provider.ContentImage && block.Attachment != nil &&
				string(block.Attachment.Data) == "png" {
				reopened = true
			}
		}
	}
	if !reopened {
		t.Fatalf("reopened image missing from provider request: %+v", request.Messages)
	}
	agedRequest := runtime.requests[len(runtime.requests)-2]
	var retained bool
	for _, message := range agedRequest.Messages {
		for _, block := range message.Blocks {
			if block.Type == provider.ContentImage && block.Attachment != nil &&
				block.Attachment.Handle == handle &&
				string(block.Attachment.Data) == "png" {
				retained = true
			}
		}
	}
	if !retained {
		t.Fatalf("historical image was rewritten before compaction: %+v", agedRequest.Messages)
	}
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
