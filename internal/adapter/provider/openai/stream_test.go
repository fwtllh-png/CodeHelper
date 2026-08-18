package openai

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestChatStreamNormalizesTextReasoningToolAndUsage(t *testing.T) {
	input := strings.Join([]string{
		`data: {"choices":[{"delta":{"reasoning_content":"think "},"finish_reason":null}]}`,
		"",
		`data: {"choices":[{"delta":{"content":"hello","tool_calls":[{"index":0,"id":"call_1","function":{"name":"search","arguments":"{\"q\":"}}]},"finish_reason":null}]}`,
		"",
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":7,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":1},"prompt_tokens_details":{"cached_tokens":2}}}`,
		"",
		`data: [DONE]`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIChat)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	wantTypes := []provider.StreamEventType{
		provider.EventMessageStart,
		provider.EventReasoningDelta,
		provider.EventTextDelta,
		provider.EventToolCallDelta,
		provider.EventUsage,
		provider.EventMessageStop,
	}
	if len(events) != len(wantTypes) {
		t.Fatalf("events = %+v", events)
	}
	for index, want := range wantTypes {
		if events[index].Type != want {
			t.Fatalf("events[%d].Type = %q, want %q", index, events[index].Type, want)
		}
	}
	if events[4].Usage.InputTokens != 7 || events[4].Usage.ReasoningTokens != 1 {
		t.Fatalf("usage = %+v", events[4].Usage)
	}
}

func TestResponsesStream(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"answer"}`,
		"",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"search"}}`,
		"",
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"item_id":"fc_1","delta":"{\"q\":\"docs\"}"}`,
		"",
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"ciphertext","summary":[]}}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":4,"output_tokens":2}}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 8 ||
		events[1].Text != "thinking" ||
		events[2].Text != "answer" ||
		events[5].Type != provider.EventTransportProgress {
		t.Fatalf("events = %+v", events)
	}
	if events[3].ToolCall.Name != "search" || events[4].ToolCall.Arguments == "" {
		t.Fatalf("tool fragments = %+v %+v", events[3], events[4])
	}
	if events[3].ToolCall.ID != "call_1" || events[4].ToolCall.ID != "" {
		t.Fatalf("tool identities = %+v %+v", events[3], events[4])
	}
	for _, event := range events {
		if len(event.ReplayFragment) != 0 || event.Type == provider.EventReplayState {
			t.Fatalf("default decoder exposed private replay: %+v", event)
		}
	}
}

func TestResponsesStreamHarvestsReasoningFromCompleted(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_final","content":[{"type":"reasoning_text","text":"final chain"}]}],"usage":{"input_tokens":1,"output_tokens":2,"output_tokens_details":{"reasoning_tokens":3}}}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 {
		t.Fatalf("events = %+v", events)
	}
	var reasoning *provider.StreamEvent
	for index := range events {
		if events[index].Type == provider.EventReasoningDelta {
			reasoning = &events[index]
			break
		}
	}
	if reasoning == nil || reasoning.Text != "final chain" || reasoning.Block == nil {
		t.Fatalf("completed harvest = %+v", events)
	}
}

