package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
)

type Adapter struct {
	id        model.AdapterID
	sessionMu sync.Mutex
	sessions  map[string]*responsesSession
}

func NewAdapter(id model.AdapterID) (*Adapter, error) {
	switch id {
	case model.AdapterOpenAI, model.AdapterDeepSeek, model.AdapterOpenAICompatible:
		return &Adapter{id: id}, nil
	default:
		return nil, fmt.Errorf("unsupported OpenAI-compatible adapter %q", id)
	}
}
func (a *Adapter) ID() model.AdapterID { return a.id }
func (a *Adapter) Supports(protocol model.WireProtocol) bool {
	return protocol == model.ProtocolOpenAIChat ||
		protocol == model.ProtocolOpenAIResponses
}
func (a *Adapter) Prepare(request provider.ModelRequest) (providerwire.PreparedCall, error) {
	var (
		body map[string]any
		path string
	)
	switch request.Route.Protocol() {
	case model.ProtocolOpenAIChat:
		body, path = chatBody(request), "/chat/completions"
	case model.ProtocolOpenAIResponses:
		input, err := responsesInput(request.Messages)
		if err != nil {
			return providerwire.PreparedCall{}, err
		}
		body, path = responsesBody(request, input), "/responses"
	default:
		return providerwire.PreparedCall{}, fmt.Errorf("adapter %q does not support protocol %q", a.id, request.Route.Protocol())
	}
	data, err := json.Marshal(body)
	if err != nil {
		return providerwire.PreparedCall{}, err
	}
	return providerwire.PreparedCall{
		Method: http.MethodPost, Path: path, Body: data,
		Headers: http.Header{
			"Content-Type": []string{"application/json"},
			"Accept":       []string{"text/event-stream"},
		},
		Auth: providerwire.AuthBearer, Adapter: a.id,
		Protocol: request.Route.Protocol(),
	}, nil
}
func (a *Adapter) OpenStream(body io.ReadCloser, call providerwire.PreparedCall) (provider.Stream, error) {
	return NewStream(body, call.Protocol)
}
func (a *Adapter) ClassifyHTTP(failure providerwire.HTTPFailure) error {
	return providerwire.GenericHTTPFailure(failure)
}
func chatBody(request provider.ModelRequest) map[string]any {
	body := map[string]any{
		"model": request.Route.Model().WireID, "messages": chatMessages(request.Messages),
		"max_tokens": request.MaxOutputTokens, "stream": true,
		"stream_options": map[string]bool{"include_usage": true},
	}
	applyOptional(body, request)
	applyChatTools(body, request.Tools)
	if request.NativeSearch {
		appendTool(body, map[string]any{"type": "web_search_preview"})
	}
	if request.PromptCacheKey != "" && request.Route.Model().Capabilities.PromptCache {
		body["prompt_cache_key"] = request.PromptCacheKey
	}
	return body
}
func responsesBody(request provider.ModelRequest, input []map[string]any) map[string]any {
	store := false
	if request.Store != nil {
		store = *request.Store
	}
	parallelTools := true
	if request.ParallelTools != nil {
		parallelTools = *request.ParallelTools
	}
	include := request.Include
	if len(include) == 0 && request.ReasoningEffort != "" {
		include = []string{"reasoning.encrypted_content"}
	}
	body := map[string]any{
		"model": request.Route.Model().WireID, "input": input,
		"max_output_tokens": request.MaxOutputTokens, "stream": true,
		"store": store, "parallel_tool_calls": parallelTools,
	}
	if len(include) != 0 {
		body["include"] = include
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.ReasoningEffort != "" {
		body["reasoning"] = map[string]any{"effort": request.ReasoningEffort, "summary": "auto"}
	}
	applyResponsesTools(body, request.Tools)
	if request.NativeSearch {
		appendTool(body, map[string]any{"type": "web_search"})
	}
	if request.PromptCacheKey != "" && request.Route.Model().Capabilities.PromptCache {
		body["prompt_cache_key"] = request.PromptCacheKey
	}
	return body
}
func chatMessages(messages []provider.Message) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := map[string]any{"role": message.Role}
		var text, reasoning string
		var calls, images []map[string]any
		for _, block := range message.Blocks {
			switch block.Type {
			case provider.ContentText:
				text += block.Text
			case provider.ContentReasoning:
				reasoning += block.Text
			case provider.ContentImage:
				images = append(images, map[string]any{"type": "image_url", "image_url": map[string]any{"url": block.Attachment.DataURL()}})
			case provider.ContentToolCall:
				call := block.ToolCall
				calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": call.Arguments}})
			case provider.ContentToolResult:
				item["tool_call_id"] = block.ToolResult.CallID
				text += block.ToolResult.Content
			}
		}
		if len(images) == 0 {
			item["content"] = text
		} else {
			content := make([]map[string]any, 0, len(images)+1)
			if text != "" {
				content = append(content, map[string]any{"type": "text", "text": text})
			}
			item["content"] = append(content, images...)
		}
		if reasoning != "" {
			item["reasoning_content"] = reasoning
		}
		if len(calls) != 0 {
			item["tool_calls"] = calls
		}
		result = append(result, item)
	}
	return result
}
func responsesInput(messages []provider.Message) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		if grouped, ok := responsesImageItem(message); ok {
			result = append(result, grouped)
			continue
		}
		for _, block := range message.Blocks {
			switch block.Type {
			case provider.ContentText:
				result = append(result, map[string]any{"role": message.Role, "content": block.Text})
			case provider.ContentReasoning:
				item, err := responsesReasoningItem(block)
				if err != nil {
					return nil, err
				}
				if item != nil {
					result = append(result, item)
				}
			case provider.ContentToolCall:
				call := block.ToolCall
				result = ensureReasoningBeforeFunctionCall(result)
				result = append(result, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Arguments})
			case provider.ContentToolResult:
				result = append(result, map[string]any{"type": "function_call_output", "call_id": block.ToolResult.CallID, "output": block.ToolResult.Content})
			case provider.ContentProvider:
				var item map[string]any
				if err := json.Unmarshal(block.ProviderData, &item); err != nil {
					return nil, fmt.Errorf("decode OpenAI provider replay block: %w", err)
				}
				result = append(result, item)
			}
		}
	}
	if err := validateResponsesToolPairs(result); err != nil {
		return nil, err
	}
	return result, nil
}
func validateResponsesToolPairs(input []map[string]any) error {
	calls := make(map[string]struct{})
	for index, item := range input {
		switch stringValue(item["type"]) {
		case "function_call":
			callID := stringValue(item["call_id"])
			if callID == "" {
				return fmt.Errorf("OpenAI Responses function_call at input %d has no call_id", index)
			}
			calls[callID] = struct{}{}
		case "function_call_output":
			callID := stringValue(item["call_id"])
			if _, exists := calls[callID]; callID == "" || !exists {
				return fmt.Errorf("OpenAI Responses function_call_output at input %d has no preceding function_call for call_id %q", index, callID)
			}
		}
	}
	return nil
}

