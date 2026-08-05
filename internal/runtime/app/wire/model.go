package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

type ModelMetadataOptions struct {
	Path             string
	ContextTokens    uint64
	MaxOutputTokens  uint64
	Capabilities     string
	InputPerMillion  float64
	OutputPerMillion float64
	Currency         string
	ContextSet       bool
	OutputSet        bool
	CapabilitiesSet  bool
	InputPriceSet    bool
	OutputPriceSet   bool
	CurrencySet      bool
}

type modelMetadataFile struct {
	CanonicalID     string             `json:"canonical_id"`
	WireID          string             `json:"wire_id"`
	ContextTokens   uint64             `json:"context_tokens"`
	MaxOutputTokens uint64             `json:"max_output_tokens"`
	Capabilities    model.Capabilities `json:"capabilities"`
	Pricing         struct {
		InputPerMillion  float64 `json:"input_per_million"`
		OutputPerMillion float64 `json:"output_per_million"`
		Currency         string  `json:"currency"`
	} `json:"pricing"`
}

func resolveModelMetadata(modelID string, options ModelMetadataOptions) (*model.Model, error) {
	descriptor := model.Model{
		ID: modelID, CanonicalID: modelID, WireID: modelID, Provenance: model.ProvenanceCLI,
		MetadataProvenance: model.MetadataProvenance{
			CanonicalID: model.ProvenanceCLI, WireID: model.ProvenanceCLI,
		},
	}
	if options.Path != "" {
		loaded, err := loadModelMetadata(options.Path, modelID)
		if err != nil {
			return nil, err
		}
		descriptor = loaded
	}
	if options.ContextSet || options.OutputSet {
		if !options.ContextSet || !options.OutputSet {
			return nil, errors.New("context and output token overrides must be provided together")
		}
		descriptor.Limits = model.Limits{
			ContextTokens: options.ContextTokens, MaxOutputTokens: options.MaxOutputTokens,
		}
		descriptor.MetadataProvenance.Limits = model.ProvenanceCLI
	}
	if options.CapabilitiesSet {
		capabilities, err := parseCapabilities(options.Capabilities)
		if err != nil {
			return nil, err
		}
		descriptor.Capabilities = capabilities
		descriptor.MetadataProvenance.Capabilities = model.ProvenanceCLI
	}
	if options.InputPriceSet || options.OutputPriceSet || options.CurrencySet {
		if !options.InputPriceSet || !options.OutputPriceSet || !options.CurrencySet {
			return nil, errors.New("input price, output price, and currency overrides must be provided together")
		}
		if options.InputPerMillion < 0 || options.OutputPerMillion < 0 ||
			!strings.EqualFold(options.Currency, "USD") {
			return nil, errors.New("pricing overrides must be non-negative and use USD")
		}
		descriptor.Pricing = model.Pricing{
			InputPerMillion: options.InputPerMillion, OutputPerMillion: options.OutputPerMillion,
			Currency: options.Currency, Known: true, Provenance: model.ProvenanceCLI,
		}
		descriptor.MetadataProvenance.Pricing = model.ProvenanceCLI
	}
	if descriptor.Limits.ContextTokens == 0 ||
		descriptor.Limits.MaxOutputTokens == 0 ||
		descriptor.MetadataProvenance.Capabilities == "" ||
		!descriptor.Pricing.Known {
		return nil, errors.New("custom endpoint requires explicit limits, capabilities, and known pricing metadata")
	}
	if descriptor.Limits.MaxOutputTokens > descriptor.Limits.ContextTokens {
		return nil, errors.New("model output limit exceeds context limit")
	}
	return &descriptor, nil
}

func loadModelMetadata(path, modelID string) (model.Model, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return model.Model{}, fmt.Errorf("read model metadata: %w", err)
	}
	var input modelMetadataFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return model.Model{}, fmt.Errorf("decode model metadata: %w", err)
	}
	if input.CanonicalID == "" || input.WireID == "" || input.Pricing.Currency == "" {
		return model.Model{}, errors.New("model metadata requires canonical_id, wire_id, and pricing currency")
	}
	if !strings.EqualFold(input.Pricing.Currency, "USD") {
		return model.Model{}, errors.New("model metadata pricing currency must be USD")
	}
	return model.Model{
		ID: inputModelID(modelID), CanonicalID: input.CanonicalID, WireID: input.WireID,
		Limits: inputLimits(input), Capabilities: input.Capabilities,
		Pricing: model.Pricing{
			InputPerMillion:  input.Pricing.InputPerMillion,
			OutputPerMillion: input.Pricing.OutputPerMillion,
			Currency:         input.Pricing.Currency, Known: true, Provenance: model.ProvenanceConfig,
		},
		MetadataProvenance: model.MetadataProvenance{
			CanonicalID: model.ProvenanceConfig, WireID: model.ProvenanceConfig,
			Limits: model.ProvenanceConfig, Capabilities: model.ProvenanceConfig,
			Pricing: model.ProvenanceConfig,
		},
		Provenance: model.ProvenanceConfig,
	}, nil
}

func inputModelID(modelID string) string { return modelID }

func inputLimits(input modelMetadataFile) model.Limits {
	return model.Limits{
		ContextTokens: input.ContextTokens, MaxOutputTokens: input.MaxOutputTokens,
	}
}

func parseCapabilities(value string) (model.Capabilities, error) {
	var result model.Capabilities
	seen := make(map[string]bool)
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		switch item {
		case "streaming":
			result.Streaming = true
		case "reasoning":
			result.Reasoning = true
		case "tool_calls":
			result.ToolCalls = true
		case "native_search":
			result.NativeSearch = true
		default:
			return model.Capabilities{}, fmt.Errorf("unknown model capability %q", item)
		}
	}
	if !result.Streaming {
		return model.Capabilities{}, errors.New("custom endpoint must declare streaming capability")
	}
	return result, nil
}
