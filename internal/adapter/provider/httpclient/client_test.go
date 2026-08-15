package httpclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	runtimeapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestClientOpenAIRequestAndStream(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("Authorization = %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(writer, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n")
		_, _ = io.WriteString(writer, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := testClient()
	client.Credentials = staticCredentials("test-key")
	request := testRequest(t, server.URL, model.ProtocolOpenAIChat)
	request.NativeSearch = true
	request.Tools = []provider.ToolDefinition{{
		Name: "read", Description: "read a file",
		InputSchema: map[string]any{"type": "object"},
	}}
	stream, err := client.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[1].Text != "hello" || events[2].Usage.OutputTokens != 1 {
		t.Fatalf("events = %+v", events)
	}
	if requestBody["model"] != "wire-model" || requestBody["stream"] != true {
		t.Fatalf("request body = %+v", requestBody)
	}
	tools, exists := requestBody["tools"].([]any)
	if !exists || len(tools) != 2 {
		t.Fatalf("native search missing from request: %+v", requestBody)
	}
}

func TestClientAnthropicRequest(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/messages" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("x-api-key"); got != "anthropic-key" {
			t.Errorf("x-api-key = %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(writer, "data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	client := testClient()
	client.Credentials = staticCredentials("anthropic-key")
	request := testRequest(t, server.URL, model.ProtocolAnthropic)
	request.Messages = append([]provider.Message{provider.TextMessage(provider.RoleSystem, "system")}, request.Messages...)
	request.MaxOutputTokens = 2048
	request.ReasoningEffort = "high"
	request.NativeSearch = true
	request.Tools = []provider.ToolDefinition{{
		Name: "read", Description: "read", InputSchema: map[string]any{"type": "object"},
	}}
	stream, err := client.Stream(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Drain(stream); err != nil {
		t.Fatal(err)
	}
	if requestBody["system"] != nil {
		system, ok := requestBody["system"].([]any)
		if !ok || len(system) != 1 {
			t.Fatalf("system blocks = %#v", requestBody["system"])
		}
		block, _ := system[0].(map[string]any)
		if block["type"] != "text" || block["text"] != "system" {
			t.Fatalf("system block = %#v", block)
		}
		if _, ok := block["cache_control"]; ok {
			t.Fatalf("fixture without prompt_cache must omit cache_control: %#v", block)
		}
	} else {
		t.Fatalf("request body missing system: %+v", requestBody)
	}
	if _, exists := requestBody["thinking"]; !exists {
		t.Fatalf("thinking config missing: %+v", requestBody)
	}
	if tools, ok := requestBody["tools"].([]any); !ok || len(tools) != 2 {
		t.Fatalf("native search and regular tool do not coexist: %+v", requestBody)
	}
}

func TestClientRejectsInvalidAnthropicThinkingBudget(t *testing.T) {
	client := testClient()
	request := testRequest(t, "http://127.0.0.1:1", model.ProtocolAnthropic)
	request.MaxOutputTokens = 1024
	request.ReasoningEffort = "high"

	_, err := client.Stream(t.Context(), request)

	if !protocol.IsCode(err, protocol.CodeInvalidArgument) {
		t.Fatalf("Stream() error = %v, code=%q", err, protocol.CodeOf(err))
	}
}

func TestClientOpenAIResponsesRequest(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/responses" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if err := json.NewDecoder(request.Body).Decode(&requestBody); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	defer server.Close()

	client := testClient()
	req := testRequest(t, server.URL, model.ProtocolOpenAIResponses)
	req.Tools = []provider.ToolDefinition{{
		Name: "echo", Description: "echo", InputSchema: map[string]any{"type": "object"},
	}}
	req.NativeSearch = true
	stream, err := client.Stream(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Drain(stream); err != nil {
		t.Fatal(err)
	}
	if requestBody["max_output_tokens"] != float64(128) {
		t.Fatalf("request body = %+v", requestBody)
	}
	tools, _ := requestBody["tools"].([]any)
	if len(tools) < 2 {
		t.Fatalf("tools = %#v", requestBody["tools"])
	}
	fn, _ := tools[0].(map[string]any)
	if fn["type"] != "function" || fn["name"] != "echo" || fn["function"] != nil {
		t.Fatalf("responses function tool must be flat: %#v", fn)
	}
	search, _ := tools[1].(map[string]any)
	if search["type"] != "web_search" {
		t.Fatalf("native search tool = %#v", search)
	}
}

// TestBundledResponsesCatalogEntryEncodesToTheResponsesPath is the T5
// acceptance: a route taken from the bundled catalog, with no custom endpoint
// metadata, still produces a /responses body rather than /chat/completions.
func TestBundledResponsesCatalogEntryEncodesToTheResponsesPath(t *testing.T) {
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "openai-responses", ModelID: "gpt-4.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	data, path, err := encodeRequest(provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleUser, "hello"),
		},
		MaxOutputTokens: 128,
		PromptCacheKey:  "session-1",
		Idempotent:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/responses" {
		t.Fatalf("path = %q, want /responses", path)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if body["model"] != "gpt-4.1" {
		t.Fatalf("model = %#v", body["model"])
	}
	if body["prompt_cache_key"] != "session-1" {
		t.Fatalf("prompt_cache_key missing: %#v", body)
	}
	if _, ok := body["messages"]; ok {
		t.Fatalf("Responses body must not use chat messages: %#v", body)
	}
	if _, ok := body["input"]; !ok {
		t.Fatalf("Responses body missing input: %#v", body)
	}
}

func TestEncodeOmitsPromptCacheKeyWithoutCapability(t *testing.T) {
	request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
	request.PromptCacheKey = "session-1"
	data, _, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if _, ok := body["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key should be omitted without capability: %#v", body)
	}
}

func TestEncodeChatPromptCacheKeyWithCapability(t *testing.T) {
	withCache := testRequestWithPromptCache(t, "https://provider.test", model.ProtocolOpenAIChat, true)
	withCache.PromptCacheKey = "session-chat"
	data, path, err := encodeRequest(withCache)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/chat/completions" {
		t.Fatalf("path = %q", path)
	}
	var body map[string]any
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if body["prompt_cache_key"] != "session-chat" {
		t.Fatalf("prompt_cache_key missing: %#v", body)
	}

	without := testRequestWithPromptCache(t, "https://provider.test", model.ProtocolOpenAIChat, false)
	without.PromptCacheKey = "session-chat"
	data, _, err = encodeRequest(without)
	if err != nil {
		t.Fatal(err)
	}
	var withoutBody map[string]any
	if err := json.Unmarshal(data, &withoutBody); err != nil {
		t.Fatal(err)
	}
	if _, ok := withoutBody["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key should be omitted without capability: %#v", withoutBody)
	}
}

