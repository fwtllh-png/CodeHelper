package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type stream struct {
	body           io.ReadCloser
	decoder        *provider.SSEDecoder
	queue          []provider.StreamEvent
	started        bool
	stopped        bool
	closed         bool
	searchInputs   map[int]*strings.Builder
	pendingQueries []string
}

func NewStream(body io.ReadCloser) (provider.Stream, error) {
	if body == nil {
		return nil, errors.New("response body is required")
	}
	return &stream{
		body: body, decoder: provider.NewSSEDecoder(body), searchInputs: make(map[int]*strings.Builder),
	}, nil
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
			if errors.Is(err, io.EOF) {
				return provider.StreamEvent{}, errors.New("Anthropic stream ended before message_stop")
			}
			return provider.StreamEvent{}, err
		}
		events, err := s.parseChunk([]byte(record.Data))
		if err != nil {
			return provider.StreamEvent{}, err
		}
		for _, event := range events {
			if event.Type == provider.EventMessageStop {
				s.stopped = true
			}
			s.queue = append(s.queue, event)
		}
	}
}
func (s *stream) parseChunk(data []byte) ([]provider.StreamEvent, error) {
	var envelope struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type  string          `json:"type"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, fmt.Errorf("decode Anthropic stream chunk: %w", err)
	}
	switch envelope.Type {
	case "content_block_start":
		if envelope.ContentBlock.Type == "server_tool_use" && envelope.ContentBlock.Name == "web_search" {
			builder := &strings.Builder{}
			if len(envelope.ContentBlock.Input) != 0 && string(envelope.ContentBlock.Input) != "null" {
				builder.Write(envelope.ContentBlock.Input)
			}
			s.searchInputs[envelope.Index] = builder
			return nil, nil
		}
	case "content_block_delta":
		if envelope.Delta.Type == "input_json_delta" {
			if builder, exists := s.searchInputs[envelope.Index]; exists {
				builder.WriteString(envelope.Delta.PartialJSON)
				return nil, nil
			}
		}
	case "content_block_stop":
		if builder, exists := s.searchInputs[envelope.Index]; exists {
			s.pendingQueries = append(s.pendingQueries, searchQuery(builder.String()))
			delete(s.searchInputs, envelope.Index)
			return nil, nil
		}
	}
	events, err := parseChunk(data)
	if err != nil {
		return nil, err
	}
	for index := range events {
		if events[index].Type != provider.EventSearchResult || len(s.pendingQueries) == 0 {
			continue
		}
		events[index].Search.Query = s.pendingQueries[0]
		s.pendingQueries = s.pendingQueries[1:]
	}
	return events, nil
}
func searchQuery(input string) string {
	var value struct {
		Query string `json:"query"`
	}
	_ = json.Unmarshal([]byte(input), &value)
	return value.Query
}
func (s *stream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.stopped = true
	return s.body.Close()
}
func parseChunk(data []byte) ([]provider.StreamEvent, error) {
	var chunk struct {
		Type    string `json:"type"`
		Index   int    `json:"index"`
		Message struct {
			Usage struct {
				InputTokens         uint64 `json:"input_tokens"`
				CacheReadTokens     uint64 `json:"cache_read_input_tokens"`
				CacheCreationTokens uint64 `json:"cache_creation_input_tokens"`
			} `json:"usage"`
		} `json:"message"`
		ContentBlock struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Content   json.RawMessage `json:"content"`
			ErrorCode string          `json:"error_code"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			Thinking    string `json:"thinking"`
			Signature   string `json:"signature"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
			Citation    struct {
				Type           string `json:"type"`
				EncryptedIndex string `json:"encrypted_index"`
				URL            string `json:"url"`
				Title          string `json:"title"`
				StartCharIndex int    `json:"start_char_index"`
				EndCharIndex   int    `json:"end_char_index"`
			} `json:"citation"`
		} `json:"delta"`
		Usage struct {
			OutputTokens uint64 `json:"output_tokens"`
		} `json:"usage"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return nil, fmt.Errorf("decode Anthropic stream chunk: %w", err)
	}
	switch chunk.Type {
	case "message_start":
		// Anthropic reports cache reads and writes beside input_tokens.
		// Both enter the total, but only reads count as cache hits.
		usage := provider.Usage{
			InputTokens: chunk.Message.Usage.InputTokens +
				chunk.Message.Usage.CacheReadTokens +
				chunk.Message.Usage.CacheCreationTokens,
			CachedTokens: chunk.Message.Usage.CacheReadTokens,
		}
		if usage.InputTokens == 0 {
			return nil, nil
		}
		return []provider.StreamEvent{{Type: provider.EventUsage, Usage: &usage}}, nil
	case "content_block_start":
		switch chunk.ContentBlock.Type {
		case "web_search_tool_result":
			search := parseSearchContent(chunk.ContentBlock.Content)
			if chunk.ContentBlock.ErrorCode != "" {
				search.Error = chunk.ContentBlock.ErrorCode
			}
			block := provider.ContentBlock{Type: provider.ContentSearch, Search: &search}
			return []provider.StreamEvent{{Type: provider.EventSearchResult, Index: chunk.Index, Block: &block, Search: &search}}, nil
		case "server_tool_use":
			return nil, nil
		case "tool_use":
		default:
			return nil, nil
		}
		fragment := provider.ToolCallFragment{Index: chunk.Index, ID: chunk.ContentBlock.ID, Name: chunk.ContentBlock.Name}
		return []provider.StreamEvent{{Type: provider.EventToolCallDelta, ToolCall: &fragment}}, nil
	case "content_block_delta":
		switch chunk.Delta.Type {
		case "text_delta":
			if chunk.Delta.Text == "" {
				return nil, nil
			}
			block := provider.ContentBlock{Type: provider.ContentText, Text: chunk.Delta.Text}
			return []provider.StreamEvent{{Type: provider.EventTextDelta, Index: chunk.Index, Block: &block, Text: chunk.Delta.Text}}, nil
		case "thinking_delta":
			if chunk.Delta.Thinking == "" {
				return nil, nil
			}
			block := provider.ContentBlock{Type: provider.ContentReasoning, Text: chunk.Delta.Thinking}
			return []provider.StreamEvent{{Type: provider.EventReasoningDelta, Index: chunk.Index, Block: &block, Text: chunk.Delta.Thinking}}, nil
		case "signature_delta":
			if chunk.Delta.Signature == "" {
				return nil, nil
			}
			block := provider.ContentBlock{Type: provider.ContentReasoning, Signature: chunk.Delta.Signature}
			return []provider.StreamEvent{{
				Type: provider.EventReasoningSignature, Index: chunk.Index, Block: &block,
				Signature: chunk.Delta.Signature,
			}}, nil
		case "input_json_delta":
			fragment := provider.ToolCallFragment{Index: chunk.Index, Arguments: chunk.Delta.PartialJSON}
			return []provider.StreamEvent{{Type: provider.EventToolCallDelta, ToolCall: &fragment}}, nil
		case "citations_delta":
			if chunk.Delta.Citation.URL == "" {
				return nil, nil
			}
			citation := provider.Citation{
				SourceID: chunk.Delta.Citation.EncryptedIndex,
				URL:      chunk.Delta.Citation.URL,
				Title:    chunk.Delta.Citation.Title,
				Start:    chunk.Delta.Citation.StartCharIndex,
				End:      chunk.Delta.Citation.EndCharIndex,
			}
			block := provider.ContentBlock{Type: provider.ContentCitation, Citation: &citation}
			return []provider.StreamEvent{{Type: provider.EventCitation, Index: chunk.Index, Block: &block, Citation: &citation}}, nil
		default:
			return nil, nil
		}
	case "message_delta":
		var events []provider.StreamEvent
		if chunk.Usage.OutputTokens != 0 {
			events = append(events, provider.StreamEvent{
				Type:  provider.EventUsage,
				Usage: &provider.Usage{OutputTokens: chunk.Usage.OutputTokens},
			})
		}
		if chunk.Delta.StopReason != "" {
			events = append(events, provider.StreamEvent{
				Type:       provider.EventMessageStop,
				StopReason: anthropicStopReason(chunk.Delta.StopReason),
			})
		}
		return events, nil
	case "message_stop":
		return []provider.StreamEvent{{Type: provider.EventMessageStop}}, nil
	case "ping", "content_block_stop":
		return nil, nil
	case "error":
		return nil, fmt.Errorf("Anthropic stream error: %s", chunk.Error.Message)
	default:
		return nil, nil
	}
}
func anthropicStopReason(value string) provider.StopReason {
	switch value {
	case "end_turn", "stop_sequence", "refusal":
		return provider.StopReasonEndTurn
	case "tool_use":
		return provider.StopReasonToolUse
	case "max_tokens", "model_context_window_exceeded", "pause_turn":
		return provider.StopReasonMaxTokens
	default:
		return provider.StopReasonUnknown
	}
}
func parseSearchContent(data json.RawMessage) provider.SearchResult {
	result := provider.SearchResult{Sources: []provider.Source{}}
	if len(data) == 0 || string(data) == "null" {
		return result
	}
	var items []struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		Title     string `json:"title"`
		ErrorCode string `json:"error_code"`
	}
	if err := json.Unmarshal(data, &items); err == nil {
		for _, item := range items {
			if item.Type == "web_search_result" && item.URL != "" {
				result.Sources = append(result.Sources, provider.Source{Title: item.Title, URL: item.URL})
			}
			if item.ErrorCode != "" {
				result.Error = item.ErrorCode
			}
		}
		return result
	}
	var item struct {
		ErrorCode string `json:"error_code"`
	}
	if json.Unmarshal(data, &item) == nil {
		result.Error = item.ErrorCode
	}
	return result
}
