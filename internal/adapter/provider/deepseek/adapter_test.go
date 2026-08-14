package deepseek

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestChatPrepareScopesReasoningToToolCallTurns(t *testing.T) {
	request := testRequest(t, model.ProtocolOpenAIChat)
	request.ReasoningEffort = "high"
	request.Messages = []provider.Message{
		provider.TextMessage(provider.RoleUser, "inspect"),
		{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{
			{Type: provider.ContentReasoning, Text: "drop me"},
			{Type: provider.ContentText, Text: "visible"},
		}},
		{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{
			{Type: provider.ContentReasoning, Text: "keep me"},
			{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{
				ID: "call_1", Name: "read", Arguments: `{"path":"a.go"}`,
			}},
		}},
		{Role: provider.RoleTool, Blocks: []provider.ContentBlock{{
			Type:       provider.ContentToolResult,
			ToolResult: &provider.ToolResult{CallID: "call_1"},
		}}},
	}
	call, err := NewAdapter().Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(call.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body.Messages[1]["reasoning_content"]; exists {
		t.Fatalf("tool-free reasoning leaked: %#v", body.Messages[1])
	}
	if body.Messages[2]["reasoning_content"] != "keep me" {
		t.Fatalf("tool reasoning missing: %#v", body.Messages[2])
	}
	if body.Messages[3]["content"] != emptyToolOutput {
		t.Fatalf("empty tool output = %#v", body.Messages[3]["content"])
	}
}

func TestChatPrepareRejectsImagesAndMapsOff(t *testing.T) {
	request := testRequest(t, model.ProtocolOpenAIChat)
	request.ReasoningEffort = "off"
	call, err := NewAdapter().Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(call.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["reasoning_effort"]; exists {
		t.Fatalf("reasoning_effort=off leaked: %s", call.Body)
	}
	if thinking, _ := body["thinking"].(map[string]any); thinking["type"] != "disabled" {
		t.Fatalf("thinking = %#v", body["thinking"])
	}
	request.Messages = []provider.Message{{
		Role: provider.RoleUser,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentImage,
			Attachment: &provider.Attachment{
				MediaType: "image/png", Data: []byte("png"),
			},
		}},
	}}
	if _, err := NewAdapter().Prepare(request); err == nil {
		t.Fatal("image input unexpectedly accepted")
	}
}

func TestReasoningOffIsValidForNonReasoningChatModel(t *testing.T) {
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "deepseek", ModelID: "deepseek-chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	request := provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleUser, "hello"),
		},
		MaxOutputTokens: 64, ReasoningEffort: "off",
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestResponsesPrepareScopesDeepSeekReplayRules(t *testing.T) {
	request := testRequest(t, model.ProtocolOpenAIResponses)
	request.ReasoningEffort = "max"
	request.Messages = []provider.Message{
		provider.TextMessage(provider.RoleUser, "inspect"),
		{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolCall,
			ToolCall: &provider.ToolCall{
				ID: "call_1", Name: "read", Arguments: "{}",
			},
		}}},
	}
	call, err := NewAdapter().Prepare(request)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(call.Body, &body); err != nil {
		t.Fatal(err)
	}
	if _, exists := body["previous_response_id"]; exists {
		t.Fatalf("previous_response_id leaked: %s", call.Body)
	}
	if _, exists := body["include"]; exists {
		t.Fatalf("encrypted reasoning include leaked: %s", call.Body)
	}
	input, _ := body["input"].([]any)
	if len(input) != 3 {
		t.Fatalf("input = %#v", input)
	}
	reasoning, _ := input[1].(map[string]any)
	if reasoning["type"] != "reasoning" ||
		!strings.Contains(string(call.Body), reasoningPlaceholder) {
		t.Fatalf("DeepSeek placeholder missing: %s", call.Body)
	}
}

func TestChatStreamNormalizesCacheAndRequiresDone(t *testing.T) {
	stream, err := newStream(io.NopCloser(strings.NewReader(sse(
		`{"choices":[{"delta":{"reasoning_content":""},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"reasoning_content":"think"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4,`+
			`"prompt_cache_hit_tokens":10,"prompt_cache_miss_tokens":2,`+
			`"completion_tokens_details":{"reasoning_tokens":2}}}`,
		`[DONE]`,
	))), model.ProtocolOpenAIChat)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	usage := events[len(events)-2].Usage
	if usage == nil || usage.CachedTokens != 10 ||
		usage.InputTokens != 12 || usage.ReasoningTokens != 2 {
		t.Fatalf("usage = %#v", usage)
	}

	stream, _ = newStream(io.NopCloser(strings.NewReader(sse(
		`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
	))), model.ProtocolOpenAIChat)
	_, err = provider.Drain(stream)
	assertFailure(t, err, provider.FailureStreamClosed)
}

func TestChatStreamRejectsEmptyAndInvalidCacheUsage(t *testing.T) {
	stream, _ := newStream(io.NopCloser(strings.NewReader(sse(
		`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`[DONE]`,
	))), model.ProtocolOpenAIChat)
	_, err := provider.Drain(stream)
	assertFailure(t, err, provider.FailureEmptyResponse)

	stream, _ = newStream(io.NopCloser(strings.NewReader(sse(
		`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":1,`+
			`"prompt_cache_hit_tokens":6}}`,
		`[DONE]`,
	))), model.ProtocolOpenAIChat)
	_, err = provider.Drain(stream)
	assertFailure(t, err, provider.FailureMalformedResponse)
}