func TestEncodeAnthropicSystemCacheControl(t *testing.T) {
	request := testRequestWithPromptCache(t, "https://provider.test", model.ProtocolAnthropic, true)
	request.Messages = []provider.Message{
		provider.TextMessage(provider.RoleSystem, "stable-a"),
		provider.TextMessage(provider.RoleSystem, "stable-b"),
		provider.TextMessage(provider.RoleUser, "hello"),
		provider.TextMessage(provider.RoleSystem, "volatile-turn"),
	}
	data, path, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/messages" {
		t.Fatalf("path = %q", path)
	}
	var body struct {
		System   []map[string]any `json:"system"`
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.System) != 3 {
		t.Fatalf("system = %#v", body.System)
	}
	if body.System[0]["text"] != "stable-a" || body.System[0]["cache_control"] != nil {
		t.Fatalf("first stable = %#v", body.System[0])
	}
	control, _ := body.System[1]["cache_control"].(map[string]any)
	if body.System[1]["text"] != "stable-b" || control["type"] != "ephemeral" {
		t.Fatalf("last stable = %#v", body.System[1])
	}
	if body.System[2]["text"] != "volatile-turn" || body.System[2]["cache_control"] != nil {
		t.Fatalf("volatile = %#v", body.System[2])
	}
	if len(body.Messages) != 1 || body.Messages[0]["role"] != "user" {
		t.Fatalf("messages = %#v", body.Messages)
	}

	noCache := testRequestWithPromptCache(t, "https://provider.test", model.ProtocolAnthropic, false)
	noCache.Messages = []provider.Message{
		provider.TextMessage(provider.RoleSystem, "stable"),
		provider.TextMessage(provider.RoleUser, "hello"),
	}
	data, _, err = encodeRequest(noCache)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.System) != 1 || body.System[0]["cache_control"] != nil {
		t.Fatalf("no-cache system = %#v", body.System)
	}
}