func TestResponsesStreamCommitsVersionedReplayOnlyOnCompletion(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","encrypted_content":"ciphertext","summary":[{"type":"summary_text","text":"inspect"}]}}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStreamWithOptions(
		io.NopCloser(strings.NewReader(input)),
		model.ProtocolOpenAIResponses,
		StreamPolicy{CaptureReplay: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	var replay *provider.ReplayState
	for _, event := range events {
		if len(event.ReplayFragment) != 0 {
			t.Fatalf("private provider data leaked through content block: %+v", event.Block)
		}
		if event.Type == provider.EventReplayState {
			replay = event.Replay
		}
	}
	if replay == nil || replay.Version != provider.ReplayVersion ||
		!strings.Contains(string(replay.Data), `"id":"rs_1"`) {
		t.Fatalf("replay = %+v, events = %+v", replay, events)
	}

	incomplete := strings.Replace(input, "response.completed", "response.incomplete", 1)
	stream, err = NewStreamWithOptions(
		io.NopCloser(strings.NewReader(incomplete)),
		model.ProtocolOpenAIResponses,
		StreamPolicy{CaptureReplay: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	events, err = provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == provider.EventReplayState {
			t.Fatalf("incomplete response committed replay: %+v", events)
		}
	}
}

func TestResponsesStreamDoesNotReplayCompletedReasoning(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.reasoning_text.delta","output_index":0,"item_id":"rs_1","delta":"think"}`,
		"",
		`data: {"type":"response.reasoning_text.delta","output_index":0,"item_id":"rs_1","delta":"ing"}`,
		"",
		`data: {"type":"response.reasoning_text.done","output_index":0,"item_id":"rs_1","text":"thinking"}`,
		"",
		`data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"thinking"}]}]}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(
		io.NopCloser(strings.NewReader(input)),
		model.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	var visible strings.Builder
	var replayEvents int
	for _, event := range events {
		if event.Type != provider.EventReasoningDelta {
			continue
		}
		visible.WriteString(event.Text)
		if event.Type == provider.EventReplayState ||
			len(event.ReplayFragment) != 0 {
			replayEvents++
		}
	}
	if visible.String() != "thinking" {
		t.Fatalf("visible reasoning = %q, events = %+v", visible.String(), events)
	}
	if replayEvents != 0 {
		t.Fatalf("private replay events = %d, want 0", replayEvents)
	}
}

func TestResponsesReplayKeepsVisibleSummarySeparateFromNativeContent(
	t *testing.T,
) {
	input := strings.Join([]string{
		`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"visible "}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","output_index":0,"item_id":"rs_1","delta":"summary"}`,
		"",
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"native private chain"}],"summary":[{"type":"summary_text","text":"visible summary"}]}}`,
		"",
		`data: {"type":"response.completed","response":{"output":[{"type":"reasoning","id":"rs_1","content":[{"type":"reasoning_text","text":"native private chain"}],"summary":[{"type":"summary_text","text":"visible summary"}]}],"usage":{"input_tokens":1,"output_tokens":2}}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStreamWithOptions(
		io.NopCloser(strings.NewReader(input)),
		model.ProtocolOpenAIResponses,
		StreamPolicy{CaptureReplay: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	var visible strings.Builder
	var replay *provider.ReplayState
	for _, event := range events {
		if event.Type == provider.EventReasoningDelta {
			visible.WriteString(event.Text)
		}
		if event.Type == provider.EventReplayState {
			replay = event.Replay
		}
	}
	if visible.String() != "visible summary" || replay == nil {
		t.Fatalf(
			"visible=%q replay=%+v events=%+v",
			visible.String(),
			replay,
			events,
		)
	}

	request := testRequest(
		t,
		"https://api.openai.test",
		model.ProtocolOpenAIResponses,
	)
	request.Messages = []provider.Message{
		provider.ProducedAssistant(
			request.Route,
			[]provider.ContentBlock{{
				Type: provider.ContentReasoning,
				ID:   "rs_1",
				Text: "visible summary",
			}},
			1,
			replay,
		),
		provider.TextMessage(provider.RoleUser, "continue"),
	}
	adapter, err := NewAdapter(model.AdapterOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	call, err := adapter.Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(call.Body), "native private chain") ||
		!strings.Contains(string(call.Body), "visible summary") {
		t.Fatalf("replayed request = %s", call.Body)
	}
}

func TestResponsesStreamNormalizesSearchCitationAndRegularTool(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","call_id":"call_1","name":"read"}}`,
		"",
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","status":"completed","action":{"query":"Go docs","sources":[{"id":"src_1","title":"Go","url":"https://go.dev"}]}}}`,
		"",
		`data: {"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","source_id":"src_1","url":"https://go.dev","title":"Go","start_index":0,"end_index":2}}`,
		"",
		`data: {"type":"response.completed","response":{"usage":{"input_tokens":4,"output_tokens":2}}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 ||
		events[1].Type != provider.EventToolCallDelta ||
		events[2].Type != provider.EventSearchResult ||
		events[3].Type != provider.EventCitation {
		t.Fatalf("events = %+v", events)
	}
	if events[2].Search.Query != "Go docs" || len(events[2].Search.Sources) != 1 {
		t.Fatalf("search = %+v", events[2].Search)
	}
	if events[3].Citation.SourceID != "src_1" || events[3].Citation.End != 2 {
		t.Fatalf("citation = %+v", events[3].Citation)
	}
}

func TestResponsesStreamNormalizesEmptyAndFailedSearch(t *testing.T) {
	for name, item := range map[string]struct {
		item      string
		wantError bool
	}{
		"empty":  {`{"type":"web_search_call","status":"completed","action":{"query":"none","sources":[]}}`, false},
		"failed": {`{"type":"web_search_call","status":"failed","action":{"query":"blocked"},"error":{"code":"rate_limit","message":"search limited"}}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			input := "data: {\"type\":\"response.output_item.done\",\"item\":" + item.item + "}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{}}\n\n"
			stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIResponses)
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
			if got := events[1].Search.Error != ""; got != item.wantError {
				t.Fatalf("search error = %q", events[1].Search.Error)
			}
		})
	}
}