func TestResponsesStreamUsesDedicatedValidation(t *testing.T) {
	stream, err := newStream(
		io.NopCloser(strings.NewReader(sse(
			`{"type":"response.reasoning_text.delta","output_index":0,"delta":"think"}`,
			`{"type":"response.output_text.delta","output_index":1,"delta":"answer"}`,
			`{"type":"response.completed","response":{"usage":{"input_tokens":12,`+
				`"output_tokens":4,"input_tokens_details":{"cached_tokens":10},`+
				`"output_tokens_details":{"reasoning_tokens":2}}}}`,
		))),
		model.ProtocolOpenAIResponses,
	)
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-2].Usage == nil ||
		events[len(events)-2].Usage.CachedTokens != 10 {
		t.Fatalf("events = %#v", events)
	}

	stream, _ = newStream(
		io.NopCloser(strings.NewReader(sse(
			`{"type":"response.completed","response":{}}`,
		))),
		model.ProtocolOpenAIResponses,
	)
	_, err = provider.Drain(stream)
	assertFailure(t, err, provider.FailureEmptyResponse)
}

func TestFailureClassificationRetainsProviderFacts(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		code   provider.FailureCode
	}{
		{"auth", 401, `{"error":{"message":"invalid key"}}`, provider.FailureAuth},
		{"quota", 429, `{"error":{"code":"insufficient_quota","message":"credits exhausted"}}`, provider.FailureQuota},
		{"rate", 429, `{"error":{"message":"rate limit"}}`, provider.FailureRateLimit},
		{"context", 400, `{"error":{"code":"context_length_exceeded","message":"too long"}}`, provider.FailureContextWindowExceeded},
		{"invalid", 400, `{"error":{"code":"invalid_request","message":"bad"}}`, provider.FailureInvalidRequest},
		{"server", 503, `{"error":{"message":"unavailable"}}`, provider.FailureServer},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewAdapter().ClassifyHTTP(providerwire.HTTPFailure{
				Status: test.status, Body: test.body,
				Header: http.Header{
					"Retry-After":           []string{"2"},
					"X-Deepseek-Request-Id": []string{"ds_req_1"},
				},
			})
			var fact *provider.Failure
			if !errors.As(err, &fact) || fact.Code != test.code ||
				fact.RequestID != "ds_req_1" {
				t.Fatalf("failure = %#v, error = %v", fact, err)
			}
			var problem *protocol.Problem
			if !errors.As(err, &problem) || problem.HTTPStatus != test.status {
				t.Fatalf("problem = %#v", problem)
			}
		})
	}
}

func testRequest(
	t *testing.T,
	wireProtocol model.WireProtocol,
) provider.ModelRequest {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "deepseek-test", Adapter: model.AdapterDeepSeek,
		Endpoint: "https://api.deepseek.com", Protocol: wireProtocol,
		Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{"model": {
			ID: "model", CanonicalID: "model", WireID: "model",
			Limits: model.Limits{ContextTokens: 8192, MaxOutputTokens: 4096},
			Capabilities: model.Capabilities{
				Streaming: true, Reasoning: true, ToolCalls: true,
			},
			Pricing:    model.Pricing{Provenance: model.ProvenanceFixture},
			Provenance: model.ProvenanceFixture,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "deepseek-test", ModelID: "model",
	})
	if err != nil {
		t.Fatal(err)
	}
	return provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleUser, "hello"),
		},
		MaxOutputTokens: 128,
	}
}
func sse(payloads ...string) string {
	var result strings.Builder
	for _, payload := range payloads {
		result.WriteString("data: ")
		result.WriteString(payload)
		result.WriteString("\n\n")
	}
	return result.String()
}
func assertFailure(
	t *testing.T,
	err error,
	code provider.FailureCode,
) {
	t.Helper()
	var failure *provider.Failure
	if !errors.As(err, &failure) || failure.Code != code {
		t.Fatalf("error = %v, failure = %#v, want %s", err, failure, code)
	}
}
