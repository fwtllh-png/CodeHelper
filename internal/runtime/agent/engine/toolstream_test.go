package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

// streamingTool reports progress while it runs, the way a shell command does.
type streamingTool struct {
	chunks []string
	// blocked, when non-nil, holds the tool open until the test releases it, so a
	// test can prove chunks arrive before the result does.
	blocked chan struct{}
}

func (*streamingTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "stream", Description: "stream output", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (s *streamingTool) Execute(ctx context.Context, _ json.RawMessage) (tool.Result, error) {
	observe := tool.OutputObserverFrom(ctx)
	var whole strings.Builder
	var cursor uint64
	for _, chunk := range s.chunks {
		whole.WriteString(chunk)
		cursor += uint64(len(chunk))
		if observe != nil {
			observe(tool.OutputChunk{Stream: "stdout", Data: chunk, Cursor: cursor})
		}
	}
	if s.blocked != nil {
		<-s.blocked
	}
	return tool.Result{Content: whole.String()}, nil
}

func streamingTurn(t *testing.T, executor tool.Executor, options func(*Options)) (*Engine, *scriptedProvider) {
	t.Helper()
	runtime := &scriptedProvider{streams: []provider.Stream{
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: executor.Descriptor().Name, Arguments: `{}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	opts := Options{ProviderConfig: ProviderConfig{Provider: runtime, Route: testRoute(t), MaxOutputTokens: 128}, ToolConfig: ToolConfig{Tools: registry,
		Authorize: func(provider.ToolCall) bool { return true }},
	}
	if options != nil {
		options(&opts)
	}
	engine, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return engine, runtime
}

// The point of the feature: output is observable while the call is open, tagged
// with the call it belongs to, and the result still carries the whole thing.
func TestToolOutputReachesTheHostWhileTheCallIsStillOpen(t *testing.T) {
	release := make(chan struct{})
	executor := &streamingTool{chunks: []string{"compiling\n", "linking\n"}, blocked: release}
	engine, _ := streamingTurn(t, executor, nil)

	var (
		chunks  []ToolOutput
		results int
	)
	done := make(chan error, 1)
	go func() {
		_, err := engine.Run(t.Context(), "work", func(event Event) error {
			switch {
			case event.ToolOutput != nil:
				chunks = append(chunks, *event.ToolOutput)
				if len(chunks) == 2 {
					// Both chunks landed with the tool still running.
					close(release)
				}
			case event.Result != nil:
				results++
			}
			return nil
		})
		done <- err
	}()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(chunks) != 2 {
		t.Fatalf("chunks = %+v, want one per write", chunks)
	}
	if results != 1 {
		t.Fatalf("tool results = %d, want 1", results)
	}
	for index, chunk := range chunks {
		if chunk.CallID != "call_1" || chunk.Tool != "stream" {
			t.Fatalf("chunk %d = %+v, want it attributed to the call", index, chunk)
		}
		if chunk.Stream != "stdout" || chunk.Truncated {
			t.Fatalf("chunk %d = %+v", index, chunk)
		}
	}
	if chunks[0].Chunk != "compiling\n" || chunks[1].Chunk != "linking\n" {
		t.Fatalf("chunks = %+v, want them in order and whole", chunks)
	}
	if chunks[1].Cursor != uint64(len("compiling\nlinking\n")) {
		t.Fatalf("cursor = %d, want the byte count through the second chunk", chunks[1].Cursor)
	}
}

// A command that prints a hundred megabytes must not turn into a hundred
// megabytes of events. Streaming stops; the result does not.
func TestStreamingStopsAtItsBudgetButTheResultIsComplete(t *testing.T) {
	executor := &streamingTool{chunks: []string{
		strings.Repeat("a", 8), strings.Repeat("b", 8), strings.Repeat("c", 8),
	}}
	engine, runtime := streamingTurn(t, executor, func(options *Options) {
		options.MaxToolStreamBytes = 12
	})

	var chunks []ToolOutput
	if _, err := engine.Run(t.Context(), "work", func(event Event) error {
		if event.ToolOutput != nil {
			chunks = append(chunks, *event.ToolOutput)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var streamed strings.Builder
	for _, chunk := range chunks {
		streamed.WriteString(chunk.Chunk)
	}
	if streamed.Len() != 12 {
		t.Fatalf("streamed %d bytes, want the 12-byte budget", streamed.Len())
	}
	if last := chunks[len(chunks)-1]; !last.Truncated {
		t.Fatalf("last chunk = %+v, want it flagged truncated", last)
	}
	// Nothing is sent after the budget is spent, so a client is not left guessing
	// whether more is coming.
	if len(chunks) != 2 {
		t.Fatalf("chunks = %d, want delivery to stop at the ceiling", len(chunks))
	}
	// The model still sees everything the command printed.
	if len(runtime.requests) < 2 {
		t.Fatalf("requests = %d", len(runtime.requests))
	}
	var feedback string
	for _, message := range runtime.requests[1].Messages {
		if content := toolResultContent(message); content != "" {
			feedback = content
		}
	}
	if !strings.Contains(feedback, strings.Repeat("c", 8)) {
		t.Fatalf("tool feedback lost the tail: %q", feedback)
	}
}

func toolResultContent(message provider.Message) string {
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolResult && block.ToolResult != nil {
			return block.ToolResult.Content
		}
	}
	return ""
}

// A tool that streams nothing must behave exactly as it did before this existed.
func TestATurnWithoutStreamedOutputEmitsNoOutputEvents(t *testing.T) {
	engine, _ := streamingTurn(t, &streamingTool{}, nil)
	var chunks int
	if _, err := engine.Run(t.Context(), "work", func(event Event) error {
		if event.ToolOutput != nil {
			chunks++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if chunks != 0 {
		t.Fatalf("output events = %d, want none", chunks)
	}
}
