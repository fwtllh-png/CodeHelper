package httpclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/anthropic"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/openai"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

const (
	maxErrorBodyBytes = 16 << 10
	defaultRetryCap   = 30 * time.Second
)

var requestSequence atomic.Uint64

type CredentialResolver interface {
	Resolve(context.Context, model.CredentialRef) (string, error)
}

type EnvCredentials struct{}

func (EnvCredentials) Resolve(_ context.Context, reference model.CredentialRef) (string, error) {
	if reference.Kind == "" {
		return "", nil
	}
	if reference.Kind != "env" {
		return "", fmt.Errorf("credential kind %q is not available", reference.Kind)
	}
	value, exists := os.LookupEnv(reference.Name)
	if !exists || value == "" {
		return "", fmt.Errorf("credential environment variable %s is not set", reference.Name)
	}
	return value, nil
}

type Client struct {
	HTTP        *http.Client
	Credentials CredentialResolver
	// Egress, when non-nil and Enforce, gates every RoundTrip. Nil keeps the
	// historical open client so unit tests that never wired a broker still pass.
	Egress            *egress.Gate
	MaxAttempts       int
	BaseDelay         time.Duration
	MaxRetryDelay     time.Duration
	Now               func() time.Time
	Random            func() float64
	Sleep             func(context.Context, time.Duration) error
	Metrics           *telemetry.Metrics
	IdleTimeout       time.Duration
	MaxConcurrent     int
	RequestsPerSecond float64

	mu          sync.Mutex
	active      int
	nextRequest time.Time
	health      Health
}

