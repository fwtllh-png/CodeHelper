package model

import "fmt"

// CapabilityObservation is one probe (or user) verdict about a model ability.
// It lives in SQLite beside the catalog and never rewrites catalog.v1.json.
type CapabilityObservation struct {
	ProviderID string
	ModelID    string
	Capability Capability
	Supported  bool
	Source     string // probe / user
	Detail     string
	ObservedAt string // RFC3339
}

// ApplyProbe intersects catalog capabilities with observations (RFC-010 D3).
//
// supported=false always clears the bit (tighten). supported=true only sets the
// bit when trustProbe is true (widen requires an explicit operator nod). Matching
// observations leave the catalog bit alone.
func ApplyProbe(base Capabilities, observations []CapabilityObservation, trustProbe bool) Capabilities {
	out := base
	for _, observation := range observations {
		if !observation.Supported {
			out = out.clear(observation.Capability)
			continue
		}
		if trustProbe {
			out = out.set(observation.Capability)
		}
	}
	return out
}

func (c Capabilities) clear(capability Capability) Capabilities {
	switch capability {
	case CapStreaming:
		c.Streaming = false
	case CapReasoning:
		c.Reasoning = false
	case CapToolCalls:
		c.ToolCalls = false
	case CapNativeSearch:
		c.NativeSearch = false
	case CapVision:
		c.Vision = false
	case CapImageInput:
		c.ImageInput = false
	case CapPromptCache:
		c.PromptCache = false
	}
	return c
}

func (c Capabilities) set(capability Capability) Capabilities {
	switch capability {
	case CapStreaming:
		c.Streaming = true
	case CapReasoning:
		c.Reasoning = true
	case CapToolCalls:
		c.ToolCalls = true
	case CapNativeSearch:
		c.NativeSearch = true
	case CapVision:
		c.Vision = true
	case CapImageInput:
		c.ImageInput = true
	case CapPromptCache:
		c.PromptCache = true
	}
	return c
}

// ForceCapabilities turns the named bits on so a probe request can leave the
// catalog and still pass ModelRequest.Validate long enough to learn the truth.
func ForceCapabilities(base Capabilities, capabilities ...Capability) Capabilities {
	for _, capability := range capabilities {
		base = base.set(capability)
	}
	return base
}

// WithEndpoint returns a copy pointed at a different URL (fixture / probe override).
func (r ReadyRoute) WithEndpoint(endpoint string) ReadyRoute {
	out := r
	out.endpoint = endpoint
	return out
}

// WithCapabilities returns a copy of the route whose model capabilities are
// replaced. Probe overlays use this rather than mutating a shared catalog entry.
func (r ReadyRoute) WithCapabilities(caps Capabilities) ReadyRoute {
	out := r
	out.model.Capabilities = caps
	return out
}

// ParseCapability maps a CLI / SQLite capability name onto the closed set.
func ParseCapability(raw string) (Capability, error) {
	capability := Capability(raw)
	switch capability {
	case CapStreaming, CapReasoning, CapToolCalls, CapNativeSearch,
		CapVision, CapImageInput, CapPromptCache:
		return capability, nil
	default:
		return "", fmt.Errorf("unknown capability %q", raw)
	}
}