func TestEncodeToolHistoryByProtocol(t *testing.T) {
	messages := []provider.Message{
		provider.TextMessage(provider.RoleUser, "read"),
		{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{
			{Type: provider.ContentText, Text: "checking"},
			{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{
				ID: "call_1", Name: "read", Arguments: `{"path":"a.txt"}`,
			}},
		}},
		{Role: provider.RoleTool, Blocks: []provider.ContentBlock{{
			Type:       provider.ContentToolResult,
			ToolResult: &provider.ToolResult{CallID: "call_1", Content: `{"content":"hello"}`},
		}}},
	}
	t.Run("responses", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = messages
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		// Thinking-mode providers need a reasoning item before function_call
		// even when the assistant turn only stored text + tools.
		if len(body.Input) != 5 ||
			body.Input[1]["role"] != "assistant" ||
			body.Input[2]["type"] != "reasoning" ||
			body.Input[3]["type"] != "function_call" ||
			body.Input[3]["call_id"] != "call_1" ||
			body.Input[4]["type"] != "function_call_output" ||
			body.Input[4]["call_id"] != "call_1" {
			t.Fatalf("input = %#v", body.Input)
		}
	})
	t.Run("anthropic", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolAnthropic)
		request.Messages = messages
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		assistantContent, _ := body.Messages[1]["content"].([]any)
		toolUse, _ := assistantContent[1].(map[string]any)
		resultContent, _ := body.Messages[2]["content"].([]any)
		toolResult, _ := resultContent[0].(map[string]any)
		if len(body.Messages) != 3 ||
			body.Messages[1]["role"] != "assistant" ||
			toolUse["type"] != "tool_use" ||
			toolUse["id"] != "call_1" ||
			body.Messages[2]["role"] != "user" ||
			toolResult["type"] != "tool_result" ||
			toolResult["tool_use_id"] != "call_1" {
			t.Fatalf("messages = %#v", body.Messages)
		}
	})
}

// TestEncodeImageByProtocol pins the wire shape of an image in all three
// protocols. The shapes differ enough that a single mistake is invisible until a
// provider answers 400 about a field the caller never named.
func TestEncodeImageByProtocol(t *testing.T) {
	// "PNG" as bytes, so the base64 in the assertions is readable rather than a
	// wall of pixels.
	imaged := []provider.Message{{
		Role: provider.RoleUser,
		Blocks: []provider.ContentBlock{
			{Type: provider.ContentText, Text: "what is this"},
			{Type: provider.ContentImage, Attachment: &provider.Attachment{
				MediaType: "image/png", Data: []byte("PNG"), Name: "shot.png",
			}},
		},
	}}
	const dataURL = "data:image/png;base64,UE5H"

	t.Run("chat completions", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIChat)
		request.Messages = imaged
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		content, _ := body.Messages[0]["content"].([]any)
		if len(content) != 2 {
			t.Fatalf("content = %#v", body.Messages[0]["content"])
		}
		text, _ := content[0].(map[string]any)
		image, _ := content[1].(map[string]any)
		url, _ := image["image_url"].(map[string]any)
		if text["type"] != "text" || text["text"] != "what is this" {
			t.Fatalf("text part = %#v", text)
		}
		if image["type"] != "image_url" || url["url"] != dataURL {
			t.Fatalf("image part = %#v", image)
		}
	})

	// A text-only message must keep the plain string content it always had:
	// every request body is a prompt cache key, so switching the shape for all
	// traffic would invalidate every cached prefix.
	t.Run("chat completions without an image keeps string content", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIChat)
		request.Messages = []provider.Message{provider.TextMessage(provider.RoleUser, "plain")}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if body.Messages[0]["content"] != "plain" {
			t.Fatalf("content = %#v", body.Messages[0]["content"])
		}
	})

	t.Run("responses", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = imaged
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		// The image and the question that asks about it have to arrive as one
		// input item, or the model is shown a picture and asked about nothing.
		if len(body.Input) != 1 {
			t.Fatalf("input = %#v", body.Input)
		}
		content, _ := body.Input[0]["content"].([]any)
		text, _ := content[0].(map[string]any)
		image, _ := content[1].(map[string]any)
		if text["type"] != "input_text" || text["text"] != "what is this" {
			t.Fatalf("text part = %#v", text)
		}
		if image["type"] != "input_image" || image["image_url"] != dataURL {
			t.Fatalf("image part = %#v", image)
		}
	})

	t.Run("anthropic", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolAnthropic)
		request.Messages = imaged
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		content, _ := body.Messages[0]["content"].([]any)
		image, _ := content[1].(map[string]any)
		source, _ := image["source"].(map[string]any)
		if image["type"] != "image" ||
			source["type"] != "base64" ||
			source["media_type"] != "image/png" ||
			source["data"] != "UE5H" {
			t.Fatalf("image block = %#v", image)
		}
	})
}

