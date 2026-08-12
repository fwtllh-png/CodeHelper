package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type ContentType string

const (
	ContentText       ContentType = "text"
	ContentReasoning  ContentType = "reasoning"
	ContentToolCall   ContentType = "tool_call"
	ContentToolResult ContentType = "tool_result"
	ContentSearch     ContentType = "search"
	ContentCitation   ContentType = "citation"
	ContentProvider   ContentType = "provider"
	// ContentImage carries an image a message shows the model. It exists so an
	// image travels the same path as everything else a request contains — retry,
	// idempotency key, usage, cost and trace — rather than through a side
	// channel that none of those reach.
	ContentImage ContentType = "image"
)

// Attachment is binary content a message carries. Data is the bytes themselves
// rather than a path or a URL: the encoders need the bytes, and a provider that
// fetched a local path would need the file to be reachable from the provider,
// which for a workspace file it is not.
type Attachment struct {
	MediaType string `json:"media_type"`
	Data      []byte `json:"data"`
	// Name is what the content was called locally. It is for the model's
	// benefit — "the screenshot you asked about" — and no encoder requires it.
	Name string `json:"name,omitempty"`
}

// DataURL is the base64 data URL form every supported protocol accepts for
// inline images.
func (a Attachment) DataURL() string {
	return "data:" + a.MediaType + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
}

// Base64 is the raw payload, which is what Anthropic's image source wants
// beside a separate media type.
func (a Attachment) Base64() string {
	return base64.StdEncoding.EncodeToString(a.Data)
}

