package web

import (
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	webhost "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/web"
	"github.com/fwtllh-png/CodeHelper/internal/security/credential"
)

func TestWebSetupCatalogRequiresExplicitProviderAndModel(t *testing.T) {
	catalog := webSetupCatalog()
	if catalog.Version != webhost.SetupCatalogVersion || len(catalog.Providers) != 4 {
		t.Fatalf("setup catalog = %+v", catalog)
	}
	for _, id := range []string{"openai", "anthropic", "deepseek", customProviderID} {
		found := false
		for _, provider := range catalog.Providers {
			if provider.ID == id {
				found = true
			}
		}
		if !found {
			t.Errorf("setup provider %q is missing", id)
		}
	}
	for _, provider := range catalog.Providers {
		if provider.ID == "deepseek" && len(provider.Models) == 0 {
			t.Fatal("bundled setup provider did not advertise its model ids")
		}
		if provider.ID == "deepseek" &&
			!slices.Contains(provider.Models, "deepseek-v4-flash") {
			t.Fatal("setup catalog omitted uniquely routable DeepSeek model")
		}
	}
	if _, _, err := resolveWebSetup(webhost.SetupRequest{}); err == nil {
		t.Fatal("empty setup selection was accepted")
	}
	if _, _, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "deepseek-chat",
	}); err == nil {
		t.Fatal("DeepSeek setup without an API key was accepted")
	}
	selection, _, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "deepseek-future-model", APIKey: "secret",
		ModelMetadata: testSetupMetadata("deepseek-future-model"),
	})
	if err != nil ||
		selection.Model != "deepseek-future-model" ||
		selection.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("future model setup = %+v err=%v", selection, err)
	}
	if _, _, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "another-future-model", APIKey: "secret",
	}); err == nil {
		t.Fatal("unknown catalog model without explicit metadata was accepted")
	}
	if _, _, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "openrouter-auto", APIKey: "secret",
	}); err == nil {
		t.Fatal("cross-provider model inherited catalog metadata")
	}
	crossProvider, _, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "openrouter-auto", APIKey: "secret",
		ModelMetadata: testSetupMetadata("openrouter-auto"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if crossProvider.Provider != "deepseek" ||
		crossProvider.BaseURL != "https://api.deepseek.com/v1" ||
		setupRuntimeProviderID(crossProvider) != "deepseek" {
		t.Fatalf("cross-provider metadata leaked: %+v", crossProvider)
	}
	if _, _, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "deepseek-chat", APIKey: "secret",
		ModelMetadata: testSetupMetadata("deepseek-chat"),
	}); err == nil {
		t.Fatal("catalog model accepted operator metadata")
	}
}

