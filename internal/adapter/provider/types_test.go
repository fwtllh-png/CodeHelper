package provider

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func TestModelRequestValidation(t *testing.T) {
	route := routeWithoutNativeSearch(t)
	request := ModelRequest{
		Route:           route,
		Messages:        []Message{TextMessage(RoleUser, "hello")},
		MaxOutputTokens: 128,
	}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}

	request.NativeSearch = true
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "native search") {
		t.Fatalf("Validate() error = %v, want native search capability error", err)
	}
	request.NativeSearch = false
	request.MaxOutputTokens = 2048
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "model limit") {
		t.Fatalf("Validate() error = %v, want output limit error", err)
	}
}

func TestReasoningEffortRequiresAReasoningModel(t *testing.T) {
	route := routeWithoutNativeSearch(t)
	request := ModelRequest{
		Route:           route,
		Messages:        []Message{TextMessage(RoleUser, "hello")},
		MaxOutputTokens: 128,
		ReasoningEffort: "high",
	}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "reasoning") {
		t.Fatalf("Validate() error = %v, want a reasoning capability refusal", err)
	}
}

func TestAnImageBlockRequiresImageInputOrVision(t *testing.T) {
	route := routeWithoutNativeSearch(t)
	request := ModelRequest{
		Route: route,
		Messages: []Message{{
			Role: RoleUser,
			Blocks: []ContentBlock{
				{Type: ContentText, Text: "what is this"},
				{Type: ContentImage, Attachment: &Attachment{
					MediaType: "image/png", Data: []byte("PNG"),
				}},
			},
		}},
		MaxOutputTokens: 128,
	}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "image input") {
		t.Fatalf("Validate() error = %v, want an image input refusal", err)
	}
}

func TestVisionPurposeRequiresVisionCapability(t *testing.T) {
	route := routeWithoutNativeSearch(t)
	request := ModelRequest{
		Route:           route,
		Purpose:         model.PurposeVision,
		Messages:        []Message{TextMessage(RoleUser, "describe")},
		MaxOutputTokens: 128,
	}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "vision") {
		t.Fatalf("Validate() error = %v, want a vision purpose refusal", err)
	}
}

func TestImageBlockValidation(t *testing.T) {
	png := []byte("PNG")
	valid := ContentBlock{
		Type:       ContentImage,
		Attachment: &Attachment{MediaType: "image/png", Data: png, Name: "shot.png"},
	}
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	if url := valid.Attachment.DataURL(); url != "data:image/png;base64,UE5H" {
		t.Fatalf("DataURL() = %q", url)
	}

	empty := ContentBlock{Type: ContentImage, Attachment: &Attachment{MediaType: "image/png"}}
	if err := empty.Validate(); err == nil || !strings.Contains(err.Error(), "image bytes") {
		t.Fatalf("Validate() error = %v, want missing bytes", err)
	}
	// A media type nobody checked is a 400 from the provider that names a field
	// the caller never wrote, so it is refused here where the attachment is
	// still identifiable.
	wrong := ContentBlock{
		Type:       ContentImage,
		Attachment: &Attachment{MediaType: "application/pdf", Data: png},
	}
	if err := wrong.Validate(); err == nil || !strings.Contains(err.Error(), "not an image") {
		t.Fatalf("Validate() error = %v, want media type refusal", err)
	}
}

func TestStreamEventValidation(t *testing.T) {
	valid := []StreamEvent{
		{Type: EventMessageStart},
		{Type: EventTextDelta, Text: "hello"},
		{Type: EventReasoningDelta, Text: "think"},
		{Type: EventToolCallDelta, ToolCall: &ToolCallFragment{Index: 0, Name: "search"}},
		{Type: EventUsage, Usage: &Usage{InputTokens: 1}},
		{Type: EventMessageStop},
	}
	for _, event := range valid {
		if err := event.Validate(); err != nil {
			t.Errorf("event %+v: %v", event, err)
		}
	}
	if err := (StreamEvent{Type: EventTextDelta}).Validate(); err == nil {
		t.Fatal("empty text delta is valid")
	}
}

func TestStickyPromptCacheKeyOmitsWhenResponsesLacksCapability(t *testing.T) {
	chat := routeWithoutNativeSearch(t)
	if got := StickyPromptCacheKey("session:1", chat); got != "" {
		t.Fatalf("chat without prompt_cache StickyPromptCacheKey = %q, want empty", got)
	}

	responses := responsesRoute(t, false)
	if got := StickyPromptCacheKey("session:1", responses); got != "" {
		t.Fatalf("unsupported Responses StickyPromptCacheKey = %q, want empty", got)
	}
	if got := StickyPromptCacheKey("", responses); got != "" {
		t.Fatalf("empty key = %q", got)
	}

	supported := responsesRoute(t, true)
	if got := StickyPromptCacheKey("session:1", supported); got != "session:1" {
		t.Fatalf("supported Responses StickyPromptCacheKey = %q", got)
	}
}

func TestPromptCacheKeyRequiresCapability(t *testing.T) {
	route := responsesRoute(t, false)
	request := ModelRequest{
		Route:           route,
		Messages:        []Message{TextMessage(RoleUser, "hello")},
		MaxOutputTokens: 128,
		PromptCacheKey:  "session:1",
	}
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "prompt cache") {
		t.Fatalf("Validate() error = %v, want prompt cache refusal", err)
	}
	request.PromptCacheKey = StickyPromptCacheKey("session:1", route)
	if err := request.Validate(); err != nil {
		t.Fatalf("Validate after sticky omit: %v", err)
	}

	chat := routeWithoutNativeSearch(t)
	request.Route = chat
	request.PromptCacheKey = "session:1"
	if err := request.Validate(); err == nil || !strings.Contains(err.Error(), "prompt cache") {
		t.Fatalf("chat Validate() error = %v, want prompt cache refusal", err)
	}
}

func responsesRoute(t *testing.T, promptCache bool) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "responses-test", Adapter: model.AdapterOpenAICompatible, Endpoint: "http://127.0.0.1:1",
		Protocol: model.ProtocolOpenAIResponses, Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{
			"model": {
				ID: "model", CanonicalID: "model", WireID: "model",
				Limits: model.Limits{ContextTokens: 4096, MaxOutputTokens: 1024},
				Capabilities: model.Capabilities{
					Streaming: true, PromptCache: promptCache,
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
	route, err := resolver.Resolve(model.RouteRequest{ProviderID: "responses-test", ModelID: "model"})
	if err != nil {
		t.Fatal(err)
	}
	return route
}

func routeWithoutNativeSearch(t *testing.T) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "test", Adapter: model.AdapterOpenAICompatible, Endpoint: "http://127.0.0.1:1",
		Protocol: model.ProtocolOpenAIChat, Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{
			"model": {
				ID: "model", CanonicalID: "model", WireID: "model",
				Limits:       model.Limits{ContextTokens: 4096, MaxOutputTokens: 1024},
				Capabilities: model.Capabilities{Streaming: true},
				Pricing:      model.Pricing{Currency: "USD", Provenance: model.ProvenanceFixture},
				Provenance:   model.ProvenanceFixture,
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
	route, err := resolver.Resolve(model.RouteRequest{ProviderID: "test", ModelID: "model"})
	if err != nil {
		t.Fatal(err)
	}
	return route
}