const responsesReasoningPlaceholder = "(continued)"

func ensureReasoningBeforeFunctionCall(result []map[string]any) []map[string]any {
	if len(result) > 0 {
		switch stringValue(result[len(result)-1]["type"]) {
		case "reasoning", "function_call":
			return result
		}
	}
	return append(result, map[string]any{"type": "reasoning", "content": []map[string]any{{"type": "reasoning_text", "text": responsesReasoningPlaceholder}}})
}
func responsesReasoningItem(block provider.ContentBlock) (map[string]any, error) {
	text := strings.TrimSpace(block.Text)
	var item map[string]any
	if block.ProviderType == "openai_responses.reasoning" && len(block.ProviderData) != 0 {
		if err := json.Unmarshal(block.ProviderData, &item); err != nil {
			return nil, fmt.Errorf("decode OpenAI reasoning replay block: %w", err)
		}
		if text == "" {
			text = strings.TrimSpace(reasoningTextFromItem(item))
		}
	}
	if text == "" {
		return nil, nil
	}
	out := map[string]any{"type": "reasoning", "content": []map[string]any{{"type": "reasoning_text", "text": text}}}
	if id := stringValue(item["id"]); id != "" {
		out["id"] = id
	}
	return out, nil
}
func reasoningTextFromItem(item map[string]any) string {
	if item == nil {
		return ""
	}
	if text := reasoningTextFromContentParts(item["content"]); text != "" {
		return text
	}
	return reasoningTextFromContentParts(item["summary"])
}
func reasoningTextFromContentParts(value any) string {
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
				if text := stringValue(part["text"]); text != "" {
					parts = append(parts, text)
				} else if text := stringValue(part["reasoning_text"]); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return ""
	}
}
func responsesImageItem(message provider.Message) (map[string]any, bool) {
	var content []map[string]any
	images := false
	for _, block := range message.Blocks {
		switch block.Type {
		case provider.ContentText:
			content = append(content, map[string]any{"type": "input_text", "text": block.Text})
		case provider.ContentImage:
			images = true
			content = append(content, map[string]any{"type": "input_image", "image_url": block.Attachment.DataURL()})
		}
	}
	if !images {
		return nil, false
	}
	return map[string]any{"role": message.Role, "content": content}, true
}
func applyChatTools(body map[string]any, definitions []provider.ToolDefinition) {
	for _, definition := range definitions {
		function := map[string]any{
			"name": definition.Name, "description": definition.Description,
			"parameters": definition.InputSchema,
		}
		appendTool(body, map[string]any{"type": "function", "function": function})
	}
}
func applyResponsesTools(body map[string]any, definitions []provider.ToolDefinition) {
	for _, definition := range definitions {
		appendTool(body, map[string]any{"type": "function", "name": definition.Name, "description": definition.Description, "parameters": definition.InputSchema})
	}
}
func appendTool(body map[string]any, definition map[string]any) {
	tools, _ := body["tools"].([]map[string]any)
	body["tools"] = append(tools, definition)
}
func applyOptional(body map[string]any, request provider.ModelRequest) {
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.ReasoningEffort != "" {
		body["reasoning_effort"] = request.ReasoningEffort
	}
}
