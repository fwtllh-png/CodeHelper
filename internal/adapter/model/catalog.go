package model

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

//go:embed catalog.v1.json
var bundledCatalog []byte

type ProviderKind string

const (
	ProviderOpenAI    ProviderKind = "openai"
	ProviderAnthropic ProviderKind = "anthropic"
	ProviderLocal     ProviderKind = "local"
	ProviderCustom    ProviderKind = "custom"
)

type WireProtocol string

const (
	ProtocolOpenAIChat      WireProtocol = "openai_chat"
	ProtocolOpenAIResponses WireProtocol = "openai_responses"
	ProtocolAnthropic       WireProtocol = "anthropic"
)

type Provenance string

const (
	ProvenanceBundled Provenance = "bundled"
	ProvenanceConfig  Provenance = "config"
	ProvenanceCLI     Provenance = "cli"
	ProvenanceFixture Provenance = "fixture"
)

type CredentialRef struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name,omitempty"`
}

type Limits struct {
	ContextTokens   uint64 `json:"context_tokens"`
	MaxOutputTokens uint64 `json:"max_output_tokens"`
}

type Capabilities struct {
	Streaming    bool `json:"streaming"`
	Reasoning    bool `json:"reasoning"`
	ToolCalls    bool `json:"tool_calls"`
	NativeSearch bool `json:"native_search"`
	// Vision is what the vision purpose samples on. A [route.vision] pointing
	// at a model with Vision=false is refused at route-set construction rather
	// than at the first image_analyze call.
	Vision bool `json:"vision"`
	// ImageInput is what a request that carries a ContentImage block needs.
	ImageInput bool `json:"image_input"`
	// PromptCache is what a sticky prompt_cache_key (Responses encode) needs.
	PromptCache bool `json:"prompt_cache"`
}

type Pricing struct {
	InputPerMillion       float64    `json:"input_per_million"`
	CachedInputPerMillion *float64   `json:"cached_input_per_million,omitempty"`
	OutputPerMillion      float64    `json:"output_per_million"`
	Currency              string     `json:"currency"`
	Known                 bool       `json:"known"`
	Provenance            Provenance `json:"provenance"`
}

type MetadataProvenance struct {
	CanonicalID  Provenance `json:"canonical_id"`
	WireID       Provenance `json:"wire_id"`
	Limits       Provenance `json:"limits"`
	Capabilities Provenance `json:"capabilities"`
	Pricing      Provenance `json:"pricing"`
}

type Model struct {
	ID                 string             `json:"id"`
	CanonicalID        string             `json:"canonical_id"`
	WireID             string             `json:"wire_id"`
	Limits             Limits             `json:"limits"`
	Capabilities       Capabilities       `json:"capabilities"`
	Pricing            Pricing            `json:"pricing"`
	MetadataProvenance MetadataProvenance `json:"metadata_provenance"`
	Provenance         Provenance         `json:"provenance"`
}

type Provider struct {
	ID         string           `json:"id"`
	Kind       ProviderKind     `json:"kind"`
	Endpoint   string           `json:"endpoint"`
	Protocol   WireProtocol     `json:"protocol"`
	Credential CredentialRef    `json:"credential"`
	Models     map[string]Model `json:"models"`
	Provenance Provenance       `json:"provenance"`
}

type Catalog struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewCatalog(providers ...Provider) (*Catalog, error) {
	catalog := &Catalog{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if err := catalog.AddProvider(provider); err != nil {
			return nil, err
		}
	}
	return catalog, nil
}

func DefaultCatalog() *Catalog {
	var document struct {
		Version   int        `json:"version"`
		Providers []Provider `json:"providers"`
	}
	if err := json.Unmarshal(bundledCatalog, &document); err != nil {
		panic(fmt.Errorf("decode bundled model catalog: %w", err))
	}
	if document.Version != 1 {
		panic(fmt.Errorf("unsupported bundled model catalog version %d", document.Version))
	}
	catalog, err := NewCatalog(document.Providers...)
	if err != nil {
		panic(err)
	}
	return catalog
}

func (c *Catalog) AddProvider(provider Provider) error {
	provider = normalizeProvider(provider)
	if err := validateProvider(provider); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.providers[provider.ID]; exists {
		return fmt.Errorf("provider %q already exists", provider.ID)
	}
	c.providers[provider.ID] = cloneProvider(provider)
	return nil
}

func (c *Catalog) Provider(id string) (Provider, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	provider, exists := c.providers[id]
	return cloneProvider(provider), exists
}

