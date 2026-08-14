package contextdump

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestCanonicalContextDumpMatchesGoldenAndOmitsRawContent(t *testing.T) {
	route := testRoute(t)
	assistant := provider.ProducedAssistant(
		route,
		[]provider.ContentBlock{
			{Type: provider.ContentReasoning, Text: "private reasoning"},
			{
				Type: provider.ContentToolCall,
				ToolCall: &provider.ToolCall{
					ID: "call-1", Name: "read_file",
					Arguments: `{"path":"secret/config.go"}`,
				},
			},
		},
		1,
		&provider.ReplayState{Version: provider.ReplayVersion, Data: []byte(`{"opaque":"state"}`)},
	)
	temperature := 0.2
	request := provider.ModelRequest{
		Route: route, Purpose: model.PurposeAct,
		Messages: []provider.Message{
			provider.TextMessage(provider.RoleSystem, "system secret"),
			provider.TextMessage(provider.RoleUser, "user secret"),
			assistant,
			{
				Role: provider.RoleTool, Turn: 1,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolResult,
					ToolResult: &provider.ToolResult{
						CallID: "call-1", Content: "tool secret", IsError: true,
					},
				}},
			},
			{
				Role: provider.RoleUser, Turn: 2,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentImage,
					Attachment: &provider.Attachment{
						MediaType: "image/png", Name: "private-shot.png",
						Data: []byte("private image bytes"),
					},
				}},
			},
		},
		MaxOutputTokens: 1024, Temperature: &temperature, ReasoningEffort: "high",
		NativeSearch: true, Idempotent: true, PromptCacheKey: "private-cache-key",
		Tools: []provider.ToolDefinition{{
			Name: "read_file", Description: "read a private file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		}},
	}
	attribution := protocol.SampleContextData{
		Reason: "normal", ReasoningEffort: "high",
		StableTokens: 10, HistoryUserTokens: 11,
		HistoryAssistantTokens: 12, HistoryToolTokens: 13,
		DynamicTokens: 14, ToolDefinitionTokens: 15,
		ProviderFramingTokens: 9, EstimatedTokens: 84,
		MessageCount: 5, ToolDefinitionCount: 1,
	}
	snapshot, err := Build(request, attribution)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range [][]byte{
		[]byte("system secret"), []byte("user secret"), []byte("private reasoning"),
		[]byte("secret/config.go"), []byte("tool secret"), []byte("private-shot.png"),
		[]byte("private image bytes"), []byte("private-cache-key"),
		[]byte("read a private file"), []byte(`"properties":{"path"`),
	} {
		if bytes.Contains(encoded, secret) {
			t.Fatalf("context dump retained raw content %q", secret)
		}
	}
	golden, err := os.ReadFile(filepath.Join("testdata", "canonical.golden.json"))
	if err != nil {
		t.Fatalf("read canonical golden: %v\n\ngot:\n%s", err, encoded)
	}
	golden = bytes.TrimSpace(golden)
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("canonical context dump drifted\nwant:\n%s\n\ngot:\n%s", golden, encoded)
	}
}

func testRoute(t *testing.T) model.ReadyRoute {
	t.Helper()
	descriptor := model.Model{
		ID: "fixture-model", CanonicalID: "fixture-model", WireID: "fixture-model",
		Limits: model.Limits{ContextTokens: 128_000, MaxOutputTokens: 4096},
		Capabilities: model.Capabilities{
			Streaming: true, Reasoning: true, ToolCalls: true,
			NativeSearch: true, ImageInput: true, PromptCache: true,
		},
		Provenance: model.ProvenanceFixture,
	}
	catalog, err := model.NewCatalog(model.Provider{
		ID: "fixture", Adapter: model.AdapterOpenAI,
		Endpoint:   "https://fixture.invalid/v1",
		Protocol:   model.ProtocolOpenAIResponses,
		Models:     map[string]model.Model{descriptor.ID: descriptor},
		Provenance: model.ProvenanceFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "fixture", ModelID: descriptor.ID,
		Provenance: model.ProvenanceFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	return route
}
