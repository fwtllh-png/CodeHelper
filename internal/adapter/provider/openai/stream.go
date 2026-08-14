package openai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type stream struct {
	body       io.ReadCloser
	decoder    *provider.SSEDecoder
	protocol   model.WireProtocol
	queue      []provider.StreamEvent
	started    bool
	finished   bool
	stopReason provider.StopReason
	stopped    bool
	closed     bool
	responses  ResponsesDecoder
}

func NewStream(body io.ReadCloser, protocol model.WireProtocol) (provider.Stream, error) {
	if body == nil {
		return nil, errors.New("response body is required")
	}
	if protocol != model.ProtocolOpenAIChat && protocol != model.ProtocolOpenAIResponses {
		_ = body.Close()
		return nil, fmt.Errorf("unsupported OpenAI protocol %q", protocol)
	}
	return &stream{body: body, decoder: provider.NewSSEDecoder(body), protocol: protocol}, nil
}
func (s *stream) Recv() (provider.StreamEvent, error) {
	if !s.started {
		s.started = true
		return provider.StreamEvent{Type: provider.EventMessageStart}, nil
	}
	for {
		if len(s.queue) != 0 {
			event := s.queue[0]
			s.queue = s.queue[1:]
			return event, nil
		}
		if s.stopped {
			return provider.StreamEvent{}, io.EOF
		}
		record, err := s.decoder.Next()
		if err != nil {
			if errors.Is(err, io.EOF) && s.protocol == model.ProtocolOpenAIChat && s.finished {
				s.enqueueStop(s.stopReason)
				continue
			}
			if errors.Is(err, io.EOF) && !s.stopped {
				return provider.StreamEvent{}, errors.New("OpenAI stream ended before completion")
			}
			return provider.StreamEvent{}, err
		}
		if record.Data == "[DONE]" {
			s.enqueueStop(s.stopReason)
			continue
		}
		var events []provider.StreamEvent
		if s.protocol == model.ProtocolOpenAIChat {
			events, err = parseChatChunk([]byte(record.Data))
		} else {
			events, err = s.responses.Decode([]byte(record.Data))
		}
		if err != nil {
			return provider.StreamEvent{}, err
		}
		var stopReason provider.StopReason
		for _, event := range events {
			if event.Type == provider.EventMessageStop {
				if s.protocol == model.ProtocolOpenAIChat {
					s.finished = true
					s.stopReason = event.StopReason
					continue
				}
				stopReason = event.StopReason
				continue
			}
			s.queue = append(s.queue, event)
		}
		if stopReason != "" {
			s.enqueueStop(stopReason)
		}
	}
}

// ResponsesDecoder turns one Responses JSON event into provider events while
// reconciling providers that emit both reasoning deltas and final snapshots.
type ResponsesDecoder struct {
	CaptureState   bool
	reasoning      map[int]string
	reasoningFinal map[int]bool
}