func TestEncodeReasoningReplayByProtocol(t *testing.T) {
	t.Run("anthropic thinking signature", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolAnthropic)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "first"),
			provider.ProducedAssistant(
				request.Route,
				[]provider.ContentBlock{
					{Type: provider.ContentReasoning, Text: "private thought"},
					{Type: provider.ContentText, Text: "answer"},
				},
				1,
				&provider.ReplayState{
					Version: provider.ReplayVersion,
					Data: json.RawMessage(
						`{"signatures":[{"block":0,"value":"signed-value"}]}`,
					),
				},
			),
			provider.TextMessage(provider.RoleUser, "second"),
		}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Messages []struct {
				Content []map[string]any `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		thinking := body.Messages[1].Content[0]
		if thinking["type"] != "thinking" ||
			thinking["thinking"] != "private thought" ||
			thinking["signature"] != "signed-value" {
			t.Fatalf("second-round thinking replay = %#v", thinking)
		}
	})

	t.Run("responses encrypted reasoning becomes neutral plaintext", func(t *testing.T) {
		raw := json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"ciphertext","summary":[]}`)
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "first"),
			provider.ProducedAssistant(
				request.Route,
				[]provider.ContentBlock{{
					Type: provider.ContentReasoning, ID: "rs_1", Text: "inspect",
				}},
				1,
				responsesReplayState(raw),
			),
			provider.TextMessage(provider.RoleUser, "second"),
		}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Input) != 3 || body.Input[1]["type"] != "reasoning" ||
			body.Input[2]["role"] != "user" {
			t.Fatalf("reasoning replay = %#v", body.Input)
		}
		if _, exists := body.Input[1]["encrypted_content"]; exists {
			t.Fatalf("encrypted replay leaked: %#v", body.Input[1])
		}
	})

	t.Run("responses drops empty reasoning shells", func(t *testing.T) {
		raw := json.RawMessage(`{"type":"reasoning","id":"rs_empty","content":[],"summary":[]}`)
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "q"),
			provider.ProducedAssistant(
				request.Route,
				[]provider.ContentBlock{
					{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{ID: "c1", Name: "echo", Arguments: `{}`}},
				},
				1,
				responsesReplayState(raw),
			),
		}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		// Empty shell is dropped, but the tool call still needs a non-empty
		// placeholder so DeepSeek thinking mode accepts the replay.
		if len(body.Input) < 3 || body.Input[1]["type"] != "reasoning" || body.Input[2]["type"] != "function_call" {
			t.Fatalf("expected placeholder reasoning before tool call: %#v", body.Input)
		}
		content, _ := body.Input[1]["content"].([]any)
		part, _ := content[0].(map[string]any)
		if part["text"] != responsesReasoningPlaceholder {
			t.Fatalf("placeholder = %#v", part)
		}
		if body.Input[1]["id"] != nil {
			t.Fatalf("placeholder must not reuse empty shell id: %#v", body.Input[1])
		}
	})

	t.Run("responses injects reasoning before orphan tool call", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "q"),
			{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{
				{Type: provider.ContentReasoning, Text: "first"},
				{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{ID: "c1", Name: "echo", Arguments: `{}`}},
			}},
			{Role: provider.RoleTool, Blocks: []provider.ContentBlock{{
				Type: provider.ContentToolResult, ToolResult: &provider.ToolResult{CallID: "c1", Content: "ok"},
			}}},
			// Tool-only step: no reasoning captured (common when stream only
			// emitted encrypted/empty reasoning that we drop).
			{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{
				{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{ID: "c2", Name: "file_read", Arguments: `{}`}},
			}},
		}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		var sawPlaceholder bool
		for i, item := range body.Input {
			if item["type"] != "function_call" || item["call_id"] != "c2" {
				continue
			}
			if i == 0 || body.Input[i-1]["type"] != "reasoning" {
				t.Fatalf("orphan function_call at %d: %#v", i, body.Input)
			}
			content, _ := body.Input[i-1]["content"].([]any)
			part, _ := content[0].(map[string]any)
			if part["text"] != responsesReasoningPlaceholder {
				t.Fatalf("expected placeholder before c2: %#v", body.Input[i-1])
			}
			sawPlaceholder = true
		}
		if !sawPlaceholder {
			t.Fatalf("c2 not found in %#v", body.Input)
		}
	})

	t.Run("responses rejects orphan tool output before transport", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "q"),
			{Role: provider.RoleTool, Blocks: []provider.ContentBlock{{
				Type: provider.ContentToolResult,
				ToolResult: &provider.ToolResult{
					CallID: "orphan", Content: "must not be sent",
				},
			}}},
		}
		if _, _, err := encodeRequest(request); err == nil ||
			!strings.Contains(err.Error(), "no preceding function_call") {
			t.Fatalf("encodeRequest() error = %v", err)
		}
	})

	t.Run("responses encodes paired history reconstructed after restart", func(t *testing.T) {
		events := []protocol.Event{
			{
				Kind: protocol.EventTurnStarted, ThreadID: "thread-1", TurnID: "turn-1", Sequence: 1,
				Data: &protocol.TurnStartedData{Provider: "p", Model: "m", Prompt: "inspect"},
			},
			{
				Kind: protocol.EventToolStart, ThreadID: "thread-1", TurnID: "turn-1", Sequence: 2,
				Data: &protocol.ToolStartData{
					Tool: "exec_command", CallID: "call-1",
					Arguments: []byte(`{"command":"wc -l"}`),
				},
			},
			{
				Kind: protocol.EventToolResult, ThreadID: "thread-1", TurnID: "turn-1", Sequence: 3,
				Data: &protocol.ToolResultData{
					Tool: "exec_command", CallID: "call-1", Output: "42",
				},
			},
			{
				Kind: protocol.EventTurnCompleted, ThreadID: "thread-1", TurnID: "turn-1", Sequence: 4,
				Data: &protocol.TurnCompletedData{Text: "done"},
			},
		}
		reconstructed, err := runtimeapp.ReconstructThread(events, "thread-1")
		if err != nil {
			t.Fatal(err)
		}
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = append(
			reconstructed.History,
			provider.TextMessage(provider.RoleUser, "continue"),
		)
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		callIndex, outputIndex := -1, -1
		for index, item := range body.Input {
			if item["type"] == "function_call" && item["call_id"] == "call-1" {
				callIndex = index
			}
			if item["type"] == "function_call_output" && item["call_id"] == "call-1" {
				outputIndex = index
			}
		}
		if callIndex < 0 || outputIndex <= callIndex {
			t.Fatalf("reconstructed tool pair is invalid: %#v", body.Input)
		}
	})

	t.Run("responses extracts reasoning_text from replay state", func(t *testing.T) {
		raw := json.RawMessage(`{"type":"reasoning","id":"rs_2","content":[{"type":"reasoning_text","text":"from item"}],"summary":[]}`)
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "q"),
			provider.ProducedAssistant(
				request.Route,
				[]provider.ContentBlock{{
					Type: provider.ContentReasoning, ID: "rs_2", Text: "from item",
				}},
				1,
				responsesReplayState(raw),
			),
		}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		content, _ := body.Input[1]["content"].([]any)
		part, _ := content[0].(map[string]any)
		if body.Input[1]["type"] != "reasoning" || part["text"] != "from item" {
			t.Fatalf("input=%#v", body.Input)
		}
	})

	t.Run("responses plaintext reasoning_text for tool loop", func(t *testing.T) {
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "weather?"),
			{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{
				{Type: provider.ContentReasoning, Text: "need a tool"},
				{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{
					ID: "call_1", Name: "get_weather", Arguments: `{"city":"HZ"}`,
				}},
			}},
			{Role: provider.RoleTool, Blocks: []provider.ContentBlock{{
				Type:       provider.ContentToolResult,
				ToolResult: &provider.ToolResult{CallID: "call_1", Content: "cloudy"},
			}}},
		}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Input) < 3 || body.Input[1]["type"] != "reasoning" {
			t.Fatalf("input = %#v", body.Input)
		}
		content, _ := body.Input[1]["content"].([]any)
		if len(content) != 1 {
			t.Fatalf("reasoning content = %#v", body.Input[1]["content"])
		}
		part, _ := content[0].(map[string]any)
		if part["type"] != "reasoning_text" || part["text"] != "need a tool" {
			t.Fatalf("reasoning part = %#v", part)
		}
		if _, hasCipher := body.Input[1]["encrypted_content"]; hasCipher {
			t.Fatalf("plaintext replay must not keep encrypted_content: %#v", body.Input[1])
		}
	})

	t.Run("responses opaque reasoning prefers plaintext content", func(t *testing.T) {
		raw := json.RawMessage(`{"type":"reasoning","id":"rs_2","encrypted_content":"ciphertext","summary":[]}`)
		request := testRequest(t, "https://provider.test", model.ProtocolOpenAIResponses)
		request.Messages = []provider.Message{
			provider.TextMessage(provider.RoleUser, "first"),
			provider.ProducedAssistant(
				request.Route,
				[]provider.ContentBlock{{
					Type: provider.ContentReasoning, ID: "rs_2", Text: "visible chain",
				}},
				1,
				responsesReplayState(raw),
			),
		}
		data, _, err := encodeRequest(request)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Input []map[string]any `json:"input"`
		}
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		item := body.Input[1]
		if item["type"] != "reasoning" || item["id"] != "rs_2" {
			t.Fatalf("item = %#v", item)
		}
		if _, hasCipher := item["encrypted_content"]; hasCipher {
			t.Fatalf("expected encrypted_content dropped when plaintext present: %#v", item)
		}
		content, _ := item["content"].([]any)
		part, _ := content[0].(map[string]any)
		if part["type"] != "reasoning_text" || part["text"] != "visible chain" {
			t.Fatalf("content = %#v", item["content"])
		}
	})
}

