// Package contextdump builds deterministic, content-safe descriptions of
// model-visible requests for context-engineering characterization.
package contextdump

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const SchemaVersion = 1

// Snapshot describes one complete logical model request without retaining raw
// prompt, tool argument, result, attachment, replay, or schema content.
type Snapshot struct {
	SchemaVersion         int                        `json:"schema_version"`
	LogicalRequestDigest  string                     `json:"logical_request_digest"`
	Route                 Route                      `json:"route"`
	Request               Request                    `json:"request"`
	Messages              []Message                  `json:"messages"`
	Tools                 []Tool                     `json:"tools,omitempty"`
	Attribution           protocol.SampleContextData `json:"attribution"`
	AttributedTokens      uint64                     `json:"attributed_tokens"`
	UnknownEstimatedToken uint64                     `json:"unknown_estimated_tokens"`
	AttributionCoverageBP uint64                     `json:"attribution_coverage_basis_points"`
}

type Route struct {
	Provider string `json:"provider"`
	Adapter  string `json:"adapter"`
	Protocol string `json:"protocol"`
	Model    string `json:"model"`
	Purpose  string `json:"purpose,omitempty"`
}

type Request struct {
	MaxOutputTokens     uint64   `json:"max_output_tokens"`
	Temperature         *float64 `json:"temperature,omitempty"`
	ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	NativeSearch        bool     `json:"native_search,omitempty"`
	Idempotent          bool     `json:"idempotent,omitempty"`
	PromptCacheKeySet   bool     `json:"prompt_cache_key_set,omitempty"`
	PromptCacheKeyHash  string   `json:"prompt_cache_key_digest,omitempty"`
	Store               *bool    `json:"store,omitempty"`
	ParallelTools       *bool    `json:"parallel_tools,omitempty"`
	Include             []string `json:"include,omitempty"`
	MessageCount        int      `json:"message_count"`
	ToolDefinitionCount int      `json:"tool_definition_count"`
}

type Message struct {
	Index      int     `json:"index"`
	Role       string  `json:"role"`
	Turn       uint64  `json:"turn,omitempty"`
	Provenance *Origin `json:"provenance,omitempty"`
	Blocks     []Block `json:"blocks"`
}

