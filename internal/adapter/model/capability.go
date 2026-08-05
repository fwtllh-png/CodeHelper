package model

import (
	"fmt"
	"strings"
)

// Capability is a named ability a model may or may not have. The set is closed
// on purpose: a filter has to match on something stable, and an open string
// would let a typo look like "this model lacks a feature nobody defined".
type Capability string

const (
	CapStreaming    Capability = "streaming"
	CapReasoning    Capability = "reasoning"
	CapToolCalls    Capability = "tool_calls"
	CapNativeSearch Capability = "native_search"
	// CapVision is what the vision purpose needs: the model can describe an
	// image. It is the bit NewRouteSet checks when a vision slot is configured.
	CapVision Capability = "vision"
	// CapImageInput is what a request carrying an image content block needs.
	// A model can have CapVision without CapImageInput only in theory; in the
	// catalog they travel together, and the request-time check is the one that
	// catches a ContentImage on a text-only route.
	CapImageInput Capability = "image_input"
	// CapPromptCache is what a sticky prompt_cache_key (Responses) needs.
	// Catalogues that support provider-side caching set it; a request that asks
	// for a cache key on a model without it is refused rather than silently
	// dropped, because a dropped key looks like a cache miss forever.
	CapPromptCache Capability = "prompt_cache"
)

// Supports reports whether the bit is set. Unknown names are not supported:
// inventing a capability and asking for it must fail closed, not look like a
// model that happens not to have it.
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

// MissingCapabilities lists the required bits this model does not have, in the
// order they were asked for. An empty return means the model covers the set.
func (c Capabilities) MissingCapabilities(required []Capability) []Capability {
	var missing []Capability
	for _, capability := range required {
		if !c.Supports(capability) {
			missing = append(missing, capability)
		}
	}
	return missing
}

// RequireCapabilities fails when any required bit is off. The error names the
// model and the missing bits so a misconfigured [route.vision] reads as a
// capability problem rather than a provider 400 about an image field.
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

// PurposeRequiredCapabilities is what a purpose's route must cover.
//
// Only purposes whose consumer would actually exercise the bit are listed:
// plan and subquery are ordinary chat, so they inherit whatever act already
// needed; vision is the one that would otherwise send an image to a text-only
// model and get a 400 that names a field the operator never wrote.
func PurposeRequiredCapabilities(purpose Purpose) []Capability {
	switch purpose {
	case PurposeVision:
		return []Capability{CapVision}
	default:
		return nil
	}
}
