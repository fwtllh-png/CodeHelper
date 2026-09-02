package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/QCode/internal/adapter/provider/wire"
)

type Adapter struct{}

func NewAdapter() *Adapter           { return &Adapter{} }
func (*Adapter) ID() model.AdapterID { return model.AdapterAnthropic }
func (*Adapter) Supports(protocol model.WireProtocol) bool {
	return protocol == model.ProtocolAnthropic
}
func (*Adapter) Prepare(request provider.ModelRequest) (providerwire.PreparedCall, error) {
	if request.Route.Protocol() != model.ProtocolAnthropic {
		return providerwire.PreparedCall{}, fmt.Errorf(
			"adapter %q does not support protocol %q",
			model.AdapterAnthropic, request.Route.Protocol(),
		)
	}
	system, messages, err := messages(
		request.Messages,
		request.Route,
		request.Route.Model().Capabilities.PromptCache,
	)
	if err != nil {
		return providerwire.PreparedCall{}, err
	}
	body := map[string]any{
		"model": request.Route.Model().WireID, "messages": messages,
		"max_tokens": request.MaxOutputTokens, "stream": true,
	}
	if len(system) != 0 {
		body["system"] = system
	}
	if request.Temperature != nil {
		body["temperature"] = *request.Temperature
	}
	if request.ReasoningEffort != "" {
		budget, err := reasoningBudget(request)
		if err != nil {
			return providerwire.PreparedCall{}, err
		}
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
	}
	if request.NativeSearch {
		appendTool(body, map[string]any{"type": "web_search_20250305", "name": "web_search", "max_uses": 5})
	}
	for _, definition := range request.Tools {
		appendTool(body, map[string]any{"name": definition.Name, "description": definition.Description, "input_schema": definition.InputSchema})
	}
	data, err := json.Marshal(body)
	if err != nil {
		return providerwire.PreparedCall{}, err
	}
	return providerwire.PreparedCall{
		Method: http.MethodPost, Path: "/messages", Body: data,
		Headers: http.Header{
			"Content-Type":      []string{"application/json"},
			"Accept":            []string{"text/event-stream"},
			"Anthropic-Version": []string{"2023-06-01"},
		},
		Auth: providerwire.AuthAnthropicKey, Adapter: model.AdapterAnthropic,
		Protocol: model.ProtocolAnthropic,
	}, nil
}
func (*Adapter) OpenStream(body io.ReadCloser, _ providerwire.PreparedCall) (provider.Stream, error) {
	return NewStream(body)
}
func (*Adapter) ClassifyHTTP(failure providerwire.HTTPFailure) error {
	var payload struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(failure.Body), &payload)
	message := payload.Error.Message
	if message == "" {
		message = fmt.Sprintf("provider returned HTTP %d", failure.Status)
	}
	var code provider.FailureCode
	switch {
	case failure.Status == http.StatusUnauthorized ||
		failure.Status == http.StatusForbidden:
		code = provider.FailureAuth
	case providerwire.IsQuotaFailure(payload.Error.Type, message):
		code = provider.FailureQuota
	case failure.Status == http.StatusTooManyRequests:
		code = provider.FailureRateLimit
	case failure.Status >= 500:
		code = provider.FailureServer
	default:
		return providerwire.GenericHTTPFailure(failure)
	}
	return providerwire.TypedHTTPFailure(
		failure,
		code,
		message,
		providerwire.FirstHeader(
			failure.Header,
			"Request-Id",
			"X-Request-Id",
		),
	)
}
func messages(
	input []provider.Message,
	route model.ReadyRoute,
	promptCache bool,
) ([]map[string]any, []map[string]any, error) {
	var stable, volatile []string
	seenNonSystem := false
	result := make([]map[string]any, 0, len(input))
	for _, message := range input {
		switch message.Role {
		case provider.RoleSystem:
			text := message.Text()
			if text == "" {
				continue
			}
			if !seenNonSystem {
				stable = append(stable, text)
			} else {
				volatile = append(volatile, text)
			}
		case provider.RoleAssistant:
			seenNonSystem = true
			signatures, err := replaySignatures(message, route)
			if err != nil {
				return nil, nil, err
			}
			reasoningBlock := 0
			content := make([]map[string]any, 0, len(message.Blocks))
			for _, block := range message.Blocks {
				switch block.Type {
				case provider.ContentText:
					content = append(content, map[string]any{"type": "text", "text": block.Text})
				case provider.ContentReasoning:
					thinking := map[string]any{"type": "thinking", "thinking": block.Text}
					signature := signatures[reasoningBlock]
					if signature != "" {
						thinking["signature"] = signature
					}
					content = append(content, thinking)
					reasoningBlock++
				case provider.ContentToolCall:
					call := block.ToolCall
					var toolInput any
					if err := json.Unmarshal([]byte(call.Arguments), &toolInput); err != nil {
						return nil, nil, fmt.Errorf("decode Anthropic tool arguments for %s: %w", call.ID, err)
					}
					content = append(content, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": toolInput})
				}
			}
			result = append(result, map[string]any{"role": "assistant", "content": content})
		case provider.RoleTool:
			seenNonSystem = true
			content := make([]map[string]any, 0, len(message.Blocks))
			for _, block := range message.Blocks {
				if block.Type == provider.ContentToolResult {
					content = append(content, map[string]any{
						"type": "tool_result", "tool_use_id": block.ToolResult.CallID,
						"content": block.ToolResult.Content, "is_error": block.ToolResult.IsError,
					})
				}
			}
			result = append(result, map[string]any{"role": "user", "content": content})
		default:
			seenNonSystem = true
			content := make([]map[string]any, 0, len(message.Blocks))
			for _, block := range message.Blocks {
				switch block.Type {
				case provider.ContentText:
					content = append(content, map[string]any{"type": "text", "text": block.Text})
				case provider.ContentImage:
					content = append(content, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type": "base64", "media_type": block.Attachment.MediaType,
							"data": block.Attachment.Base64(),
						},
					})
				}
			}
			result = append(result, map[string]any{"role": message.Role, "content": content})
		}
	}
	system := make([]map[string]any, 0, len(stable)+len(volatile))
	for index, text := range stable {
		block := map[string]any{"type": "text", "text": text}
		if promptCache && index == len(stable)-1 {
			block["cache_control"] = map[string]any{"type": "ephemeral"}
		}
		system = append(system, block)
	}
	for _, text := range volatile {
		system = append(system, map[string]any{"type": "text", "text": text})
	}
	return system, result, nil
}
func reasoningBudget(request provider.ModelRequest) (uint64, error) {
	if request.MaxOutputTokens <= 1024 {
		return 0, errors.New("Anthropic thinking requires max output tokens greater than 1024")
	}
	budget := request.MaxOutputTokens / 2
	if budget < 1024 {
		budget = 1024
	}
	if budget >= request.MaxOutputTokens {
		return 0, errors.New("Anthropic thinking budget must be less than max output tokens")
	}
	return budget, nil
}
func appendTool(body map[string]any, definition map[string]any) {
	tools, _ := body["tools"].([]map[string]any)
	body["tools"] = append(tools, definition)
}