type Origin struct {
	Adapter       string `json:"adapter"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ReplayVersion uint32 `json:"replay_version,omitempty"`
	ReplayBytes   int    `json:"replay_bytes,omitempty"`
	ReplayDigest  string `json:"replay_digest,omitempty"`
	ReplayBoundTo string `json:"replay_content_digest,omitempty"`
}

type Block struct {
	Index          int    `json:"index"`
	Type           string `json:"type"`
	ContentDigest  string `json:"content_digest"`
	ID             string `json:"id,omitempty"`
	TextBytes      int    `json:"text_bytes,omitempty"`
	TextDigest     string `json:"text_digest,omitempty"`
	CallID         string `json:"call_id,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	ArgumentsBytes int    `json:"arguments_bytes,omitempty"`
	ArgumentsHash  string `json:"arguments_digest,omitempty"`
	ResultBytes    int    `json:"result_bytes,omitempty"`
	ResultDigest   string `json:"result_digest,omitempty"`
	ResultError    bool   `json:"result_error,omitempty"`
	QueryDigest    string `json:"query_digest,omitempty"`
	SourceCount    int    `json:"source_count,omitempty"`
	URLDigest      string `json:"url_digest,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	AttachmentName string `json:"attachment_name_digest,omitempty"`
	AttachmentSize int    `json:"attachment_bytes,omitempty"`
	AttachmentHash string `json:"attachment_digest,omitempty"`
}

type Tool struct {
	Index             int    `json:"index"`
	Name              string `json:"name"`
	DescriptionBytes  int    `json:"description_bytes"`
	DescriptionDigest string `json:"description_digest"`
	SchemaBytes       int    `json:"schema_bytes"`
	SchemaDigest      string `json:"schema_digest"`
}

// Build returns a stable characterization snapshot for request.
func Build(
	request provider.ModelRequest,
	attribution protocol.SampleContextData,
) (Snapshot, error) {
	if err := request.Route.Validate(); err != nil {
		return Snapshot{}, fmt.Errorf("describe context route: %w", err)
	}
	result := Snapshot{
		SchemaVersion: SchemaVersion,
		Route: Route{
			Provider: request.Route.ProviderID(),
			Adapter:  string(request.Route.Adapter()),
			Protocol: string(request.Route.Protocol()),
			Model:    request.Route.Model().ID,
			Purpose:  string(request.Purpose),
		},
		Request: Request{
			MaxOutputTokens:     request.MaxOutputTokens,
			Temperature:         request.Temperature,
			ReasoningEffort:     request.ReasoningEffort,
			NativeSearch:        request.NativeSearch,
			Idempotent:          request.Idempotent,
			PromptCacheKeySet:   request.PromptCacheKey != "",
			Store:               request.Store,
			ParallelTools:       request.ParallelTools,
			Include:             append([]string(nil), request.Include...),
			MessageCount:        len(request.Messages),
			ToolDefinitionCount: len(request.Tools),
		},
		Attribution: attribution,
	}
	if request.PromptCacheKey != "" {
		result.Request.PromptCacheKeyHash = digestString(request.PromptCacheKey)
	}
	for index, message := range request.Messages {
		result.Messages = append(result.Messages, summarizeMessage(index, message))
	}
	for index, definition := range request.Tools {
		encoded, err := json.Marshal(definition.InputSchema)
		if err != nil {
			return Snapshot{}, fmt.Errorf("encode tool schema %q: %w", definition.Name, err)
		}
		result.Tools = append(result.Tools, Tool{
			Index: index, Name: definition.Name,
			DescriptionBytes:  len(definition.Description),
			DescriptionDigest: digestString(definition.Description),
			SchemaBytes:       len(encoded), SchemaDigest: digestBytes(encoded),
		})
	}
	result.AttributedTokens = attributedTokens(attribution)
	result.UnknownEstimatedToken = attribution.EstimatedTokens -
		min(attribution.EstimatedTokens, result.AttributedTokens)
	if attribution.EstimatedTokens != 0 {
		result.AttributionCoverageBP = min(
			10_000,
			result.AttributedTokens*10_000/attribution.EstimatedTokens,
		)
	}
	identity := struct {
		Route    Route                      `json:"route"`
		Request  Request                    `json:"request"`
		Messages []Message                  `json:"messages"`
		Tools    []Tool                     `json:"tools,omitempty"`
		Context  protocol.SampleContextData `json:"context"`
	}{
		Route: result.Route, Request: result.Request, Messages: result.Messages,
		Tools: result.Tools, Context: attribution,
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode logical context identity: %w", err)
	}
	result.LogicalRequestDigest = digestBytes(encoded)
	return result, nil
}

// Marshal renders a snapshot in the canonical checked-in artifact form.
func Marshal(snapshot Snapshot) ([]byte, error) {
	return json.MarshalIndent(snapshot, "", "  ")
}

func summarizeMessage(index int, message provider.Message) Message {
	result := Message{
		Index: index, Role: string(message.Role), Turn: message.Turn,
		Blocks: make([]Block, 0, len(message.Blocks)),
	}
	if message.Provenance != nil {
		result.Provenance = &Origin{
			Adapter:  string(message.Provenance.Adapter),
			Provider: message.Provenance.Provider,
			Model:    message.Provenance.Model,
		}
		if replay := message.Provenance.Replay; replay != nil {
			result.Provenance.ReplayVersion = replay.Version
			result.Provenance.ReplayBytes = len(replay.Data)
			result.Provenance.ReplayDigest = digestBytes(replay.Data)
			result.Provenance.ReplayBoundTo = replay.ContentDigest
		}
	}
	for index, block := range message.Blocks {
		encoded, _ := json.Marshal(block)
		item := Block{
			Index: index, Type: string(block.Type), ID: block.ID,
			ContentDigest: digestBytes(encoded),
		}
		if block.Text != "" {
			item.TextBytes, item.TextDigest = len(block.Text), digestString(block.Text)
		}
		if call := block.ToolCall; call != nil {
			item.CallID, item.ToolName = call.ID, call.Name
			item.ArgumentsBytes, item.ArgumentsHash =
				len(call.Arguments), digestString(call.Arguments)
		}
		if output := block.ToolResult; output != nil {
			item.CallID, item.ResultError = output.CallID, output.IsError
			item.ResultBytes, item.ResultDigest =
				len(output.Content), digestString(output.Content)
		}
		if search := block.Search; search != nil {
			item.QueryDigest = digestString(search.Query)
			item.SourceCount = len(search.Sources)
		}
		if citation := block.Citation; citation != nil {
			item.URLDigest = digestString(citation.URL)
		}
		if attachment := block.Attachment; attachment != nil {
			item.MediaType = attachment.MediaType
			item.AttachmentName = digestString(attachment.Name)
			item.AttachmentSize = len(attachment.Data)
			item.AttachmentHash = digestBytes(attachment.Data)
		}
		result.Blocks = append(result.Blocks, item)
	}
	return result
}

func attributedTokens(value protocol.SampleContextData) uint64 {
	return value.StableTokens + value.HistoryUserTokens +
		value.HistoryAssistantTokens + value.HistoryToolTokens +
		value.HistoryOtherTokens + value.DynamicTokens +
		value.ContinuationTokens + value.ToolDefinitionTokens +
		value.ProviderFramingTokens
}

func digestString(value string) string {
	return digestBytes([]byte(value))
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}
