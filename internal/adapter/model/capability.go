package model

import (
	"fmt"
	"strings"
)

// Capability is a closed model ability used for route validation.
type Capability string

const (
	CapStreaming            Capability = "streaming"
	CapReasoning            Capability = "reasoning"
	CapToolCalls            Capability = "tool_calls"
	CapNativeSearch         Capability = "native_search"
	CapIncrementalResponses Capability = "incremental_responses"
	// CapVision is required by the vision purpose.
	CapVision Capability = "vision"
	// CapImageInput permits image content blocks.
	CapImageInput Capability = "image_input"
	// CapPromptCache permits a sticky provider cache key.
	CapPromptCache Capability = "prompt_cache"
)

// Supports reports whether a known bit is set and fails closed for unknowns.
func (c Capabilities) Supports(capability Capability) bool {
	switch capability {
	case CapStreaming:
		return c.Streaming
	case CapReasoning:
		return c.Reasoning
	case CapToolCalls:
		return c.ToolCalls
	case CapNativeSearch:
		return c.NativeSearch
	case CapIncrementalResponses:
		return c.IncrementalResponses
	case CapVision:
		return c.Vision
	case CapImageInput:
		return c.ImageInput
	case CapPromptCache:
		return c.PromptCache
	default:
		return false
	}
}

// MissingCapabilities preserves required-bit order.
func (c Capabilities) MissingCapabilities(required []Capability) []Capability {
	var missing []Capability
	for _, capability := range required {
		if !c.Supports(capability) {
			missing = append(missing, capability)
		}
	}
	return missing
}

// RequireCapabilities reports the model and every missing bit.
func RequireCapabilities(modelID string, have Capabilities, required []Capability) error {
	missing := have.MissingCapabilities(required)
	if len(missing) == 0 {
		return nil
	}
	names := make([]string, len(missing))
	for index, capability := range missing {
		names[index] = string(capability)
	}
	return fmt.Errorf(
		"model %q does not support %s",
		modelID, strings.Join(names, ", "),
	)
}

// PurposeRequiredCapabilities returns capabilities exercised by a purpose.
func PurposeRequiredCapabilities(purpose Purpose) []Capability {
	switch purpose {
	case PurposeVision:
		return []Capability{CapVision}
	default:
		return nil
	}
}
