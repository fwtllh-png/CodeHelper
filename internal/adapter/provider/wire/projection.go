package wire

import (
	"encoding/json"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// CompleteStatelessProjection records content-safe evidence for an adapter
// that sends every logical message on every request.
func CompleteStatelessProjection(
	request provider.ModelRequest,
	reason provider.ProjectionFallbackReason,
) provider.ProjectionReceipt {
	receipt := provider.CompleteProjection(request.Projection, reason)
	descriptor, err := request.Route.Identity()
	if err == nil {
		receipt.RouteDigest = projectionDigest(descriptor)
	}
	receipt.PropertyDigest = projectionDigest(struct {
		MaxOutputTokens uint64                    `json:"max_output_tokens"`
		ReasoningEffort string                    `json:"reasoning_effort,omitempty"`
		NativeSearch    bool                      `json:"native_search,omitempty"`
		Tools           []provider.ToolDefinition `json:"tools,omitempty"`
	}{request.MaxOutputTokens, request.ReasoningEffort, request.NativeSearch, request.Tools})
	receipt.InputDigest = projectionDigest(request.Messages)
	receipt.LogicalItems, receipt.TransportItems = len(request.Messages), len(request.Messages)
	return receipt
}

func projectionDigest(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return Digest(encoded)
}
