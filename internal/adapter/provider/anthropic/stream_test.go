package anthropic

import (
	"io"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestStreamNormalizesAnthropicEvents(t *testing.T) {
	input := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":9}}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"signed-thinking"}}`,
		"",
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":4}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)))
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	want := []provider.StreamEventType{
		provider.EventMessageStart,
		provider.EventUsage,
		provider.EventReasoningDelta,
		provider.EventReasoningSignature,
		provider.EventTextDelta,
		provider.EventUsage,
		provider.EventMessageStop,
	}
	if len(events) != len(want) {
		t.Fatalf("events = %+v", events)
	}
	for index, eventType := range want {
		if events[index].Type != eventType {
			t.Fatalf("events[%d].Type = %q, want %q", index, events[index].Type, eventType)
		}
	}
	if events[2].Block == nil || events[2].Block.Text != "think" ||
		events[3].Block == nil || events[3].Block.Signature != "signed-thinking" {
		t.Fatalf("reasoning blocks = %+v %+v", events[2].Block, events[3].Block)
	}
}

// TestStreamCountsCachedInputTokens covers a shape only Anthropic has: cache
// reads and cache writes are reported beside input_tokens instead of inside it,
// so an adapter that forwards input_tokens alone makes every cached token — and
// the money it cost — invisible.
func TestStreamCountsCachedInputTokens(t *testing.T) {
	input := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"usage":{"input_tokens":9,` +
			`"cache_read_input_tokens":120,"cache_creation_input_tokens":30}}}`,
		"",
		`event: message_delta`,
		`data: {"type":"message_delta","usage":{"output_tokens":4}}`,
		"",
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)))
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	var usage provider.Usage
	for _, event := range events {
		if event.Type == provider.EventUsage {
			usage.Add(*event.Usage)
		}
	}
	if usage.InputTokens != 159 || usage.CachedTokens != 120 {
		t.Fatalf("usage = %+v, want 159 input including the 120 read from cache", usage)
	}
	// The two breakdown fields must stay inside their totals, or every consumer
	// that adds them up bills the same tokens twice.
	if usage.CachedTokens > usage.InputTokens || usage.ReasoningTokens > usage.OutputTokens {
		t.Fatalf("usage breaks the subset invariants: %+v", usage)
	}
	// Anthropic bills thinking inside output_tokens and never reports it
	// separately, so reasoning stays zero here by definition rather than by
	// omission.
	if usage.ReasoningTokens != 0 {
		t.Fatalf("reasoning tokens = %d, want 0 for Anthropic", usage.ReasoningTokens)
	}
}

func TestStreamPreservesMaxTokensStopReason(t *testing.T) {
	input := strings.Join([]string{
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"max_tokens"},"usage":{"output_tokens":4}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)))
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if got := events[len(events)-1]; got.Type != provider.EventMessageStop ||
		got.StopReason != provider.StopReasonMaxTokens {
		t.Fatalf("terminal event = %+v", got)
	}
}

func TestStreamNormalizesSearchCitationAndRegularTool(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"server_tool_use","id":"srv_1","name":"web_search"}}`,
		"",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"query\":\"Go docs\"}"}}`,
		"",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"web_search_tool_result","content":[{"type":"web_search_result","url":"https://go.dev","title":"Go"}]}}`,
		"",
		`data: {"type":"content_block_start","index":2,"content_block":{"type":"tool_use","id":"tool_1","name":"read"}}`,
		"",
		`data: {"type":"content_block_delta","index":3,"delta":{"type":"citations_delta","citation":{"type":"web_search_result_location","encrypted_index":"src_1","url":"https://go.dev","title":"Go","start_char_index":0,"end_char_index":2}}}`,
		"",
		`data: {"type":"message_stop"}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)))
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 5 ||
		events[1].Type != provider.EventSearchResult ||
		events[2].Type != provider.EventToolCallDelta ||
		events[3].Type != provider.EventCitation {
		t.Fatalf("events = %+v", events)
	}
	if len(events[1].Search.Sources) != 1 || events[1].Search.Sources[0].URL != "https://go.dev" {
		t.Fatalf("search = %+v", events[1].Search)
	}
	if events[1].Search.Query != "Go docs" {
		t.Fatalf("search query = %q", events[1].Search.Query)
	}
}

func TestStreamNormalizesEmptyAndFailedSearch(t *testing.T) {
	for name, content := range map[string]struct {
		content   string
		wantError bool
	}{
		"empty":  {`[]`, false},
		"failed": {`{"type":"web_search_tool_result_error","error_code":"too_many_requests"}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			input := `data: {"type":"content_block_start","content_block":{"type":"web_search_tool_result","content":` +
				content.content + "}}\n\ndata: {\"type\":\"message_stop\"}\n\n"
			stream, err := NewStream(io.NopCloser(strings.NewReader(input)))
			if err != nil {
				t.Fatal(err)
			}
			events, err := provider.Drain(stream)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 3 || events[1].Type != provider.EventSearchResult {
				t.Fatalf("events = %+v", events)
			}
			if got := events[1].Search.Error != ""; got != content.wantError {
				t.Fatalf("search error = %q", events[1].Search.Error)
			}
		})
	}
}

func FuzzStreamParserAnthropic(f *testing.F) {
	f.Add([]byte(`{"type":"message_stop"}`))
	f.Add([]byte(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_, _ = parseChunk(data)
	})
}
