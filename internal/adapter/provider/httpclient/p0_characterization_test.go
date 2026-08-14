package httpclient

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const updateP0GoldensEnv = "CODEHELPER_UPDATE_PROVIDER_P0_GOLDENS"

type p0WireGolden struct {
	SchemaVersion int          `json:"schema_version"`
	Cases         []p0WireCase `json:"cases"`
}

type p0WireCase struct {
	Name                   string             `json:"name"`
	Provider               string             `json:"provider"`
	Model                  string             `json:"model"`
	Protocol               model.WireProtocol `json:"protocol"`
	Path                   string             `json:"path"`
	SerializedBody         string             `json:"serialized_body"`
	RequestBytes           uint64             `json:"request_bytes"`
	LogicalRequestDigest   string             `json:"logical_request_digest"`
	TransportPayloadDigest string             `json:"transport_payload_digest"`
	Incremental            bool               `json:"incremental"`
	KnownGaps              []string           `json:"known_gaps,omitempty"`
}

func TestP0WireRequestGolden(t *testing.T) {
	tests := []struct {
		name       string
		providerID string
		modelID    string
		knownGaps  []string
	}{
		{
			name: "openai_chat", providerID: "openai", modelID: "gpt-4.1",
		},
		{
			name: "openai_responses", providerID: "openai-responses", modelID: "gpt-4.1",
			knownGaps: []string{
				"shared_responses_encoder_inserts_a_deepseek_reasoning_placeholder",
			},
		},
		{
			name: "anthropic", providerID: "anthropic", modelID: "claude-sonnet",
		},
		{
			name: "deepseek_chat", providerID: "deepseek", modelID: "deepseek-reasoner",
			knownGaps: []string{
				"tool_call_free_assistant_reasoning_is_replayed",
				"deepseek_chat_uses_the_shared_openai_chat_encoder",
			},
		},
		{
			name: "deepseek_responses", providerID: "deepseek-v4-flash", modelID: "deepseek-v4-flash",
			knownGaps: []string{
				"deepseek_responses_uses_the_shared_openai_responses_encoder",
				"deepseek_reasoning_placeholder_is_not_adapter_scoped",
			},
		},
	}
	golden := p0WireGolden{SchemaVersion: 1}
	for _, test := range tests {
		route := p0BundledRoute(t, test.providerID, test.modelID)
		request := p0CharacterizationRequest(route)
		if err := request.Validate(); err != nil {
			t.Fatalf("%s request: %v", test.name, err)
		}
		body, path, err := encodeRequest(request)
		if err != nil {
			t.Fatalf("%s encode: %v", test.name, err)
		}
		metadata := transportMetadata(body, body, false)
		golden.Cases = append(golden.Cases, p0WireCase{
			Name: test.name, Provider: route.ProviderID(), Model: route.Model().ID,
			Protocol: route.Protocol(), Path: path, SerializedBody: string(body),
			RequestBytes:           metadata.RequestBytes,
			LogicalRequestDigest:   metadata.LogicalRequestDigest,
			TransportPayloadDigest: metadata.TransportPayloadDigest,
			Incremental:            metadata.Incremental,
			KnownGaps:              test.knownGaps,
		})
	}
	assertP0Golden(t, "wire_requests.golden.json", golden)
}