func TestWebSetupResolvesKnownModelToOwningProviderPreset(t *testing.T) {
	selection, reference, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "deepseek-v4-flash", APIKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Provider != "deepseek" ||
		selection.Model != "deepseek-v4-flash" ||
		selection.BaseURL != "" ||
		selection.Protocol != string(model.ProtocolOpenAIChat) {
		t.Fatalf("resolved selection = %+v", selection)
	}
	if providerID := setupRuntimeProviderID(selection); providerID != "deepseek-v4-flash" {
		t.Fatalf("runtime provider = %q, want deepseek-v4-flash", providerID)
	}
	if reference.Kind != "keyring" || reference.Name != "deepseek/default" {
		t.Fatalf("resolved credential = %+v", reference)
	}
	if metadata := setupModelMetadata(selection); metadata.Descriptor != nil ||
		metadata.Path != "" {
		t.Fatalf("known model received fallback metadata = %+v", metadata)
	}
	visionSelection, _, err := resolveWebSetup(webhost.SetupRequest{
		Provider: "deepseek", Model: "deepseek-v4-flash-vision-exp",
		APIKey: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	visionRoute, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	resolvedVision, err := visionRoute.Resolve(model.RouteRequest{
		ProviderID: setupRuntimeProviderID(visionSelection),
		ModelID:    visionSelection.Model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if visionSelection.BaseURL != "" ||
		visionSelection.Protocol != string(model.ProtocolOpenAIChat) ||
		!resolvedVision.Model().Capabilities.ImageInput {
		t.Fatalf(
			"resolved vision selection=%+v model=%+v",
			visionSelection,
			resolvedVision.Model(),
		)
	}

	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: setupRuntimeProviderID(selection),
		ModelID:    selection.Model,
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.Model().Limits.ContextTokens != 1_048_576 ||
		route.Model().Limits.MaxOutputTokens != 393_216 ||
		!route.Model().Capabilities.Reasoning ||
		!route.Model().Capabilities.PromptCache {
		t.Fatalf("resolved model capabilities = %+v", route.Model())
	}

	dataDir := t.TempDir()
	if err := saveWebSetupSelection(dataDir, "workspace", selection); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := loadWebSetupSelection(dataDir, "workspace")
	if err != nil || !found || loaded != selection {
		t.Fatalf("loaded selection = %+v found=%v err=%v", loaded, found, err)
	}

	legacy := selection
	legacy.Version = 1
	legacy.BaseURL = "https://api.deepseek.com/v1"
	legacy.Protocol = string(model.ProtocolOpenAIChat)
	legacy.MetadataProvenance = ""
	legacyDataDir := t.TempDir()
	if err := saveWebSetupSelection(legacyDataDir, "workspace", legacy); err != nil {
		t.Fatal(err)
	}
	loaded, found, err = loadWebSetupSelection(legacyDataDir, "workspace")
	if err != nil || !found || loaded != selection {
		t.Fatalf("upgraded selection = %+v found=%v err=%v", loaded, found, err)
	}
}

func TestWebSetupPersistsOnlyNonSecretSelection(t *testing.T) {
	inputMetadata := testSetupMetadata("vendor/model-v1")
	inputMetadata.WireID = "wire-model-v1"
	selection, reference, err := resolveWebSetup(webhost.SetupRequest{
		Provider: customProviderID, Model: "vendor/model-v1",
		BaseURL:  "https://models.example.com/v1/",
		Protocol: "openai_responses", APIKey: "secret-value",
		ModelMetadata: inputMetadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reference.Kind != "" || reference.Name != "" ||
		selection.BaseURL != "https://models.example.com/v1" ||
		selection.MetadataProvenance != model.ProvenanceOperatorConfig {
		t.Fatalf("custom setup = %+v reference=%+v", selection, reference)
	}
	metadata := setupModelMetadata(selection).Descriptor
	if metadata == nil ||
		metadata.Limits.ContextTokens != 65_536 ||
		metadata.Limits.MaxOutputTokens != 8_192 ||
		metadata.WireID != "wire-model-v1" ||
		!metadata.Capabilities.Reasoning ||
		metadata.Capabilities.DefaultReasoningEffort != "high" ||
		metadata.MetadataProvenance.Limits != model.ProvenanceOperatorConfig {
		t.Fatalf("resolved custom metadata = %+v", metadata)
	}
	if wireID := setupWireModelID(selection, selection.Model); wireID != "wire-model-v1" {
		t.Fatalf("wire model id = %q", wireID)
	}
	selection.Credential = &credential.Reference{
		Kind: "keyring",
		Name: "web/setup/00000000000000000000000000000000",
	}
	dataDir := t.TempDir()
	if err := saveWebSetupSelection(dataDir, "workspace", selection); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(setupSelectionPath(dataDir, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-value") {
		t.Fatal("setup selection persisted the API key")
	}
	if !strings.Contains(string(data), `"context_tokens":65536`) ||
		!strings.Contains(string(data), `"reasoning_efforts":["off","high"]`) {
		t.Fatalf("setup selection did not persist model metadata: %s", data)
	}
	loaded, found, err := loadWebSetupSelection(dataDir, "workspace")
	if err != nil || !found || !reflect.DeepEqual(loaded, selection) {
		t.Fatalf("loaded setup = %+v found=%v err=%v", loaded, found, err)
	}
	restored := setupModelMetadata(loaded).Descriptor
	if restored == nil ||
		restored.Limits != metadata.Limits ||
		!reflect.DeepEqual(restored.Capabilities, metadata.Capabilities) ||
		restored.MetadataProvenance != metadata.MetadataProvenance {
		t.Fatalf("restored custom metadata = %+v", restored)
	}
}

func TestWebSetupRejectsMissingOrInvalidCustomMetadata(t *testing.T) {
	base := webhost.SetupRequest{
		Provider: customProviderID, Model: "custom-model",
		BaseURL: "https://models.example.com/v1", Protocol: "openai_chat",
	}
	if _, _, err := resolveWebSetup(base); err == nil {
		t.Fatal("custom setup without metadata was accepted")
	}
	base.ModelMetadata = testSetupMetadata(base.Model)
	base.ModelMetadata.Capabilities.Vision = nil
	if _, _, err := resolveWebSetup(base); err == nil {
		t.Fatal("custom setup with an omitted capability was accepted")
	}
	base.ModelMetadata = testSetupMetadata(base.Model)
	base.ModelMetadata.ContextTokens = 0
	if _, _, err := resolveWebSetup(base); err == nil {
		t.Fatal("custom setup with zero context was accepted")
	}
	base.ModelMetadata = testSetupMetadata(base.Model)
	base.ModelMetadata.MaxOutputTokens = base.ModelMetadata.ContextTokens + 1
	if _, _, err := resolveWebSetup(base); err == nil {
		t.Fatal("custom setup with output above context was accepted")
	}
	base.ModelMetadata = testSetupMetadata(base.Model)
	base.ModelMetadata.Capabilities.DefaultReasoningEffort = "medium"
	if _, _, err := resolveWebSetup(base); err == nil {
		t.Fatal("custom setup with undeclared default effort was accepted")
	}
	base.ModelMetadata = testSetupMetadata(base.Model)
	base.ModelMetadata.Capabilities.Reasoning = boolPointer(false)
	base.ModelMetadata.Capabilities.ReasoningEfforts = nil
	base.ModelMetadata.Capabilities.DefaultReasoningEffort = ""
	base.ModelMetadata.Capabilities.ThinkingToggle = boolPointer(true)
	if _, _, err := resolveWebSetup(base); err == nil {
		t.Fatal("custom setup with thinking toggle but no reasoning was accepted")
	}
	base.ModelMetadata = testSetupMetadata(base.Model)
	base.ModelMetadata.Capabilities.IncrementalResponses = boolPointer(true)
	if _, _, err := resolveWebSetup(base); err == nil {
		t.Fatal("chat setup with incremental responses was accepted")
	}
}

func TestLegacyCustomSelectionRequiresSetup(t *testing.T) {
	dataDir := t.TempDir()
	legacy := webSetupSelection{
		Version: 1, Provider: customProviderID, Model: "legacy-model",
		BaseURL: "https://models.example.com/v1", Protocol: "openai_chat",
	}
	if err := saveWebSetupSelection(dataDir, "workspace", legacy); err != nil {
		t.Fatal(err)
	}
	if _, found, err := loadWebSetupSelection(dataDir, "workspace"); err != nil || found {
		t.Fatalf("legacy custom selection found=%t err=%v", found, err)
	}
}

func testSetupMetadata(modelID string) *webhost.SetupModelMetadata {
	return &webhost.SetupModelMetadata{
		CanonicalID:     modelID,
		WireID:          modelID,
		ContextTokens:   65_536,
		MaxOutputTokens: 8_192,
		Capabilities: webhost.SetupModelCapabilities{
			Streaming:              boolPointer(true),
			Reasoning:              boolPointer(true),
			ReasoningEfforts:       []string{"off", "high"},
			DefaultReasoningEffort: "high",
			ToolCalls:              boolPointer(true),
			NativeSearch:           boolPointer(false),
			IncrementalResponses:   boolPointer(false),
			Vision:                 boolPointer(false),
			ImageInput:             boolPointer(false),
			PromptCache:            boolPointer(true),
			AutomaticPromptCache:   boolPointer(false),
			ThinkingToggle:         boolPointer(false),
		},
	}
}

func boolPointer(value bool) *bool {
	return &value
}
