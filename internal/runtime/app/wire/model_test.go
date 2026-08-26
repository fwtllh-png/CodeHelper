package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func TestResolveModelMetadataConfigAndOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "model.json")
	data := []byte(`{
		"canonical_id":"configured-model",
		"wire_id":"wire-model",
		"context_tokens":32768,
		"max_output_tokens":4096,
		"capabilities":{"streaming":true,"reasoning":true,"tool_calls":false,"native_search":false},
		"pricing":{"input_per_million":1.5,"output_per_million":4.5,"currency":"USD"}
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	descriptor, err := resolveModelMetadata("custom", ModelMetadataOptions{
		Path: path, ContextTokens: 65536, MaxOutputTokens: 8192,
		ContextSet: true, OutputSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Limits.ContextTokens != 65536 ||
		descriptor.MetadataProvenance.Limits != model.ProvenanceStartup ||
		descriptor.MetadataProvenance.Capabilities != model.ProvenanceConfig ||
		descriptor.MetadataProvenance.Pricing != model.ProvenanceConfig ||
		!descriptor.Pricing.Known {
		t.Fatalf("descriptor = %+v", descriptor)
	}
}

func TestResolveModelMetadataRejectsUnknownFacts(t *testing.T) {
	if _, err := resolveModelMetadata("custom", ModelMetadataOptions{}); err == nil {
		t.Fatal("resolveModelMetadata() accepted unknown facts")
	}
	if _, err := resolveExecRoute(execRouteOptions{
		ProviderID: "custom", ModelID: "custom", BaseURL: "http://127.0.0.1:1",
		Protocol: model.ProtocolOpenAIChat,
	}); err == nil {
		t.Fatal("resolveExecRoute() accepted missing metadata")
	}
}

func TestResolveModelMetadataAllowsExplicitUnknownPricing(t *testing.T) {
	descriptor, err := resolveModelMetadata("custom", ModelMetadataOptions{
		ContextTokens: 128_000, ContextSet: true,
		MaxOutputTokens: 8_192, OutputSet: true,
		Capabilities: "streaming,tool_calls", CapabilitiesSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if descriptor.Pricing.Known {
		t.Fatalf("custom pricing = %+v, want explicit unknown pricing", descriptor.Pricing)
	}
}

func TestEndpointOverrideUsesCatalogAdapterWithoutNameInference(t *testing.T) {
	deepSeek, err := resolveExecRoute(execRouteOptions{
		ProviderID: "deepseek", ModelID: "deepseek-chat",
		BaseURL: "http://127.0.0.1:1", Protocol: model.ProtocolOpenAIChat,
		Model: fixtureModel("deepseek-chat"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if deepSeek.Adapter() != model.AdapterOpenAICompatible {
		t.Fatalf(
			"known provider adapter = %q, want openai_compatible",
			deepSeek.Adapter(),
		)
	}
	lookalike, err := resolveExecRoute(execRouteOptions{
		ProviderID: "deepseek-shadow", ModelID: "shadow",
		BaseURL: "http://127.0.0.1:1", Protocol: model.ProtocolOpenAIChat,
		Model: fixtureModel("shadow"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if lookalike.Adapter() != model.AdapterOpenAICompatible {
		t.Fatalf("unknown provider adapter = %q, want openai_compatible", lookalike.Adapter())
	}
	futureModel, err := resolveModelMetadata("gpt-future", ModelMetadataOptions{
		ContextTokens: 128_000, ContextSet: true,
		MaxOutputTokens: 8_192, OutputSet: true,
		Capabilities: "streaming,tool_calls", CapabilitiesSet: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	future, err := resolveExecRoute(execRouteOptions{
		ProviderID: "openai", ModelID: "gpt-future",
		BaseURL:  "https://api.openai.com/v1",
		Protocol: model.ProtocolOpenAIChat, Model: futureModel,
	})
	if err != nil {
		t.Fatal(err)
	}
	if future.Model().ID != "gpt-future" || future.Adapter() != model.AdapterOpenAI {
		t.Fatalf("future provider model route = %+v", future)
	}
}

func TestResolveModelMetadataRejectsPartialOverrides(t *testing.T) {
	if _, err := resolveModelMetadata("custom", ModelMetadataOptions{
		ContextTokens: 4096, ContextSet: true,
	}); err == nil {
		t.Fatal("resolveModelMetadata() accepted partial limits")
	}
}

func TestResolveModelMetadataRejectsNonUSDPricing(t *testing.T) {
	if _, err := resolveModelMetadata("custom", ModelMetadataOptions{
		ContextTokens: 4096, MaxOutputTokens: 1024,
		ContextSet: true, OutputSet: true,
		Capabilities: "streaming", CapabilitiesSet: true,
		InputPerMillion: 1, OutputPerMillion: 2, Currency: "CNY",
		InputPriceSet: true, OutputPriceSet: true, CurrencySet: true,
	}); err == nil {
		t.Fatal("resolveModelMetadata() accepted non-USD pricing")
	}
}
