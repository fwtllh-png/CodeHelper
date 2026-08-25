package protocol

import (
	"errors"
	"fmt"
	"strings"
)

const ModelCatalogVersion = 1

type ProviderCatalogEntry struct {
	ID           string `json:"id"`
	DisplayName  string `json:"display_name"`
	Selected     bool   `json:"selected"`
	Availability string `json:"availability"`
	Reason       string `json:"reason,omitempty"`
}

type ProviderCatalog struct {
	Version   int                    `json:"version"`
	Providers []ProviderCatalogEntry `json:"providers"`
}

type ModelCatalogEntry struct {
	Provider     string            `json:"provider"`
	ID           string            `json:"id"`
	Source       string            `json:"source,omitempty"`
	Selected     bool              `json:"selected"`
	Capabilities ModelCapabilities `json:"capabilities"`
}

type ModelCatalog struct {
	Version int                 `json:"version"`
	Models  []ModelCatalogEntry `json:"models"`
}

func (c ProviderCatalog) Validate() error {
	if c.Version != ModelCatalogVersion || len(c.Providers) > 256 {
		return errors.New("provider catalog version or size is invalid")
	}
	seen := make(map[string]struct{}, len(c.Providers))
	for index, provider := range c.Providers {
		if !validProfileIdentifier(provider.ID) ||
			strings.TrimSpace(provider.DisplayName) == "" ||
			len(provider.DisplayName) > 256 ||
			strings.ContainsAny(provider.DisplayName, "\x00\r\n") {
			return fmt.Errorf("provider catalog entry %d is invalid", index)
		}
		switch provider.Availability {
		case "available", "unavailable":
		default:
			return fmt.Errorf("provider catalog entry %q has invalid availability", provider.ID)
		}
		if provider.Availability == "unavailable" &&
			strings.TrimSpace(provider.Reason) == "" {
			return fmt.Errorf("provider catalog entry %q requires a reason", provider.ID)
		}
		if _, duplicate := seen[provider.ID]; duplicate {
			return fmt.Errorf("provider catalog entry %q is duplicated", provider.ID)
		}
		seen[provider.ID] = struct{}{}
	}
	return nil
}

func (c ModelCatalog) Validate() error {
	if c.Version != ModelCatalogVersion || len(c.Models) > 4096 {
		return errors.New("model catalog version or size is invalid")
	}
	seen := make(map[string]struct{}, len(c.Models))
	for index, model := range c.Models {
		if !validProfileIdentifier(model.Provider) ||
			!validProfileIdentifier(model.ID) {
			return fmt.Errorf("model catalog entry %d has invalid identity", index)
		}
		if _, duplicate := seen[model.Provider+"\x00"+model.ID]; duplicate {
			return fmt.Errorf("model catalog entry %q is duplicated", model.ID)
		}
		seen[model.Provider+"\x00"+model.ID] = struct{}{}
		profile := SessionProfile{
			Version: SessionProfileVersion, Revision: 1,
			Mode: "act", Provider: model.Provider, Model: model.ID,
			ApprovalPosture: "never", ExecutionTarget: "local",
			MaxSteps: 1, PromptCacheRevision: 1,
		}
		capabilities := SessionProfileCapabilities{
			Provider: model.Provider, Model: model.ID,
			ModelCapabilities: model.Capabilities,
		}
		if err := capabilities.Validate(profile); err != nil {
			return fmt.Errorf("model catalog entry %q: %w", model.ID, err)
		}
	}
	return nil
}