// ContentBlock is the lossless provider-neutral history unit. ProviderData is
// used for opaque replay data (for example OpenAI encrypted reasoning items)
// which must be sent back unchanged.
type ContentBlock struct {
	Type         ContentType     `json:"type"`
	Text         string          `json:"text,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	ID           string          `json:"id,omitempty"`
	ToolCall     *ToolCall       `json:"tool_call,omitempty"`
	ToolResult   *ToolResult     `json:"tool_result,omitempty"`
	Search       *SearchResult   `json:"search,omitempty"`
	Citation     *Citation       `json:"citation,omitempty"`
	Attachment   *Attachment     `json:"attachment,omitempty"`
	ProviderType string          `json:"provider_type,omitempty"`
	ProviderData json.RawMessage `json:"provider_data,omitempty"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

type Message struct {
	Role   Role           `json:"role"`
	Blocks []ContentBlock `json:"content"`
	Turn   uint64         `json:"-"`
}

func TextMessage(role Role, text string) Message {
	return Message{Role: role, Blocks: []ContentBlock{{Type: ContentText, Text: text}}}
}

func (m Message) Text() string {
	var result string
	for _, block := range m.Blocks {
		if block.Type == ContentText {
			result += block.Text
		}
	}
	return result
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	// Catalog identity is internal execution authority. Providers never see it;
	// the engine binds it after parsing a call from the sampled tools[] snapshot.
	CatalogID         string `json:"-"`
	CatalogGeneration uint64 `json:"-"`
	CatalogRevision   uint64 `json:"-"`
	CatalogAuthority  uint64 `json:"-"`
}

type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type ModelRequest struct {
	Route model.ReadyRoute `json:"-"`
	// Purpose is what this call is for. It is not sent to the provider: it is
	// how the call is attributed locally, so a token spent by the vision tool is
	// distinguishable from a token spent by the turn's main sampling. An empty
	// purpose reads as the main one.
	Purpose         model.Purpose    `json:"purpose,omitempty"`
	Messages        []Message        `json:"messages"`
	MaxOutputTokens uint64           `json:"max_output_tokens"`
	Temperature     *float64         `json:"temperature,omitempty"`
	ReasoningEffort string           `json:"reasoning_effort,omitempty"`
	NativeSearch    bool             `json:"native_search,omitempty"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	Idempotent      bool             `json:"idempotent,omitempty"`
	// PromptCacheKey is a sticky session/thread key for provider prompt caching (W5.4).
	// Prefer StickyPromptCacheKey when attaching the session default: routes
	// without prompt_cache must not carry a key, or Validate refuses the turn.
	PromptCacheKey string   `json:"prompt_cache_key,omitempty"`
	Store          *bool    `json:"store,omitempty"`
	ParallelTools  *bool    `json:"parallel_tools,omitempty"`
	Include        []string `json:"include,omitempty"`
}

// StickyPromptCacheKey returns key when the route advertises prompt_cache, else
// "". Session engines always want a sticky hint; models without the capability
// (custom Chat/Responses endpoints, DeepSeek, etc.) must drop it so Validate
// does not refuse the turn and encode does not emit a contradictory field.
func StickyPromptCacheKey(key string, route model.ReadyRoute) string {
	if key == "" || !route.Model().Capabilities.PromptCache {
		return ""
	}
	return key
}

func (r ModelRequest) Validate() error {
	if err := r.Route.Validate(); err != nil {
		return fmt.Errorf("route: %w", err)
	}
	if len(r.Messages) == 0 {
		return errors.New("messages are required")
	}
	for index, message := range r.Messages {
		switch message.Role {
		case RoleSystem, RoleUser, RoleAssistant, RoleTool:
		default:
			return fmt.Errorf("messages[%d] has invalid role %q", index, message.Role)
		}
		if len(message.Blocks) == 0 {
			return fmt.Errorf("messages[%d] has no content", index)
		}
		for blockIndex, block := range message.Blocks {
			if err := block.Validate(); err != nil {
				return fmt.Errorf("messages[%d].content[%d]: %w", index, blockIndex, err)
			}
		}
	}
	if r.MaxOutputTokens == 0 {
		return errors.New("max_output_tokens must be positive")
	}
	descriptor := r.Route.Model()
	if r.MaxOutputTokens > descriptor.Limits.MaxOutputTokens {
		return fmt.Errorf("max_output_tokens exceeds model limit %d", descriptor.Limits.MaxOutputTokens)
	}
	caps := descriptor.Capabilities
	// reasoning_effort is the consumer that capabilities.reasoning was missing
	// for years: without this check the bit is a claim nobody verifies, and a
	// non-reasoning model gets a provider 400 about a field the operator never
	// saw in the catalog.
	if r.ReasoningEffort != "" && !caps.Reasoning {
		return errors.New("model does not support reasoning")
	}
	if r.NativeSearch && !caps.NativeSearch {
		return errors.New("model does not support provider-native search")
	}
	if len(r.Tools) > 0 && !caps.ToolCalls {
		return errors.New("model does not support tool calls")
	}
	if r.Purpose == model.PurposeVision {
		if err := model.RequireCapabilities(descriptor.ID, caps, []model.Capability{model.CapVision}); err != nil {
			return err
		}
	}
	if hasImageBlock(r.Messages) {
		// Either bit is enough: a vision-purpose model is expected to accept an
		// image, and a main-turn model marked image_input can too. Requiring
		// both would refuse models that only advertise one of the two names.
		if !caps.ImageInput && !caps.Vision {
			return fmt.Errorf("model %q does not support image input", descriptor.ID)
		}
	}
	if r.PromptCacheKey != "" && !caps.PromptCache {
		return fmt.Errorf("model %q does not support prompt cache", descriptor.ID)
	}
	for index, definition := range r.Tools {
		if definition.Name == "" || definition.Description == "" || definition.InputSchema == nil {
			return fmt.Errorf("tools[%d] is incomplete", index)
		}
	}
	return nil
}

func hasImageBlock(messages []Message) bool {
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.Type == ContentImage {
				return true
			}
		}
	}
	return false
}

func (b ContentBlock) Validate() error {
	switch b.Type {
	case ContentText:
		if b.Text == "" {
			return errors.New("text is required")
		}
	case ContentReasoning:
		if b.Text == "" && b.Signature == "" && len(b.ProviderData) == 0 {
			return errors.New("reasoning text, signature, or provider data is required")
		}
	case ContentToolCall:
		if b.ToolCall == nil || b.ToolCall.ID == "" || b.ToolCall.Name == "" {
			return errors.New("complete tool call is required")
		}
	case ContentToolResult:
		if b.ToolResult == nil || b.ToolResult.CallID == "" {
			return errors.New("tool result call_id is required")
		}
	case ContentSearch:
		if b.Search == nil {
			return errors.New("search result is required")
		}
	case ContentCitation:
		if b.Citation == nil || b.Citation.URL == "" {
			return errors.New("citation URL is required")
		}
	case ContentProvider:
		if b.ProviderType == "" || len(b.ProviderData) == 0 {
			return errors.New("provider type and data are required")
		}
	case ContentImage:
		if b.Attachment == nil || len(b.Attachment.Data) == 0 {
			return errors.New("image bytes are required")
		}
		// The media type is checked rather than guessed: every protocol sends it
		// verbatim, and a wrong one is rejected by the provider with an error
		// that says nothing about which attachment was wrong.
		if !strings.HasPrefix(b.Attachment.MediaType, "image/") {
			return fmt.Errorf("image media type %q is not an image", b.Attachment.MediaType)
		}
	default:
		return fmt.Errorf("unknown content block type %q", b.Type)
	}
	return nil
}

type StreamEventType string

const (
	EventMessageStart       StreamEventType = "message_start"
	EventTextDelta          StreamEventType = "text_delta"
	EventReasoningDelta     StreamEventType = "reasoning_delta"
	EventReasoningSignature StreamEventType = "reasoning_signature"
	EventToolCallDelta      StreamEventType = "tool_call_delta"
	EventSearchResult       StreamEventType = "search_result"
	EventCitation           StreamEventType = "citation"
	EventUsage              StreamEventType = "usage"
	EventMessageStop        StreamEventType = "message_stop"
)

type StopReason string

const (
	StopReasonEndTurn       StopReason = "end_turn"
	StopReasonToolUse       StopReason = "tool_use"
	StopReasonMaxTokens     StopReason = "max_tokens"
	StopReasonContentFilter StopReason = "content_filter"
	StopReasonIncomplete    StopReason = "incomplete"
	StopReasonUnknown       StopReason = "unknown"
)

func (r StopReason) Incomplete() bool {
	switch r {
	case StopReasonMaxTokens, StopReasonContentFilter, StopReasonIncomplete:
		return true
	default:
		return false
	}
}

// Usage counts one provider call. Two of the four fields are breakdowns of the
// other two rather than additions to them:
//
//	CachedTokens    ⊆ InputTokens
//	ReasoningTokens ⊆ OutputTokens
//
// Every adapter must preserve that. Providers do not agree on it themselves —
// OpenAI reports both as detail fields inside the totals, while Anthropic
// reports cache reads and writes beside input_tokens — so the adapter, not the
// caller, is where the two shapes are reconciled.
type Usage struct {
	InputTokens     uint64 `json:"input_tokens"`
	OutputTokens    uint64 `json:"output_tokens"`
	ReasoningTokens uint64 `json:"reasoning_tokens,omitempty"`
	CachedTokens    uint64 `json:"cached_tokens,omitempty"`
}

// Total is every token the call consumed. Cached and reasoning tokens are
// deliberately absent: they are already counted inside the two totals, so
// adding them would bill the same tokens twice.
func (u Usage) Total() uint64 {
	return u.InputTokens + u.OutputTokens
}

func (u *Usage) Add(other Usage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.ReasoningTokens += other.ReasoningTokens
	u.CachedTokens += other.CachedTokens
}

type ToolCallFragment struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type SearchResult struct {
	Query   string   `json:"query,omitempty"`
	Sources []Source `json:"sources"`
	Error   string   `json:"error,omitempty"`
}

type Source struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title,omitempty"`
	URL   string `json:"url"`
}

type Citation struct {
	SourceID string `json:"source_id,omitempty"`
	URL      string `json:"url"`
	Title    string `json:"title,omitempty"`
	Start    int    `json:"start,omitempty"`
	End      int    `json:"end,omitempty"`
}

type StreamEvent struct {
	Type       StreamEventType   `json:"type"`
	StopReason StopReason        `json:"stop_reason,omitempty"`
	Index      int               `json:"index,omitempty"`
	Block      *ContentBlock     `json:"block,omitempty"`
	Text       string            `json:"text,omitempty"`
	Signature  string            `json:"signature,omitempty"`
	ToolCall   *ToolCallFragment `json:"tool_call,omitempty"`
	Search     *SearchResult     `json:"search,omitempty"`
	Citation   *Citation         `json:"citation,omitempty"`
	Usage      *Usage            `json:"usage,omitempty"`
}

func (e StreamEvent) Validate() error {
	if e.Block != nil {
		if err := e.Block.Validate(); err != nil {
			return fmt.Errorf("stream block: %w", err)
		}
	}
	switch e.Type {
	case EventMessageStart:
		if e.StopReason != "" {
			return errors.New("message start cannot have a stop reason")
		}
		return nil
	case EventMessageStop:
		switch e.StopReason {
		case "", StopReasonEndTurn, StopReasonToolUse, StopReasonMaxTokens,
			StopReasonContentFilter, StopReasonIncomplete, StopReasonUnknown:
		default:
			return fmt.Errorf("unknown stop reason %q", e.StopReason)
		}
		return nil
	case EventTextDelta, EventReasoningDelta:
		if e.Text == "" && e.Block == nil {
			return errors.New("stream text delta is empty")
		}
	case EventReasoningSignature:
		if e.Signature == "" && (e.Block == nil || e.Block.Signature == "") {
			return errors.New("reasoning signature is empty")
		}
	case EventToolCallDelta:
		if e.ToolCall == nil {
			return errors.New("tool call fragment is required")
		}
	case EventUsage:
		if e.Usage == nil {
			return errors.New("usage is required")
		}
	case EventSearchResult:
		if e.Search == nil {
			return errors.New("search result is required")
		}
		for index, source := range e.Search.Sources {
			if source.URL == "" {
				return fmt.Errorf("search source %d URL is required", index)
			}
		}
	case EventCitation:
		if e.Citation == nil || e.Citation.URL == "" {
			return errors.New("citation URL is required")
		}
	default:
		return fmt.Errorf("unknown stream event type %q", e.Type)
	}
	return nil
}

type Stream interface {
	Recv() (StreamEvent, error)
	Close() error
}

type Provider interface {
	Stream(context.Context, ModelRequest) (Stream, error)
}