func TestClientMakesOneAttemptForRetryableStatus(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := testClient()
	_, err := client.Stream(t.Context(), testRequest(t, server.URL, model.ProtocolOpenAIChat))
	var problem *protocol.Problem
	if !errors.As(err, &problem) || !problem.Retryable {
		t.Fatalf("error = %v, want retryable problem", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("attempts = %d, want 1", got)
	}
}

func TestClientMakesOneAttemptForRetryableHTTPStatusMatrix(t *testing.T) {
	statuses := []int{
		http.StatusRequestTimeout,
		http.StatusTooEarly,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
	}
	for _, status := range statuses {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(
				writer http.ResponseWriter,
				_ *http.Request,
			) {
				attempts.Add(1)
				writer.WriteHeader(status)
			}))
			defer server.Close()
			client := testClient()
			_, err := client.Stream(
				t.Context(),
				testRequest(t, server.URL, model.ProtocolOpenAIChat),
			)
			var problem *protocol.Problem
			if !errors.As(err, &problem) || !problem.Retryable {
				t.Fatalf("error = %v, want retryable problem", err)
			}
			if attempts.Load() != 1 {
				t.Fatalf("attempts = %d, want 1", attempts.Load())
			}
		})
	}
}

func TestClientRetainsRetryAfterMetadataWithoutSleeping(t *testing.T) {
	var attempts atomic.Int32
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		writer.Header().Set("Retry-After", "60")
		writer.Header().Set("RateLimit-Limit", "100")
		writer.Header().Set("RateLimit-Remaining", "0")
		writer.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	client := testClient()
	_, err := client.Stream(t.Context(), testRequest(t, server.URL, model.ProtocolOpenAIChat))

	var problem *protocol.Problem
	if !errors.As(err, &problem) {
		t.Fatalf("error = %v", err)
	}
	if problem.HTTPStatus != http.StatusTooManyRequests ||
		problem.RateLimit == nil ||
		problem.RateLimit.Limit != "100" ||
		problem.RateLimit.Remaining != "0" ||
		problem.RateLimit.RetryAfterMS != 60000 {
		t.Fatalf("problem = %+v", problem)
	}
	if attempts.Load() != 1 || len(keys) != 1 || keys[0] == "" {
		t.Fatalf("idempotency keys = %v", keys)
	}
}

