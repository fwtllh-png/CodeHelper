package web

import (
	"os"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	webhost "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/web"
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
	})
	if err != nil ||
		selection.Model != "deepseek-future-model" ||
		selection.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("future model setup = %+v err=%v", selection, err)
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
		selection.Protocol != string(model.ProtocolOpenAIResponses) {
		t.Fatalf("resolved selection = %+v", selection)
	}
	if providerID := setupRuntimeProviderID(selection); providerID != "deepseek-v4-flash" {
		t.Fatalf("runtime provider = %q, want deepseek-v4-flash", providerID)
	}
	if reference.Kind != "keyring" || reference.Name != "deepseek/default" {
		t.Fatalf("resolved credential = %+v", reference)
	}
	if metadata := setupModelMetadata(selection); metadata.ContextSet ||
		metadata.OutputSet || metadata.CapabilitiesSet {
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
		visionSelection.Protocol != string(model.ProtocolOpenAIResponses) ||
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
	legacy.BaseURL = "https://api.deepseek.com/v1"
	legacy.Protocol = string(model.ProtocolOpenAIChat)
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
	selection, reference, err := resolveWebSetup(webhost.SetupRequest{
		Provider: customProviderID, Model: "vendor/model-v1",
		BaseURL:  "https://models.example.com/v1/",
		Protocol: "openai_responses", APIKey: "secret-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reference.Kind != "" || reference.Name != "" ||
		selection.BaseURL != "https://models.example.com/v1" {
		t.Fatalf("custom setup = %+v reference=%+v", selection, reference)
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
	loaded, found, err := loadWebSetupSelection(dataDir, "workspace")
	if err != nil || !found || loaded != selection {
		t.Fatalf("loaded setup = %+v found=%v err=%v", loaded, found, err)
	}
}