func (d *ResponsesDecoder) Decode(data []byte) ([]provider.StreamEvent, error) {
	events, err := parseResponsesChunk(data)
	if err != nil {
		return nil, err
	}
	if d.reasoning == nil {
		d.reasoning = make(map[int]string)
		d.reasoningFinal = make(map[int]bool)
	}
	reconciled := make([]provider.StreamEvent, 0, len(events))
	for _, event := range events {
		if event.Type == provider.EventResponseState && !d.CaptureState {
			continue
		}
		if event.Type != provider.EventReasoningDelta {
			reconciled = append(reconciled, event)
			continue
		}
		seen := d.reasoning[event.Index]
		visible := event.Text
		if strings.HasPrefix(visible, seen) {
			visible = strings.TrimPrefix(visible, seen)
		} else if seen != "" && strings.HasSuffix(seen, visible) {
			visible = ""
		}
		if visible != "" {
			d.reasoning[event.Index] = seen + visible
		}
		event.Text = visible
		if event.Block != nil && len(event.Block.ProviderData) == 0 {
			event.Block.Text = visible
		}
		hasProviderData := event.Block != nil && len(event.Block.ProviderData) != 0
		if visible == "" && (!hasProviderData || d.reasoningFinal[event.Index]) {
			continue
		}
		if hasProviderData {
			d.reasoningFinal[event.Index] = true
		}
		reconciled = append(reconciled, event)
	}
	return reconciled, nil
}
func (s *stream) enqueueStop(reason provider.StopReason) {
	if s.stopped {
		return
	}
	s.stopped = true
	s.queue = append(s.queue, provider.StreamEvent{
		Type: provider.EventMessageStop, StopReason: reason,
	})
}
func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.stopped = true
	return s.body.Close()
}
func parseChatChunk(data []byte) ([]provider.StreamEvent, error) {
	var chunk struct {
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				Reasoning        string `json:"reasoning"`
				ReasoningContent string `json:"reasoning_content"`
				Annotations      []struct {
					Type        string `json:"type"`
					URLCitation struct {
						URL        string `json:"url"`
						Title      string `json:"title"`
						StartIndex int    `json:"start_index"`
						EndIndex   int    `json:"end_index"`
					} `json:"url_citation"`
				} `json:"annotations"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     uint64 `json:"prompt_tokens"`
			CompletionTokens uint64 `json:"completion_tokens"`
			Details          struct {
				ReasoningTokens uint64 `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
			PromptDetails struct {
				CachedTokens uint64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, fmt.Errorf("decode OpenAI stream chunk: %w", err)
	}
	if chunk.Error != nil {
		return nil, fmt.Errorf("OpenAI stream error: %s", chunk.Error.Message)
	}
	var events []provider.StreamEvent
	for _, choice := range chunk.Choices {
		if choice.Delta.ReasoningContent != "" {
			block := provider.ContentBlock{Type: provider.ContentReasoning, Text: choice.Delta.ReasoningContent}
			events = append(events, provider.StreamEvent{Type: provider.EventReasoningDelta, Block: &block, Text: choice.Delta.ReasoningContent})
		} else if choice.Delta.Reasoning != "" {
			block := provider.ContentBlock{Type: provider.ContentReasoning, Text: choice.Delta.Reasoning}
			events = append(events, provider.StreamEvent{Type: provider.EventReasoningDelta, Block: &block, Text: choice.Delta.Reasoning})
		}
		if choice.Delta.Content != "" {
			block := provider.ContentBlock{Type: provider.ContentText, Text: choice.Delta.Content}
			events = append(events, provider.StreamEvent{Type: provider.EventTextDelta, Block: &block, Text: choice.Delta.Content})
		}
		for _, call := range choice.Delta.ToolCalls {
			fragment := provider.ToolCallFragment{
				Index: call.Index, ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments,
			}
			events = append(events, provider.StreamEvent{Type: provider.EventToolCallDelta, ToolCall: &fragment})
		}
		for _, annotation := range choice.Delta.Annotations {
			if annotation.Type != "url_citation" || annotation.URLCitation.URL == "" {
				continue
			}
			citation := provider.Citation{
				URL: annotation.URLCitation.URL, Title: annotation.URLCitation.Title,
				Start: annotation.URLCitation.StartIndex, End: annotation.URLCitation.EndIndex,
			}
			block := provider.ContentBlock{Type: provider.ContentCitation, Citation: &citation}
			events = append(events, provider.StreamEvent{Type: provider.EventCitation, Block: &block, Citation: &citation})
		}
		if choice.FinishReason != "" {
			events = append(events, provider.StreamEvent{
				Type:       provider.EventMessageStop,
				StopReason: openAIStopReason(choice.FinishReason),
			})
		}
	}
	if chunk.Usage != nil {
		events = append(events, provider.StreamEvent{
			Type: provider.EventUsage,
			Usage: &provider.Usage{
				InputTokens:     chunk.Usage.PromptTokens,
				OutputTokens:    chunk.Usage.CompletionTokens,
				ReasoningTokens: chunk.Usage.Details.ReasoningTokens,
				CachedTokens:    chunk.Usage.PromptDetails.CachedTokens,
			},
		})
	}
	return events, nil
}
func openAIStopReason(value string) provider.StopReason {
	switch value {
	case "stop":
		return provider.StopReasonEndTurn
	case "tool_calls", "function_call":
		return provider.StopReasonToolUse
	case "length", "max_tokens":
		return provider.StopReasonMaxTokens
	case "content_filter":
		return provider.StopReasonContentFilter
	default:
		return provider.StopReasonUnknown
	}
}
func parseResponsesChunk(data []byte) ([]provider.StreamEvent, error) {
	var chunk map[string]any
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, fmt.Errorf("decode OpenAI Responses chunk: %w", err)
	}
	eventType, _ := chunk["type"].(string)
	switch eventType {
	case "response.output_text.delta":
		return textEvent(provider.EventTextDelta, chunk["delta"]), nil
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		return responsesReasoningTextEvent(chunk, chunk["delta"]), nil
	case "response.reasoning_text.done", "response.reasoning_summary_text.done":
		if text := firstString(chunk["text"], chunk["delta"]); text != "" {
			return responsesReasoningTextEvent(chunk, text), nil
		}
		return nil, nil
	case "response.output_item.added":
		item, _ := chunk["item"].(map[string]any)
		itemType := stringValue(item["type"])
		if itemType == "reasoning" {
			return nil, nil
		}
		if itemType != "function_call" {
			return nil, nil
		}
		fragment := provider.ToolCallFragment{
			Index: int(number(chunk["output_index"])),
			ID:    firstString(item["call_id"], item["id"]),
			Name:  stringValue(item["name"]),
		}
		return []provider.StreamEvent{{Type: provider.EventToolCallDelta, ToolCall: &fragment}}, nil
	case "response.function_call_arguments.delta":
		fragment := provider.ToolCallFragment{
			Index:     int(number(chunk["output_index"])),
			ID:        stringValue(chunk["item_id"]),
			Name:      stringValue(chunk["name"]),
			Arguments: stringValue(chunk["delta"]),
		}
		return []provider.StreamEvent{{Type: provider.EventToolCallDelta, ToolCall: &fragment}}, nil
	case "response.web_search_call.completed", "response.output_item.done":
		item, _ := chunk["item"].(map[string]any)
		if len(item) == 0 {
			item, _ = chunk["output_item"].(map[string]any)
		}
		if stringValue(item["type"]) == "reasoning" {
			return reasoningItemEvents(item, int(number(chunk["output_index"])))
		}
		if stringValue(item["type"]) != "web_search_call" && eventType == "response.output_item.done" {
			return nil, nil
		}
		search := searchResult(item)
		if search.Query == "" && len(search.Sources) == 0 && search.Error == "" {
			return nil, nil
		}
		block := provider.ContentBlock{Type: provider.ContentSearch, Search: &search}
		return []provider.StreamEvent{{Type: provider.EventSearchResult, Block: &block, Search: &search}}, nil
	case "response.output_text.annotation.added":
		annotation, _ := chunk["annotation"].(map[string]any)
		if stringValue(annotation["type"]) != "url_citation" || stringValue(annotation["url"]) == "" {
			return nil, nil
		}
		citation := provider.Citation{
			SourceID: firstString(annotation["source_id"], annotation["id"]),
			URL:      stringValue(annotation["url"]),
			Title:    stringValue(annotation["title"]),
			Start:    int(number(annotation["start_index"])),
			End:      int(number(annotation["end_index"])),
		}
		block := provider.ContentBlock{Type: provider.ContentCitation, Citation: &citation}
		return []provider.StreamEvent{{Type: provider.EventCitation, Block: &block, Citation: &citation}}, nil
	case "response.completed", "response.incomplete":
		var events []provider.StreamEvent
		if response, ok := chunk["response"].(map[string]any); ok {
			if id := stringValue(response["id"]); id != "" {
				state := &provider.ResponseState{ID: id}
				if output, ok := response["output"].([]any); ok {
					for _, item := range output {
						raw, err := json.Marshal(item)
						if err != nil {
							return nil, err
						}
						state.Output = append(state.Output, raw)
					}
				}
				events = append(events, provider.StreamEvent{
					Type: provider.EventResponseState, Response: state,
				})
			}
			// Final output recovers reasoning omitted from deltas.
			if output, ok := response["output"].([]any); ok {
				for index, raw := range output {
					item, _ := raw.(map[string]any)
					if stringValue(item["type"]) != "reasoning" {
						continue
					}
					itemEvents, err := reasoningItemEvents(item, index)
					if err != nil {
						return nil, err
					}
					events = append(events, itemEvents...)
				}
			}
			if usage, ok := response["usage"].(map[string]any); ok {
				events = append(events, provider.StreamEvent{
					Type: provider.EventUsage,
					Usage: &provider.Usage{
						InputTokens:     uint64(number(usage["input_tokens"])),
						OutputTokens:    uint64(number(usage["output_tokens"])),
						ReasoningTokens: nestedUint(usage, "output_tokens_details", "reasoning_tokens"),
						CachedTokens:    nestedUint(usage, "input_tokens_details", "cached_tokens"),
					},
				})
			}
		}
		reason := provider.StopReasonEndTurn
		if eventType == "response.incomplete" {
			reason = provider.StopReasonIncomplete
			if response, ok := chunk["response"].(map[string]any); ok {
				if details, ok := response["incomplete_details"].(map[string]any); ok {
					switch stringValue(details["reason"]) {
					case "max_output_tokens":
						reason = provider.StopReasonMaxTokens
					case "content_filter":
						reason = provider.StopReasonContentFilter
					}
				}
			}
		}
		return append(events, provider.StreamEvent{
			Type: provider.EventMessageStop, StopReason: reason,
		}), nil
	case "error", "response.failed":
		message := stringValue(chunk["message"])
		if message == "" {
			if errObj, ok := chunk["error"].(map[string]any); ok {
				message = firstString(errObj["message"], errObj["code"])
			}
		}
		if message == "" {
			if response, ok := chunk["response"].(map[string]any); ok {
				if errObj, ok := response["error"].(map[string]any); ok {
					message = firstString(errObj["message"], errObj["code"])
				}
			}
		}
		if message == "" {
			message = "unknown Responses stream failure"
		}
		return nil, fmt.Errorf("OpenAI Responses stream error: %s", message)
	default:
		return nil, nil
	}
}
func responsesReasoningTextEvent(
	chunk map[string]any,
	value any,
) []provider.StreamEvent {
	events := textEvent(provider.EventReasoningDelta, value)
	for index := range events {
		events[index].Index = int(number(chunk["output_index"]))
		if events[index].Block != nil {
			events[index].Block.ID = firstString(chunk["item_id"], chunk["id"])
		}
	}
	return events
}
func reasoningItemEvents(item map[string]any, index int) ([]provider.StreamEvent, error) {
	raw, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	text := reasoningTextFromResponsesItem(item)
	block := provider.ContentBlock{
		Type: provider.ContentReasoning, ID: stringValue(item["id"]),
		Text: text, ProviderType: "openai_responses.reasoning", ProviderData: raw,
	}
	return []provider.StreamEvent{{
		Type: provider.EventReasoningDelta, Index: index, Text: text, Block: &block,
	}}, nil
}
func reasoningTextFromResponsesItem(item map[string]any) string {
	if item == nil {
		return ""
	}
	if text := reasoningTextFromParts(item["content"]); text != "" {
		return text
	}
	return reasoningTextFromParts(item["summary"])
}
func reasoningTextFromParts(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var parts []string
		for _, raw := range content {
			part, _ := raw.(map[string]any)
			if part == nil {
				continue
			}
			switch stringValue(part["type"]) {
			case "reasoning_text", "output_text", "summary_text", "text", "":
				if text := firstString(part["text"], part["reasoning_text"]); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}
func searchResult(item map[string]any) provider.SearchResult {
	action, _ := item["action"].(map[string]any)
	result := provider.SearchResult{Query: stringValue(action["query"])}
	if errValue, ok := item["error"].(map[string]any); ok {
		result.Error = firstString(errValue["message"], errValue["code"])
	}
	if result.Error == "" && stringValue(item["status"]) == "failed" {
		result.Error = "web search failed"
	}
	for _, key := range []string{"sources", "results"} {
		values, _ := action[key].([]any)
		for _, value := range values {
			source, _ := value.(map[string]any)
			url := stringValue(source["url"])
			if url == "" {
				continue
			}
			result.Sources = append(result.Sources, provider.Source{
				ID: firstString(source["id"], source["source_id"]), Title: stringValue(source["title"]), URL: url,
			})
		}
	}
	if result.Sources == nil {
		result.Sources = []provider.Source{}
	}
	return result
}
func textEvent(eventType provider.StreamEventType, value any) []provider.StreamEvent {
	text := stringValue(value)
	if text == "" {
		return nil
	}
	blockType := provider.ContentText
	if eventType == provider.EventReasoningDelta {
		blockType = provider.ContentReasoning
	}
	block := provider.ContentBlock{Type: blockType, Text: text}
	return []provider.StreamEvent{{Type: eventType, Block: &block, Text: text}}
}
func nestedUint(value map[string]any, parent, child string) uint64 {
	nested, _ := value[parent].(map[string]any)
	return uint64(number(nested[child]))
}
func number(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case json.Number:
		result, _ := typed.Float64()
		return result
	case string:
		result, _ := strconv.ParseFloat(typed, 64)
		return result
	default:
		return 0
	}
}
func stringValue(value any) string {
	result, _ := value.(string)
	return result
}
func firstString(values ...any) string {
	for _, value := range values {
		if result := stringValue(value); result != "" {
			return result
		}
	}
	return ""
}