func TestClientClassifiesTransportErrors(t *testing.T) {
	tests := map[string]struct {
		err       error
		retryable bool
	}{
		"connection reset": {fmt.Errorf("write: %w", syscall.ECONNRESET), true},
		"temporary DNS":    {&net.DNSError{Err: "temporary", Name: "provider", IsTemporary: true}, true},
		"missing DNS":      {&net.DNSError{Err: "not found", Name: "provider", IsNotFound: true}, false},
		"TLS certificate": {
			&tls.CertificateVerificationError{
				UnverifiedCertificates: []*x509.Certificate{{}},
				Err:                    x509.UnknownAuthorityError{Cert: &x509.Certificate{}},
			},
			false,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var attempts atomic.Int32
			client := testClient()
			client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				attempts.Add(1)
				return nil, test.err
			})}

			_, err := client.Stream(t.Context(), testRequest(t, "https://provider.test", model.ProtocolOpenAIChat))
			var problem *protocol.Problem
			if !errors.As(err, &problem) ||
				problem.Retryable != test.retryable ||
				attempts.Load() != 1 {
				t.Fatalf("error=%v attempts=%d", err, attempts.Load())
			}
		})
	}
}

func TestClientDoesNotRetryPermanentClientError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writer.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(writer, "invalid model")
	}))
	defer server.Close()

	client := testClient()
	_, err := client.Stream(t.Context(), testRequest(t, server.URL, model.ProtocolOpenAIChat))
	if !protocol.IsCode(err, protocol.CodeInvalidArgument) {
		t.Fatalf("error = %v, code = %q", err, protocol.CodeOf(err))
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want 1", attempts.Load())
	}
}

