package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	webhost "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/web"
	"github.com/fwtllh-png/CodeHelper/internal/persist/atomicfile"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/credential"
)

const (
	webSetupVersion       = 1
	webSupervisorScope    = "web-supervisor"
	customProviderID      = "openai-compatible"
	customContextTokens   = 128_000
	customMaxOutputTokens = 8_192
)

var setupModelIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)

type webSetupSelection struct {
	Version  int    `json:"version"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
	BaseURL  string `json:"base_url,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

type webSetupAttempt struct {
	request webhost.SetupRequest
	result  chan error
}

func webSetupCatalog() webhost.SetupCatalog {
	catalog := model.DefaultCatalog()
	displayNames := map[string]string{
		"openai":    "OpenAI",
		"anthropic": "Anthropic",
		"deepseek":  "DeepSeek",
	}
	providers := make([]webhost.SetupProvider, 0, len(displayNames)+1)
	for _, id := range []string{"openai", "anthropic", "deepseek"} {
		provider, exists := catalog.Provider(id)
		if !exists {
			continue
		}
		providers = append(providers, webhost.SetupProvider{
			ID: id, DisplayName: displayNames[id],
			Protocol:       string(provider.Protocol),
			RequiresAPIKey: provider.Credential.Kind != "",
		})
	}
	providers = append(providers, webhost.SetupProvider{
		ID: customProviderID, DisplayName: "OpenAI-compatible",
		Protocol: string(model.ProtocolOpenAIChat), Custom: true,
	})
	return webhost.SetupCatalog{
		Version: webhost.SetupCatalogVersion, Providers: providers,
	}
}

func resolveWebSetup(request webhost.SetupRequest) (
	webSetupSelection, credential.Reference, error,
) {
	providerID := strings.TrimSpace(request.Provider)
	modelID := strings.TrimSpace(request.Model)
	if providerID == customProviderID {
		baseURL, err := validateSetupBaseURL(request.BaseURL)
		if err != nil {
			return webSetupSelection{}, credential.Reference{}, err
		}
		protocolName := strings.TrimSpace(request.Protocol)
		if protocolName == "" {
			protocolName = string(model.ProtocolOpenAIChat)
		}
		if protocolName != string(model.ProtocolOpenAIChat) &&
			protocolName != string(model.ProtocolOpenAIResponses) {
			return webSetupSelection{}, credential.Reference{}, invalidSetup(
				"custom provider protocol must be openai_chat or openai_responses",
			)
		}
		if !setupModelIDPattern.MatchString(modelID) {
			return webSetupSelection{}, credential.Reference{}, invalidSetup(
				"custom provider model id is invalid",
			)
		}
		return webSetupSelection{
			Version: webSetupVersion, Provider: customProviderID, Model: modelID,
			BaseURL: baseURL, Protocol: protocolName,
		}, credential.Reference{}, nil
	}

	if !setupModelIDPattern.MatchString(modelID) {
		return webSetupSelection{}, credential.Reference{}, invalidSetup(
			"model id is invalid",
		)
	}
	allowed := map[string]bool{"openai": true, "anthropic": true, "deepseek": true}
	catalog := model.DefaultCatalog()
	provider, exists := catalog.Provider(providerID)
	_, directlyKnown := provider.Models[modelID]
	if !exists || !allowed[providerID] && !directlyKnown {
		return webSetupSelection{}, credential.Reference{}, invalidSetup(
			"provider must be OpenAI, Anthropic, DeepSeek, or OpenAI-compatible",
		)
	}
	routeProvider := provider
	if !directlyKnown {
		if matched, found := uniqueSetupProviderForModel(
			catalog,
			provider.Adapter,
			modelID,
		); found {
			routeProvider = matched
			directlyKnown = true
		}
	}
	if routeProvider.Credential.Kind != "" && strings.TrimSpace(request.APIKey) == "" {
		return webSetupSelection{}, credential.Reference{}, invalidSetup("API key is required")
	}
	baseURL := ""
	if !directlyKnown {
		baseURL = provider.Endpoint
	}
	return webSetupSelection{
			Version: webSetupVersion, Provider: providerID, Model: modelID,
			BaseURL:  baseURL,
			Protocol: string(routeProvider.Protocol),
		}, credential.Reference{
			Kind: routeProvider.Credential.Kind, Name: routeProvider.Credential.Name,
		}, nil
}

func uniqueSetupProviderForModel(
	catalog *model.Catalog,
	adapter model.AdapterID,
	modelID string,
) (model.Provider, bool) {
	var match model.Provider
	found := false
	for _, provider := range catalog.Providers() {
		if provider.Adapter != adapter {
			continue
		}
		if _, exists := provider.Models[modelID]; !exists {
			continue
		}
		if found {
			return model.Provider{}, false
		}
		match = provider
		found = true
	}
	return match, found
}

func validateSetupBaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", invalidSetup("custom provider base URL is invalid")
	}
	if parsed.Scheme == "https" {
		return value, nil
	}
	address := net.ParseIP(parsed.Hostname())
	if parsed.Scheme == "http" &&
		(parsed.Hostname() == "localhost" || address != nil && address.IsLoopback()) {
		return value, nil
	}
	return "", invalidSetup("custom provider must use HTTPS or loopback HTTP")
}