func (c *Catalog) Providers() []Provider {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ids := make([]string, 0, len(c.providers))
	for id := range c.providers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]Provider, 0, len(ids))
	for _, id := range ids {
		result = append(result, cloneProvider(c.providers[id]))
	}
	return result
}

func validateProvider(provider Provider) error {
	if strings.TrimSpace(provider.ID) == "" {
		return errors.New("provider id is required")
	}
	switch provider.Kind {
	case ProviderOpenAI, ProviderAnthropic, ProviderLocal, ProviderCustom:
	default:
		return fmt.Errorf("provider %q has unsupported kind %q", provider.ID, provider.Kind)
	}
	switch provider.Protocol {
	case ProtocolOpenAIChat, ProtocolOpenAIResponses, ProtocolAnthropic:
	default:
		return fmt.Errorf("provider %q has unsupported protocol %q", provider.ID, provider.Protocol)
	}
	endpoint, err := url.Parse(provider.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return fmt.Errorf("provider %q has invalid endpoint", provider.ID)
	}
	if len(provider.Models) == 0 {
		return fmt.Errorf("provider %q has no models", provider.ID)
	}
	for key, model := range provider.Models {
		if key == "" || model.ID != key || model.CanonicalID == "" || model.WireID == "" {
			return fmt.Errorf("provider %q has invalid model %q", provider.ID, key)
		}
		if model.Limits.ContextTokens == 0 || model.Limits.MaxOutputTokens == 0 {
			return fmt.Errorf("provider %q model %q has invalid limits", provider.ID, key)
		}
		if model.Limits.MaxOutputTokens > model.Limits.ContextTokens {
			return fmt.Errorf("provider %q model %q output limit exceeds context", provider.ID, key)
		}
		if model.Pricing.Known && model.Pricing.Currency == "" {
			return fmt.Errorf("provider %q model %q known pricing requires currency", provider.ID, key)
		}
		if cached := model.Pricing.CachedInputPerMillion; cached != nil &&
			(!model.Pricing.Known || *cached < 0) {
			return fmt.Errorf("provider %q model %q has invalid cached input pricing", provider.ID, key)
		}
		if !model.Pricing.Known && (model.Pricing.InputPerMillion != 0 || model.Pricing.OutputPerMillion != 0) {
			return fmt.Errorf("provider %q model %q unknown pricing must not set dollar rates", provider.ID, key)
		}
	}
	if provider.Credential.Kind != "" && provider.Credential.Kind != "env" && provider.Credential.Kind != "keyring" {
		return fmt.Errorf("provider %q has invalid credential kind", provider.ID)
	}
	if provider.Credential.Kind != "" && provider.Credential.Name == "" {
		return fmt.Errorf("provider %q credential name is required", provider.ID)
	}
	return nil
}

func normalizeProvider(provider Provider) Provider {
	provider = cloneProvider(provider)
	for id, descriptor := range provider.Models {
		// Do not infer Known from Currency: unknown prices must stay known=false
		// without fabricating $0 display facts.
		if descriptor.Pricing.Provenance == "" {
			descriptor.Pricing.Provenance = descriptor.Provenance
		}
		provenance := &descriptor.MetadataProvenance
		if provenance.CanonicalID == "" {
			provenance.CanonicalID = descriptor.Provenance
		}
		if provenance.WireID == "" {
			provenance.WireID = descriptor.Provenance
		}
		if provenance.Limits == "" {
			provenance.Limits = descriptor.Provenance
		}
		if provenance.Capabilities == "" {
			provenance.Capabilities = descriptor.Provenance
		}
		if provenance.Pricing == "" {
			provenance.Pricing = descriptor.Pricing.Provenance
		}
		provider.Models[id] = descriptor
	}
	return provider
}

func cloneProvider(provider Provider) Provider {
	if provider.Models == nil {
		return provider
	}
	models := make(map[string]Model, len(provider.Models))
	for id, model := range provider.Models {
		models[id] = model
	}
	provider.Models = models
	return provider
}

// CredentialHelpEntry is a non-secret credential reference hint for auth slots.
type CredentialHelpEntry struct {
	ProviderID string        `json:"provider_id"`
	Credential CredentialRef `json:"credential"`
}

// CredentialHelp returns bundled env credential slots for providers that need auth.
func (c *Catalog) CredentialHelp() []CredentialHelpEntry {
	providers := c.Providers()
	result := make([]CredentialHelpEntry, 0, len(providers))
	for _, provider := range providers {
		if provider.Credential.Kind == "" || provider.Credential.Name == "" {
			continue
		}
		result = append(result, CredentialHelpEntry{
			ProviderID: provider.ID, Credential: provider.Credential,
		})
	}
	return result
}