func TestClientCancellationClosesResponse(t *testing.T) {
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := testClient()
	stream, err := client.Stream(ctx, testRequest(t, server.URL, model.ProtocolOpenAIChat))
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = stream.Recv()
	if err != nil {
		t.Fatalf("first event error = %v", err)
	}
	_, err = stream.Recv()
	if err == nil {
		t.Fatal("Recv() error = nil after cancellation")
	}
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("server request context was not canceled")
	}
	_ = stream.Close()
}

func TestClientHonorsContextDeadlineBeforeHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		time.Sleep(100 * time.Millisecond)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	client := testClient()

	_, err := client.Stream(ctx, testRequest(t, server.URL, model.ProtocolOpenAIChat))

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stream() error = %v, want deadline exceeded", err)
	}
}

func TestClientDoesNotReplayAfterStreamStarts(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		attempts.Add(1)
		writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(writer, "data: {\n\n")
	}))
	defer server.Close()

	client := testClient()
	stream, err := client.Stream(t.Context(), testRequest(t, server.URL, model.ProtocolOpenAIChat))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Drain(stream); err == nil {
		t.Fatal("Drain() error = nil, want malformed stream error")
	}
	if attempts.Load() != 1 {
		t.Fatalf("attempts = %d, want no replay after stream start", attempts.Load())
	}
}