func setupModelMetadata(selection webSetupSelection) wire.ModelMetadataOptions {
	if selection.BaseURL == "" {
		return wire.ModelMetadataOptions{}
	}
	return wire.ModelMetadataOptions{
		ContextTokens: customContextTokens, ContextSet: true,
		MaxOutputTokens: customMaxOutputTokens, OutputSet: true,
		Capabilities: "streaming,tool_calls", CapabilitiesSet: true,
	}
}

func setupRuntimeProviderID(selection webSetupSelection) string {
	if selection.Provider == customProviderID || selection.BaseURL != "" {
		return selection.Provider
	}
	catalog := model.DefaultCatalog()
	provider, exists := catalog.Provider(selection.Provider)
	if !exists {
		return selection.Provider
	}
	if routeProvider, found := uniqueSetupProviderForModel(
		catalog,
		provider.Adapter,
		selection.Model,
	); found {
		return routeProvider.ID
	}
	return selection.Provider
}

func setupSelectionPath(dataDir, _ string) string {
	return filepath.Join(dataDir, "web-setup", "selection.json")
}

func loadWebSetupConfig(
	options webCommandOptions,
	selection webSetupSelection,
	reference credential.Reference,
) (config.Snapshot, error) {
	overrides := webConfigOverrides(options)
	runtimeProviderID := setupRuntimeProviderID(selection)
	overrides.Provider = &runtimeProviderID
	overrides.Model = &selection.Model
	overrides.Protocol = &selection.Protocol
	overrides.CredentialKind = &reference.Kind
	overrides.CredentialName = &reference.Name
	return config.Load(config.LoadOptions{
		Path: options.configPath, Overrides: overrides,
	})
}

func loadWebSetupSelection(dataDir, workspaceID string) (webSetupSelection, bool, error) {
	data, err := os.ReadFile(setupSelectionPath(dataDir, workspaceID))
	if errors.Is(err, os.ErrNotExist) {
		return webSetupSelection{}, false, nil
	}
	if err != nil {
		return webSetupSelection{}, false, fmt.Errorf("read Web setup selection: %w", err)
	}
	var selection webSetupSelection
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&selection); err != nil {
		return webSetupSelection{}, false, fmt.Errorf("decode Web setup selection: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return webSetupSelection{}, false, errors.New("Web setup selection has trailing data")
	}
	request := webhost.SetupRequest{
		Provider: selection.Provider, Model: selection.Model,
		BaseURL: selection.BaseURL, Protocol: selection.Protocol, APIKey: "persisted",
	}
	resolved, _, err := resolveWebSetup(request)
	if err != nil {
		return webSetupSelection{}, false, err
	}
	if resolved != selection && !canUpgradeSetupSelection(selection, resolved) {
		return webSetupSelection{}, false, errors.New("Web setup selection is not canonical")
	}
	return resolved, true, nil
}

func canUpgradeSetupSelection(previous, resolved webSetupSelection) bool {
	return previous.Version == webSetupVersion &&
		previous.Provider != customProviderID &&
		previous.Provider == resolved.Provider &&
		previous.Model == resolved.Model &&
		resolved.BaseURL == ""
}

func saveWebSetupSelection(dataDir, workspaceID string, selection webSetupSelection) error {
	path := setupSelectionPath(dataDir, workspaceID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(selection)
	if err != nil {
		return err
	}
	return atomicfile.Replace(path, append(data, '\n'), 0o600)
}

func invalidSetup(message string) error {
	return protocol.NewProblem(protocol.CodeInvalidArgument, message, false, nil)
}