func TestChatStreamNormalizesCitation(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"Go\",\"annotations\":[{\"type\":\"url_citation\",\"url_citation\":{\"url\":\"https://go.dev\",\"title\":\"Go\",\"start_index\":0,\"end_index\":2}}]},\"finish_reason\":\"stop\"}]}\n\n"
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIChat)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[2].Type != provider.EventCitation || events[2].Citation.URL != "https://go.dev" {
		t.Fatalf("events = %+v", events)
	}
}

func TestChatStreamRejectsMalformedAndAbruptStreams(t *testing.T) {
	for name, input := range map[string]string{
		"malformed": "data: {\n\n",
		"abrupt":    "data: {\"choices\":[]}\n\n",
	} {
		t.Run(name, func(t *testing.T) {
			stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIChat)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = stream.Recv()
			_, err = stream.Recv()
			if err == nil || errors.Is(err, io.EOF) {
				t.Fatalf("Recv() error = %v, want protocol error", err)
			}
		})
	}
}

func TestChatStreamAcceptsEOFOnlyAfterFinishReason(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\n"
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIChat)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 ||
		events[0].Type != provider.EventMessageStart ||
		events[1].Type != provider.EventTextDelta ||
		events[2].Type != provider.EventMessageStop {
		t.Fatalf("events = %+v", events)
	}
}

func TestChatStreamPreservesLengthStopReason(t *testing.T) {
	input := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"},\"finish_reason\":\"length\"}]}\n\n"
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIChat)
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

func TestResponsesStreamPreservesIncompleteStopReason(t *testing.T) {
	input := "data: {\"type\":\"response.incomplete\",\"response\":{}}\n\n"
	stream, err := NewStream(io.NopCloser(strings.NewReader(input)), model.ProtocolOpenAIResponses)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if got := events[len(events)-1]; got.Type != provider.EventMessageStop ||
		got.StopReason != provider.StopReasonIncomplete {
		t.Fatalf("terminal event = %+v", got)
	}
}

func TestResponsesStreamClassifiesIncompleteDetails(t *testing.T) {
	for name, test := range map[string]struct {
		reason string
		want   provider.StopReason
	}{
		"max output tokens": {
			reason: "max_output_tokens",
			want:   provider.StopReasonMaxTokens,
		},
		"content filter": {
			reason: "content_filter",
			want:   provider.StopReasonContentFilter,
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := fmt.Sprintf(
				"data: {\"type\":\"response.incomplete\",\"response\":"+
					"{\"incomplete_details\":{\"reason\":%q}}}\n\n",
				test.reason,
			)
			stream, err := NewStream(
				io.NopCloser(strings.NewReader(input)),
				model.ProtocolOpenAIResponses,
			)
			if err != nil {
				t.Fatal(err)
			}
			events, err := provider.Drain(stream)
			if err != nil {
				t.Fatal(err)
			}
			if got := events[len(events)-1]; got.Type != provider.EventMessageStop ||
				got.StopReason != test.want {
				t.Fatalf("terminal event = %+v", got)
			}
		})
	}
}

func TestResponsesStreamCarriesStableSequenceIdentity(t *testing.T) {
	input := strings.Join([]string{
		`data: {"type":"response.output_text.delta","sequence_number":0,"event_id":"evt-0","delta":"one"}`,
		"",
		`data: {"type":"response.completed","sequence_number":1,"event_id":"evt-1","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`,
		"",
		"",
	}, "\n")
	stream, err := NewStream(
		io.NopCloser(strings.NewReader(input)),
		model.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 {
		t.Fatalf("events = %+v", events)
	}
	if !events[1].Sequenced || events[1].Sequence != 0 ||
		events[1].EventID != "evt-0#0" ||
		!events[2].Sequenced || events[2].Sequence != 1 ||
		events[2].Ordinal != 0 || events[3].Ordinal != 1 {
		t.Fatalf("event identities = %+v", events)
	}
}

func TestResponsesStreamRejectsUnknownFormatDrift(t *testing.T) {
	input := "data: {\"type\":\"response.future_delta\",\"delta\":\"unsafe\"}\n\n"
	stream, err := NewStream(
		io.NopCloser(strings.NewReader(input)),
		model.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = stream.Recv()
	if _, err := stream.Recv(); err == nil ||
		!strings.Contains(err.Error(), "unsupported OpenAI Responses") {
		t.Fatalf("format drift error = %v", err)
	}
}

func FuzzStreamParserOpenAI(f *testing.F) {
	f.Add([]byte(`{"choices":[{"delta":{"content":"hello"},"finish_reason":null}]}`))
	f.Add([]byte(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	f.Add([]byte(`{`))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			t.Skip()
		}
		_, _ = parseChatChunk(data, false)
		_, _ = parseResponsesChunk(data)
	})
}