func TestNormalizeStreamErrorClassifiesConnectionReset(t *testing.T) {
	err := normalizeStreamError(fmt.Errorf("read stream: %w", syscall.ECONNRESET))
	if !protocol.IsCode(err, protocol.CodeUnavailable) ||
		!protocol.IsRetryable(err) ||
		!errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("normalizeStreamError() = %#v", err)
	}
}

func TestClientStreamIdleTimeoutAndNonIdempotentRetry(t *testing.T) {
	t.Run("idle timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
			<-request.Context().Done()
		}))
		defer server.Close()
		client := testClient()
		client.IdleTimeout = 10 * time.Millisecond
		stream, err := client.Stream(t.Context(), testRequest(t, server.URL, model.ProtocolOpenAIChat))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.Recv(); !protocol.IsCode(err, protocol.CodeUnavailable) {
			t.Fatalf("idle Recv() error = %v", err)
		}
	})

	t.Run("non idempotent", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			writer.WriteHeader(http.StatusBadGateway)
		}))
		defer server.Close()
		client := testClient()
		request := testRequest(t, server.URL, model.ProtocolOpenAIChat)
		request.Idempotent = false
		if _, err := client.Stream(t.Context(), request); err == nil {
			t.Fatal("Stream() error = nil")
		}
		if attempts.Load() != 1 {
			t.Fatalf("attempts = %d", attempts.Load())
		}
	})
}

func testClient() *Client {
	client := New()
	client.Credentials = staticCredentials("")
	return client
}

func responsesReplayState(items ...json.RawMessage) *provider.ReplayState {
	data, _ := json.Marshal(struct {
		Items []json.RawMessage `json:"items"`
	}{Items: items})
	return &provider.ReplayState{
		Version: provider.ReplayVersion,
		Data:    data,
	}
}

func testRequest(t *testing.T, endpoint string, wireProtocol model.WireProtocol) provider.ModelRequest {
	t.Helper()
	return testRequestWithPromptCache(t, endpoint, wireProtocol, false)
}

func testRequestWithPromptCache(
	t *testing.T, endpoint string, wireProtocol model.WireProtocol, promptCache bool,
) provider.ModelRequest {
	t.Helper()
	adapter := model.AdapterOpenAICompatible
	if wireProtocol == model.ProtocolAnthropic {
		adapter = model.AdapterAnthropic
	}
	catalog, err := model.NewCatalog(model.Provider{
		ID:         "fixture",
		Adapter:    adapter,
		Endpoint:   endpoint,
		Protocol:   wireProtocol,
		Credential: model.CredentialRef{Kind: "env", Name: "FIXTURE_API_KEY"},
		Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{
			"fixture-model": {
				ID:          "fixture-model",
				CanonicalID: "fixture-model",
				WireID:      "wire-model",
				Limits:      model.Limits{ContextTokens: 8192, MaxOutputTokens: 4096},
				Capabilities: model.Capabilities{
					Streaming: true, Reasoning: true, ToolCalls: true, NativeSearch: true,
					PromptCache: promptCache,
				},
				Pricing:    model.Pricing{Currency: "USD", Provenance: model.ProvenanceFixture},
				Provenance: model.ProvenanceFixture,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "fixture", ModelID: "fixture-model", Provenance: model.ProvenanceFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider.ModelRequest{
		Route:           route,
		Messages:        []provider.Message{provider.TextMessage(provider.RoleUser, "hello")},
		MaxOutputTokens: 128,
		Idempotent:      true,
	}
}

type staticCredentials string

func (s staticCredentials) Resolve(context.Context, model.CredentialRef) (string, error) {
	return string(s), nil
}

var _ CredentialResolver = staticCredentials("")

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