func p0BundledRoute(t *testing.T, providerID, modelID string) model.ReadyRoute {
	t.Helper()
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: providerID, ModelID: modelID, Provenance: model.ProvenanceBundled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func p0CharacterizationRequest(route model.ReadyRoute) provider.ModelRequest {
	temperature := 0.2
	store := false
	parallel := true
	maxOutput := uint64(256)
	if route.Protocol() == model.ProtocolAnthropic {
		maxOutput = 2048
	}
	effort := ""
	if route.Model().Capabilities.Reasoning {
		effort = "high"
		if route.ProviderID() == "deepseek-v4-flash" {
			effort = "max"
		}
	}
	return provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleSystem, "You are a precise coding agent."),
			provider.TextMessage(provider.RoleUser, "Inspect the repository."),
			{
				Role: provider.RoleAssistant,
				Blocks: []provider.ContentBlock{
					{Type: provider.ContentReasoning, Text: "plain-turn reasoning"},
					{Type: provider.ContentText, Text: "The repository needs a focused inspection."},
				},
			},
			provider.TextMessage(provider.RoleUser, "Search for the provider implementation."),
			{
				Role: provider.RoleAssistant,
				Blocks: []provider.ContentBlock{
					{Type: provider.ContentReasoning, Text: "tool-turn reasoning"},
					{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{
						ID: "call_search", Name: "search_text",
						Arguments: `{"query":"Provider","path":"internal/adapter/provider"}`,
					}},
				},
			},
			{
				Role: provider.RoleTool,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolResult,
					ToolResult: &provider.ToolResult{
						CallID: "call_search", Content: "internal/adapter/provider/types.go:481",
					},
				}},
			},
			provider.TextMessage(provider.RoleUser, "Search the transport implementation too."),
			{
				Role: provider.RoleAssistant,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolCall,
					ToolCall: &provider.ToolCall{
						ID: "call_transport", Name: "search_text",
						Arguments: `{"query":"encodeRequest","path":"internal/adapter/provider/httpclient"}`,
					},
				}},
			},
			{
				Role: provider.RoleTool,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolResult,
					ToolResult: &provider.ToolResult{
						CallID: "call_transport", Content: "internal/adapter/provider/httpclient/client.go:270",
					},
				}},
			},
			provider.TextMessage(provider.RoleUser, "Summarize the finding."),
		},
		MaxOutputTokens: maxOutput,
		Temperature:     &temperature,
		ReasoningEffort: effort,
		NativeSearch:    route.Model().Capabilities.NativeSearch,
		Tools: []provider.ToolDefinition{{
			Name: "search_text", Description: "Search repository text",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"path":  map[string]any{"type": "string"},
				},
				"required":             []string{"query"},
				"additionalProperties": false,
			},
		}},
		Idempotent:     true,
		PromptCacheKey: provider.StickyPromptCacheKey("p0-provider-thread", route),
		Store:          &store,
		ParallelTools:  &parallel,
	}
}

type p0StreamGolden struct {
	SchemaVersion int            `json:"schema_version"`
	Cases         []p0StreamCase `json:"cases"`
}

type p0StreamCase struct {
	Name      string                 `json:"name"`
	Protocol  model.WireProtocol     `json:"protocol"`
	Events    []provider.StreamEvent `json:"events"`
	KnownGaps []string               `json:"known_gaps,omitempty"`
}