type Health struct {
	Healthy             bool      `json:"healthy"`
	Active              int       `json:"active"`
	ConsecutiveFailures uint64    `json:"consecutive_failures"`
	LastError           string    `json:"last_error,omitempty"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func New() *Client {
	return &Client{
		HTTP:          &http.Client{},
		Credentials:   DefaultCredentials(),
		MaxAttempts:   3,
		BaseDelay:     200 * time.Millisecond,
		MaxRetryDelay: defaultRetryCap,
		Now:           time.Now,
		Random:        func() float64 { return 0.5 },
		Sleep:         wait,
		IdleTimeout:   60 * time.Second,
		MaxConcurrent: 8,
		health:        Health{Healthy: true, UpdatedAt: time.Now()},
	}
}

func (c *Client) httpClient() *http.Client {
	base := c.HTTP
	if base == nil {
		base = http.DefaultClient
	}
	if c.Egress == nil {
		return base
	}
	return egress.WrapClient(base, c.Egress)
}

func (c *Client) Stream(ctx context.Context, request provider.ModelRequest) (provider.Stream, error) {
	if err := request.Validate(); err != nil {
		return nil, protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err)
	}
	if err := c.acquire(ctx); err != nil {
		return nil, err
	}
	requestContext, requestCancel := context.WithCancel(ctx)
	release := true
	defer func() {
		if release {
			requestCancel()
			c.release()
		}
	}()
	if err := c.rateLimit(requestContext); err != nil {
		return nil, err
	}
	httpClient := c.httpClient()
	resolver := c.Credentials
	if resolver == nil {
		resolver = DefaultCredentials()
	}
	credential, err := resolver.Resolve(requestContext, request.Route.Credential())
	if err != nil {
		return nil, protocol.NewProblem(protocol.CodeUnavailable, "resolve provider credential", false, err)
	}
	body, path, err := encodeRequest(request)
	if err != nil {
		return nil, protocol.NewProblem(protocol.CodeInvalidArgument, err.Error(), false, err)
	}
	attempts := c.MaxAttempts
	if attempts <= 0 {
		attempts = 3
	}
	if !request.Idempotent {
		attempts = 1
	}
	delay := c.BaseDelay
	if delay <= 0 {
		delay = 200 * time.Millisecond
	}
	retryCap := c.MaxRetryDelay
	if retryCap <= 0 {
		retryCap = defaultRetryCap
	}
	now := c.Now
	if now == nil {
		now = time.Now
	}
	random := c.Random
	if random == nil {
		random = func() float64 { return 0.5 }
	}
	sleep := c.Sleep
	if sleep == nil {
		sleep = wait
	}
	idempotencyKey := requestKey(body)
	reasoningRepaired := false

	for attempt := 1; attempt <= attempts; attempt++ {
		httpRequest, createErr := http.NewRequestWithContext(
			requestContext,
			http.MethodPost,
			joinEndpoint(request.Route.Endpoint(), path),
			bytes.NewReader(body),
		)
		if createErr != nil {
			return nil, protocol.NewProblem(protocol.CodeInvalidArgument, "create provider request", false, createErr)
		}
		setHeaders(httpRequest, request.Route, credential)
		httpRequest.Header.Set("Idempotency-Key", idempotencyKey)
		c.Metrics.ProviderRequest()
		response, doErr := httpClient.Do(httpRequest)
		if doErr != nil {
			if ctx.Err() != nil {
				requestCancel()
				return nil, ctx.Err()
			}
			retryable := retryableTransportError(doErr)
			if retryable && attempt < attempts {
				retryDelay := cappedJitter(delay*time.Duration(attempt), retryCap, random())
				if err := sleep(ctx, retryDelay); err != nil {
					return nil, err
				}
				continue
			}
			problem := protocol.NewProblem(protocol.CodeUnavailable, "provider request failed", retryable, doErr)
			c.recordFailure(problem)
			return nil, problem
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			stream, decodeErr := decodeStream(response.Body, request.Route.Protocol())
			if decodeErr != nil {
				requestCancel()
				c.recordFailure(decodeErr)
				return nil, decodeErr
			}
			release = false
			return &managedStream{
				stream: stream, cancel: requestCancel, release: c.release,
				idleTimeout: c.IdleTimeout, success: c.recordSuccess, failure: c.recordFailure,
			}, nil
		}
		errorText := boundedBody(response.Body)
		if !reasoningRepaired &&
			request.Route.Protocol() == model.ProtocolOpenAIResponses &&
			looksLikeReasoningReplayError(errorText) {
			if hydrated, changed := hydrateReasoningText(request.Messages); changed {
				repaired := request
				repaired.Messages = hydrated
				if repairedBody, repairedPath, encodeErr := encodeRequest(repaired); encodeErr == nil {
					reasoningRepaired = true
					request = repaired
					body, path = repairedBody, repairedPath
					idempotencyKey = requestKey(body)
					continue
				}
			}
		}
		retryable := retryableStatus(response.StatusCode)
		serverDelay, hasRetryAfter := retryAfter(response.Header.Get("Retry-After"), now())
		rateLimit := rateLimitMetadata(response.Header, serverDelay, hasRetryAfter)
		if retryable && attempt < attempts {
			retryDelay := serverDelay
			if !hasRetryAfter {
				retryDelay = delay * time.Duration(attempt)
			}
			retryDelay = cappedJitter(retryDelay, retryCap, random())
			if err := sleep(ctx, retryDelay); err != nil {
				return nil, err
			}
			continue
		}
		code := protocol.CodeUnavailable
		if response.StatusCode >= 400 && response.StatusCode < 500 && response.StatusCode != http.StatusTooManyRequests {
			code = protocol.CodeInvalidArgument
		}
		message := fmt.Sprintf("provider returned HTTP %d", response.StatusCode)
		if errorText != "" {
			message += ": " + errorText
		}
		if shouldDumpProvider(response.StatusCode, errorText) {
			if dumpPath, dumpErr := dumpProviderFailure(
				request, body, path, response.StatusCode, errorText, reasoningRepaired,
			); dumpErr == nil && dumpPath != "" {
				message += " [diagnostic: " + dumpPath + "]"
			}
		}
		problem := protocol.NewProblem(code, message, retryable, nil)
		problem.HTTPStatus = response.StatusCode
		problem.RateLimit = rateLimit
		c.recordFailure(problem)
		return nil, problem
	}
	requestCancel()
	problem := protocol.NewProblem(protocol.CodeUnavailable, "provider retries exhausted", true, nil)
	c.recordFailure(problem)
	return nil, problem
}

func encodeRequest(request provider.ModelRequest) ([]byte, string, error) {
	switch request.Route.Protocol() {
	case model.ProtocolOpenAIChat:
		body := map[string]any{
			"model":      request.Route.Model().WireID,
			"messages":   openAIMessages(request.Messages),
			"max_tokens": request.MaxOutputTokens,
			"stream":     true,
			"stream_options": map[string]bool{
				"include_usage": true,
			},
		}
		applyOptional(body, request)
		applyOpenAIChatTools(body, request.Tools)
		if request.NativeSearch {
			appendTool(body, map[string]any{"type": "web_search_preview"})
		}
		// Same gate as Responses: only advertise when the catalog says the model
		// supports prompt caching (StickyPromptCacheKey already drops sticky
		// defaults without the capability).
		if request.PromptCacheKey != "" && request.Route.Model().Capabilities.PromptCache {
			body["prompt_cache_key"] = request.PromptCacheKey
		}
		data, err := json.Marshal(body)
		return data, "/chat/completions", err
	case model.ProtocolOpenAIResponses:
		input, err := openAIResponsesInput(request.Messages)
		if err != nil {
			return nil, "", err
		}
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
			"model":               request.Route.Model().WireID,
			"input":               input,
			"max_output_tokens":   request.MaxOutputTokens,
			"stream":              true,
			"store":               store,
			"parallel_tool_calls": parallelTools,
		}
		if len(include) != 0 {
			body["include"] = include
		}
		if request.Temperature != nil {
			body["temperature"] = *request.Temperature
		}
		if request.ReasoningEffort != "" {
			body["reasoning"] = map[string]any{
				"effort": request.ReasoningEffort, "summary": "auto",
			}
		}
		applyOpenAIResponsesTools(body, request.Tools)
		if request.NativeSearch {
			// OpenAI uses web_search_preview; DeepSeek Responses accepts web_search.
			appendTool(body, map[string]any{"type": "web_search"})
		}
		// Only advertise a cache key when the catalog (or CLI) says the model
		// supports it. Custom Responses endpoints often leave prompt_cache
		// false and ignore the field; sending it is fine for them, but we still
		// gate so a sticky key never contradicts Validate.
		if request.PromptCacheKey != "" && request.Route.Model().Capabilities.PromptCache {
			body["prompt_cache_key"] = request.PromptCacheKey
		}
		data, err := json.Marshal(body)
		return data, "/responses", err
	case model.ProtocolAnthropic:
		system, messages, err := anthropicMessages(
			request.Messages, request.Route.Model().Capabilities.PromptCache,
		)
		if err != nil {
			return nil, "", err
		}
		body := map[string]any{
			"model":      request.Route.Model().WireID,
			"messages":   messages,
			"max_tokens": request.MaxOutputTokens,
			"stream":     true,
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
				return nil, "", err
			}
			body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": budget}
		}
		if request.NativeSearch {
			appendTool(body, map[string]any{
				"type": "web_search_20250305", "name": "web_search", "max_uses": 5,
			})
		}
		if len(request.Tools) != 0 {
			for _, definition := range request.Tools {
				appendTool(body, map[string]any{
					"name": definition.Name, "description": definition.Description, "input_schema": definition.InputSchema,
				})
			}
		}
		data, err := json.Marshal(body)
		return data, "/messages", err
	default:
		return nil, "", fmt.Errorf("unsupported provider protocol %q", request.Route.Protocol())
	}
}

func openAIMessages(messages []provider.Message) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		item := map[string]any{"role": message.Role}
		var text, reasoning string
		var calls []map[string]any
		var images []map[string]any
		for _, block := range message.Blocks {
			switch block.Type {
			case provider.ContentText:
				text += block.Text
			case provider.ContentReasoning:
				reasoning += block.Text
			case provider.ContentImage:
				images = append(images, map[string]any{
					"type": "image_url",
					"image_url": map[string]any{
						"url": block.Attachment.DataURL(),
					},
				})
			case provider.ContentToolCall:
				call := block.ToolCall
				calls = append(calls, map[string]any{
					"id": call.ID, "type": "function", "function": map[string]any{
						"name": call.Name, "arguments": call.Arguments,
					},
				})
			case provider.ContentToolResult:
				item["tool_call_id"] = block.ToolResult.CallID
				text += block.ToolResult.Content
			}
		}
		// Text-only messages keep the plain string form. The array form is
		// equivalent to the provider but not byte-identical, and every request
		// body is a prompt cache key: switching all traffic to arrays to
		// accommodate the rare message with an image would invalidate every
		// cached prefix.
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

func openAIResponsesInput(messages []provider.Message) ([]map[string]any, error) {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		// An image has to sit in the same input item as the text that asks about
		// it, so a message carrying one is emitted as a single item with typed
		// parts. Messages without images keep emitting one item per block, which
		// is the shape every existing request already has.
		if grouped, ok := openAIResponsesImageItem(message); ok {
			result = append(result, grouped)
			continue
		}
		for _, block := range message.Blocks {
			switch block.Type {
			case provider.ContentText:
				result = append(result, map[string]any{"role": message.Role, "content": block.Text})
			case provider.ContentReasoning:
				item, err := openAIResponsesReasoningItem(block)
				if err != nil {
					return nil, err
				}
				if item != nil {
					result = append(result, item)
				}
			case provider.ContentToolCall:
				call := block.ToolCall
				// DeepSeek thinking mode rejects any function_call that is not
				// preceded by a reasoning item (or another function_call that
				// already shares one). Empty/encrypted-only reasoning is dropped
				// above, so tool-only assistant steps need a placeholder here.
				result = ensureReasoningBeforeFunctionCall(result)
				result = append(result, map[string]any{
					"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": call.Arguments,
				})
			case provider.ContentToolResult:
				result = append(result, map[string]any{
					"type": "function_call_output", "call_id": block.ToolResult.CallID,
					"output": block.ToolResult.Content,
				})
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
				return fmt.Errorf(
					"OpenAI Responses function_call_output at input %d has no preceding function_call for call_id %q",
					index,
					callID,
				)
			}
		}
	}
	return nil
}

// Placeholder used when a tool call must be replayed but no plaintext reasoning
// was captured for that step. DeepSeek accepts this; an empty reasoning item or
// a bare function_call after function_call_output both return HTTP 400.
const responsesReasoningPlaceholder = "(continued)"

func ensureReasoningBeforeFunctionCall(result []map[string]any) []map[string]any {
	if len(result) > 0 {
		switch stringValue(result[len(result)-1]["type"]) {
		case "reasoning", "function_call":
			return result
		}
	}
	return append(result, map[string]any{
		"type": "reasoning",
		"content": []map[string]any{{
			"type": "reasoning_text", "text": responsesReasoningPlaceholder,
		}},
	})
}

// openAIResponsesReasoningItem rebuilds a Responses `reasoning` input item.
// DeepSeek rejects empty reasoning items with HTTP 400 ("reasoning_text must be
// passed back"); omitting the item entirely is accepted. Always emit plaintext
// content and never send an empty shell. When tool calls remain after a drop,
// ensureReasoningBeforeFunctionCall injects a non-empty placeholder.
func openAIResponsesReasoningItem(block provider.ContentBlock) (map[string]any, error) {
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
	out := map[string]any{
		"type": "reasoning",
		"content": []map[string]any{{
			"type": "reasoning_text", "text": text,
		}},
	}
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

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

// openAIResponsesImageItem groups a message's text and images into one input
// item. It reports false for a message without images, which every message in
// the ordinary path is.
func openAIResponsesImageItem(message provider.Message) (map[string]any, bool) {
	var content []map[string]any
	images := false
	for _, block := range message.Blocks {
		switch block.Type {
		case provider.ContentText:
			content = append(content, map[string]any{"type": "input_text", "text": block.Text})
		case provider.ContentImage:
			images = true
			content = append(content, map[string]any{
				"type": "input_image", "image_url": block.Attachment.DataURL(),
			})
		}
	}
	if !images {
		return nil, false
	}
	return map[string]any{"role": message.Role, "content": content}, true
}

// anthropicMessages splits RoleSystem messages into Anthropic system text
// blocks. Leading system messages (before the first non-system role) are the
// stable prefix; any system message after conversation content is volatile
// turn context. When promptCache is true, only the last stable block gets
// cache_control so the breakpoint stays off turn-local tails.
func anthropicMessages(
	messages []provider.Message, promptCache bool,
) ([]map[string]any, []map[string]any, error) {
	var stable []string
	var volatile []string
	seenNonSystem := false
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
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
			content := make([]map[string]any, 0, len(message.Blocks))
			for _, block := range message.Blocks {
				switch block.Type {
				case provider.ContentText:
					content = append(content, map[string]any{"type": "text", "text": block.Text})
				case provider.ContentReasoning:
					thinking := map[string]any{"type": "thinking", "thinking": block.Text}
					if block.Signature != "" {
						thinking["signature"] = block.Signature
					}
					content = append(content, thinking)
				case provider.ContentToolCall:
					call := block.ToolCall
					var input any
					if err := json.Unmarshal([]byte(call.Arguments), &input); err != nil {
						return nil, nil, fmt.Errorf("decode Anthropic tool arguments for %s: %w", call.ID, err)
					}
					content = append(content, map[string]any{
						"type": "tool_use", "id": call.ID, "name": call.Name, "input": input,
					})
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
							"type":       "base64",
							"media_type": block.Attachment.MediaType,
							"data":       block.Attachment.Base64(),
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

func applyOpenAIChatTools(body map[string]any, definitions []provider.ToolDefinition) {
	for _, definition := range definitions {
		appendTool(body, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": definition.Name, "description": definition.Description, "parameters": definition.InputSchema,
			},
		})
	}
}

// applyOpenAIResponsesTools uses the flat function tool shape required by Responses API
// (name/description/parameters at top level — not nested under "function").
func applyOpenAIResponsesTools(body map[string]any, definitions []provider.ToolDefinition) {
	for _, definition := range definitions {
		appendTool(body, map[string]any{
			"type":        "function",
			"name":        definition.Name,
			"description": definition.Description,
			"parameters":  definition.InputSchema,
		})
	}
}

// Deprecated alias kept for older call sites / tests.
func applyOpenAITools(body map[string]any, definitions []provider.ToolDefinition) {
	applyOpenAIChatTools(body, definitions)
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

func setHeaders(request *http.Request, route model.ReadyRoute, credential string) {
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if credential == "" {
		return
	}
	if route.Protocol() == model.ProtocolAnthropic {
		request.Header.Set("x-api-key", credential)
		request.Header.Set("anthropic-version", "2023-06-01")
		return
	}
	request.Header.Set("Authorization", "Bearer "+credential)
}

func decodeStream(body io.ReadCloser, protocol model.WireProtocol) (provider.Stream, error) {
	if protocol == model.ProtocolAnthropic {
		return anthropic.NewStream(body)
	}
	return openai.NewStream(body, protocol)
}

type managedStream struct {
	stream      provider.Stream
	cancel      context.CancelFunc
	release     func()
	idleTimeout time.Duration
	success     func()
	failure     func(error)
	closeOnce   sync.Once
}

type receiveResult struct {
	event provider.StreamEvent
	err   error
}

func (s *managedStream) Recv() (provider.StreamEvent, error) {
	if s.idleTimeout <= 0 {
		event, err := s.stream.Recv()
		s.observe(event, err)
		return event, err
	}
	result := make(chan receiveResult, 1)
	go func() {
		event, err := s.stream.Recv()
		result <- receiveResult{event: event, err: err}
	}()
	timer := time.NewTimer(s.idleTimeout)
	defer timer.Stop()
	select {
	case value := <-result:
		s.observe(value.event, value.err)
		return value.event, value.err
	case <-timer.C:
		err := protocol.NewProblem(
			protocol.CodeUnavailable,
			fmt.Sprintf("provider stream idle timeout after %s", s.idleTimeout),
			true,
			context.DeadlineExceeded,
		)
		s.failure(err)
		// Cancel the request first so the blocked decoder returns, then close
		// the parser. Closing parser state concurrently with Recv is unsafe.
		s.cancel()
		<-result
		_ = s.Close()
		return provider.StreamEvent{}, err
	}
}

func (s *managedStream) observe(event provider.StreamEvent, err error) {
	if err != nil && !errors.Is(err, io.EOF) {
		s.failure(err)
	}
	if event.Type == provider.EventMessageStop {
		s.success()
	}
	if err != nil || event.Type == provider.EventMessageStop {
		_ = s.Close()
	}
}

func (s *managedStream) Close() (result error) {
	s.closeOnce.Do(func() {
		s.cancel()
		result = s.stream.Close()
		s.release()
	})
	return result
}

func (c *Client) acquire(ctx context.Context) error {
	for {
		c.mu.Lock()
		maximum := c.MaxConcurrent
		if maximum <= 0 {
			maximum = 8
		}
		if c.active < maximum {
			c.active++
			c.health.Active = c.active
			c.health.UpdatedAt = time.Now()
			c.mu.Unlock()
			return nil
		}
		c.mu.Unlock()
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		}
	}
}

func (c *Client) release() {
	c.mu.Lock()
	if c.active > 0 {
		c.active--
	}
	c.health.Active = c.active
	c.health.UpdatedAt = time.Now()
	c.mu.Unlock()
}

func (c *Client) rateLimit(ctx context.Context) error {
	c.mu.Lock()
	rate := c.RequestsPerSecond
	if rate <= 0 {
		c.mu.Unlock()
		return nil
	}
	now := time.Now()
	waitFor := c.nextRequest.Sub(now)
	if waitFor < 0 {
		waitFor = 0
	}
	c.nextRequest = now.Add(waitFor + time.Duration(float64(time.Second)/rate))
	c.mu.Unlock()
	return wait(ctx, waitFor)
}

func (c *Client) recordSuccess() {
	c.mu.Lock()
	c.health.Healthy = true
	c.health.ConsecutiveFailures = 0
	c.health.LastError = ""
	c.health.UpdatedAt = time.Now()
	c.mu.Unlock()
}

func (c *Client) recordFailure(err error) {
	c.mu.Lock()
	c.health.ConsecutiveFailures++
	c.health.Healthy = c.health.ConsecutiveFailures < 3
	c.health.LastError = errorString(err)
	c.health.UpdatedAt = time.Now()
	c.mu.Unlock()
}

func (c *Client) Health() Health {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func joinEndpoint(endpoint, path string) string {
	return strings.TrimRight(endpoint, "/") + path
}

func boundedBody(body io.ReadCloser) string {
	defer body.Close()
	data, _ := io.ReadAll(io.LimitReader(body, maxErrorBodyBytes))
	return strings.TrimSpace(string(data))
}

func retryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := at.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= 500
}

func retryableTransportError(err error) bool {
	var certificateError *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var recordHeaderError tls.RecordHeaderError
	if errors.As(err, &certificateError) ||
		errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &recordHeaderError) {
		return false
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return !dnsError.IsNotFound && (dnsError.IsTimeout || dnsError.IsTemporary)
	}
	if errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
}

func cappedJitter(delay, cap time.Duration, random float64) time.Duration {
	if delay < 0 {
		delay = 0
	}
	if random < 0 {
		random = 0
	}
	if random > 1 {
		random = 1
	}
	delay = time.Duration(float64(delay) * (1 + 0.2*random))
	if delay > cap {
		return cap
	}
	return delay
}

func rateLimitMetadata(header http.Header, retryDelay time.Duration, hasRetryAfter bool) *protocol.RateLimitMetadata {
	metadata := &protocol.RateLimitMetadata{
		Limit:     firstHeader(header, "RateLimit-Limit", "X-RateLimit-Limit"),
		Remaining: firstHeader(header, "RateLimit-Remaining", "X-RateLimit-Remaining"),
		Reset:     firstHeader(header, "RateLimit-Reset", "X-RateLimit-Reset"),
	}
	if hasRetryAfter {
		metadata.RetryAfterMS = uint64(retryDelay / time.Millisecond)
	}
	if metadata.Limit == "" && metadata.Remaining == "" && metadata.Reset == "" && metadata.RetryAfterMS == 0 {
		return nil
	}
	return metadata
}

func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}

func requestKey(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("codehelper-%x-%d", digest[:8], requestSequence.Add(1))
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var _ provider.Provider = (*Client)(nil)
var _ CredentialResolver = EnvCredentials{}