func TestP0StreamOrderGolden(t *testing.T) {
	tests := []struct {
		name      string
		protocol  model.WireProtocol
		stream    string
		knownGaps []string
	}{
		{
			name: "openai_chat", protocol: model.ProtocolOpenAIChat,
			stream: p0SSE(
				`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
				`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4,`+
					`"prompt_tokens_details":{"cached_tokens":3}}}`,
				`[DONE]`,
			),
		},
		{
			name: "openai_responses", protocol: model.ProtocolOpenAIResponses,
			stream: p0SSE(
				`{"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"think"}`,
				`{"type":"response.output_text.delta","output_index":1,"delta":"answer"}`,
				`{"type":"response.completed","response":{"usage":{"input_tokens":12,`+
					`"output_tokens":4,"input_tokens_details":{"cached_tokens":3},`+
					`"output_tokens_details":{"reasoning_tokens":2}}}}`,
			),
		},
		{
			name: "anthropic", protocol: model.ProtocolAnthropic,
			stream: p0SSE(
				`{"type":"message_start","message":{"usage":{"input_tokens":2,`+
					`"cache_read_input_tokens":8,"cache_creation_input_tokens":2}}}`,
				`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}`,
				`{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"answer"}}`,
				`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":4}}`,
			),
		},
		{
			name: "deepseek_chat", protocol: model.ProtocolOpenAIChat,
			stream: p0SSE(
				`{"choices":[{"delta":{"reasoning_content":""},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"reasoning_content":"think"},"finish_reason":null}]}`,
				`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
				`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":4,`+
					`"prompt_cache_hit_tokens":10,"prompt_cache_miss_tokens":2,`+
					`"completion_tokens_details":{"reasoning_tokens":2}}}`,
				`[DONE]`,
			),
			knownGaps: []string{
				"native_prompt_cache_hit_tokens_is_not_counted",
			},
		},
		{
			name:     "deepseek_chat_eof_after_finish_without_done",
			protocol: model.ProtocolOpenAIChat,
			stream: p0SSE(
				`{"choices":[{"delta":{"content":"answer"},"finish_reason":"stop"}]}`,
			),
			knownGaps: []string{
				"chat_stream_accepts_eof_after_finish_reason_without_done",
			},
		},
		{
			name:     "deepseek_chat_empty_success",
			protocol: model.ProtocolOpenAIChat,
			stream: p0SSE(
				`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
				`[DONE]`,
			),
			knownGaps: []string{
				"empty_success_is_not_classified_as_empty_response",
			},
		},
		{
			name: "deepseek_responses", protocol: model.ProtocolOpenAIResponses,
			stream: p0SSE(
				`{"type":"response.reasoning_text.delta","output_index":0,"item_id":"rs_1","delta":"think"}`,
				`{"type":"response.output_text.delta","output_index":1,"delta":"answer"}`,
				`{"type":"response.completed","response":{"id":"resp_1","usage":{"input_tokens":12,`+
					`"output_tokens":4,"input_tokens_details":{"cached_tokens":10},`+
					`"output_tokens_details":{"reasoning_tokens":2}}}}`,
			),
			knownGaps: []string{
				"deepseek_responses_is_decoded_by_the_shared_openai_responses_decoder",
			},
		},
	}
	golden := p0StreamGolden{SchemaVersion: 1}
	for _, test := range tests {
		stream, err := decodeStream(
			io.NopCloser(strings.NewReader(test.stream)),
			test.protocol,
		)
		if err != nil {
			t.Fatalf("%s open stream: %v", test.name, err)
		}
		events, err := provider.Drain(stream)
		if err != nil {
			t.Fatalf("%s drain stream: %v", test.name, err)
		}
		if len(events) < 2 ||
			events[0].Type != provider.EventMessageStart ||
			events[len(events)-1].Type != provider.EventMessageStop {
			t.Fatalf("%s invalid event enclosure: %+v", test.name, events)
		}
		golden.Cases = append(golden.Cases, p0StreamCase{
			Name: test.name, Protocol: test.protocol, Events: events,
			KnownGaps: test.knownGaps,
		})
	}
	assertP0Golden(t, "stream_order.golden.json", golden)
}

func p0SSE(payloads ...string) string {
	var builder strings.Builder
	for _, payload := range payloads {
		builder.WriteString("data: ")
		builder.WriteString(payload)
		builder.WriteString("\n\n")
	}
	return builder.String()
}

type p0FailureGolden struct {
	SchemaVersion int             `json:"schema_version"`
	Cases         []p0FailureCase `json:"cases"`
}

type p0FailureCase struct {
	Name              string             `json:"name"`
	HTTPStatus        int                `json:"http_status"`
	RuntimeCode       protocol.ErrorCode `json:"runtime_code"`
	Retryable         bool               `json:"retryable"`
	RetryAfterMS      uint64             `json:"retry_after_ms,omitempty"`
	RequestIDRetained bool               `json:"request_id_retained"`
	TargetFailureCode string             `json:"target_failure_code"`
}

func TestP0FailureClassificationGolden(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		headers    http.Header
		targetCode string
	}{
		{
			name: "deepseek_auth", status: http.StatusUnauthorized,
			body:       `{"error":{"message":"invalid api key","type":"authentication_error"}}`,
			targetCode: "auth",
		},
		{
			name: "deepseek_quota", status: http.StatusTooManyRequests,
			body:       `{"error":{"message":"account credits exhausted","code":"insufficient_quota"}}`,
			targetCode: "quota",
		},
		{
			name: "deepseek_rate_limit", status: http.StatusTooManyRequests,
			body:       `{"error":{"message":"request rate limit exceeded"}}`,
			headers:    http.Header{"Retry-After": []string{"2"}},
			targetCode: "rate_limit",
		},
		{
			name: "deepseek_context_window", status: http.StatusBadRequest,
			body: `{"error":{"message":"maximum context length exceeded",` +
				`"code":"context_length_exceeded"}}`,
			targetCode: "context_window_exceeded",
		},
		{
			name: "deepseek_invalid_request", status: http.StatusBadRequest,
			body:       `{"error":{"message":"temperature is invalid","code":"invalid_request"}}`,
			targetCode: "invalid_request",
		},
		{
			name: "deepseek_server", status: http.StatusServiceUnavailable,
			body:       `{"error":{"message":"temporarily unavailable"}}`,
			headers:    http.Header{"X-Deepseek-Request-Id": []string{"ds_req_p0"}},
			targetCode: "server",
		},
	}
	golden := p0FailureGolden{SchemaVersion: 1}
	for _, test := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			for key, values := range test.headers {
				for _, value := range values {
					writer.Header().Add(key, value)
				}
			}
			writer.WriteHeader(test.status)
			_, _ = io.WriteString(writer, test.body)
		}))
		client := testClient()
		_, err := client.Stream(
			t.Context(),
			testRequest(t, server.URL, model.ProtocolOpenAIChat),
		)
		server.Close()
		var problem *protocol.Problem
		if !errors.As(err, &problem) {
			t.Fatalf("%s error = %v, want protocol problem", test.name, err)
		}
		retryAfter := uint64(0)
		if problem.RateLimit != nil {
			retryAfter = problem.RateLimit.RetryAfterMS
		}
		golden.Cases = append(golden.Cases, p0FailureCase{
			Name: test.name, HTTPStatus: problem.HTTPStatus,
			RuntimeCode: problem.Code, Retryable: problem.Retryable,
			RetryAfterMS: retryAfter,
			// protocol.Problem has no Provider request-id field at P0.
			RequestIDRetained: false,
			TargetFailureCode: test.targetCode,
		})
	}
	assertP0Golden(t, "failure_classification.golden.json", golden)
}

type p0CountGolden struct {
	SchemaVersion    int    `json:"schema_version"`
	Scenario         string `json:"scenario"`
	ModelSamples     uint64 `json:"model_samples"`
	ProviderRequests uint64 `json:"provider_requests"`
	CurrentBehavior  string `json:"current_behavior"`
}

func TestP0ProviderRequestAndModelSampleCountGolden(t *testing.T) {
	// P0 is immutable historical evidence. P2 has a live single-attempt test.
	assertP0Golden(t, "request_counts.golden.json", p0CountGolden{
		SchemaVersion:    1,
		Scenario:         "one_model_sample_with_two_hidden_http_retries",
		ModelSamples:     1,
		ProviderRequests: 3,
		CurrentBehavior:  "httpclient_retries_are_inside_one_engine_model_sample",
	})
}

type p0Evidence struct {
	Stage      string `json:"stage"`
	Status     string `json:"status"`
	Acceptance struct {
		Passed           bool `json:"passed"`
		WireRequestCases int  `json:"wire_request_cases"`
		StreamCases      int  `json:"stream_cases"`
		FailureCases     int  `json:"failure_cases"`
	} `json:"acceptance"`
	Artifacts map[string]struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"artifacts"`
}

func TestP0EvidenceMatchesGoldens(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", "..", ".."))
	data, err := os.ReadFile(filepath.Join(
		root,
		"docs",
		"provider-architecture-p0-baseline.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var evidence p0Evidence
	if err := json.Unmarshal(data, &evidence); err != nil {
		t.Fatal(err)
	}
	if evidence.Stage != "P0" || evidence.Status != "baseline_frozen" ||
		!evidence.Acceptance.Passed ||
		evidence.Acceptance.WireRequestCases != 5 ||
		evidence.Acceptance.StreamCases != 7 ||
		evidence.Acceptance.FailureCases != 6 {
		t.Fatalf("invalid P0 evidence summary: %+v", evidence)
	}
	if len(evidence.Artifacts) != 4 {
		t.Fatalf("P0 evidence artifacts = %d, want 4", len(evidence.Artifacts))
	}
	for name, artifact := range evidence.Artifacts {
		content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(artifact.Path)))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := digest(content); got != artifact.SHA256 {
			t.Fatalf("%s digest = %s, want %s", name, got, artifact.SHA256)
		}
	}
}

func assertP0Golden(t *testing.T, name string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	path := filepath.Join("testdata", "p0", name)
	if os.Getenv(updateP0GoldensEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v (set %s=1 to create it)", path, err, updateP0GoldensEnv)
	}
	if !bytes.Equal(data, want) {
		t.Fatalf("%s mismatch\nGOT:\n%s\nWANT:\n%s", path, data, want)
	}
}
