package openai

import (
	"encoding/json"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
)

func (a *Adapter) prepareProjection(
	request provider.ModelRequest,
	call providerwire.PreparedCall,
) provider.ProjectionReceipt {
	receipt := provider.CompleteProjection(
		request.Projection,
		provider.ProjectionFallbackNoCommittedResponse,
	)
	receipt.RouteDigest = routeDigest(request.Route)
	if call.Protocol != model.ProtocolOpenAIResponses {
		receipt.FallbackReason = provider.ProjectionFallbackProtocolUnsupported
		return receipt
	}
	_, input, property, err := responsesSocketBody(call.Body)
	if err == nil {
		receipt.PropertyDigest = property
		receipt.InputDigest = rawMessagesDigest(input)
		receipt.LogicalItems = len(input)
		receipt.TransportItems = len(input)
	}
	if !request.Route.Model().Capabilities.IncrementalResponses {
		receipt.FallbackReason = provider.ProjectionFallbackCapabilityDisabled
		return receipt
	}
	if request.PromptCacheKey == "" {
		receipt.FallbackReason = provider.ProjectionFallbackCacheKeyMissing
		return receipt
	}
	receipt.Mode = provider.ProjectionModeFullSession
	session := a.session(request)
	session.mu.Lock()
	if session.forceHTTP {
		session.forceHTTP = false
		receipt.Mode = provider.ProjectionModeFullHTTP
		if request.Projection.Retry {
			receipt.FallbackReason = provider.ProjectionFallbackRetry
		} else {
			receipt.FallbackReason = provider.ProjectionFallbackConnectionReset
		}
	}
	session.mu.Unlock()
	return receipt
}

func routeDigest(route model.ReadyRoute) string {
	descriptor, err := route.Describe()
	if err != nil {
		return ""
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		return ""
	}
	return providerwire.Digest(encoded)
}

func rawMessagesDigest(messages []json.RawMessage) string {
	if len(messages) == 0 {
		return ""
	}
	encoded, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	return providerwire.Digest(encoded)
}

func evaluateProjection(
	session *responsesSession,
	request provider.ModelRequest,
	call providerwire.PreparedCall,
	input []json.RawMessage,
	property string,
) provider.ProjectionReceipt {
	receipt := call.Projection
	receipt.Mode = provider.ProjectionModeFullSession
	receipt.IncrementalEligible = false
	receipt.ContextRevision = request.Projection.ContextRevision
	receipt.WindowID = request.Projection.WindowID
	receipt.WindowNumber = request.Projection.WindowNumber
	receipt.InputDigest = rawMessagesDigest(input)
	receipt.LogicalItems = len(input)
	receipt.TransportItems = len(input)
	receipt.StablePrefixDigest = rawMessagesDigest(session.prefix)
	receipt.LogicalTransportEquivalent = true
	switch {
	case request.Projection.Retry:
		receipt.FallbackReason = provider.ProjectionFallbackRetry
	case request.Projection.RecoveryID != "" &&
		request.Projection.RecoveryID != session.recoveryID:
		receipt.FallbackReason = provider.ProjectionFallbackResume
	case session.previous == "":
		receipt.FallbackReason = provider.ProjectionFallbackNoCommittedResponse
	case session.routeDigest != receipt.RouteDigest:
		receipt.FallbackReason = provider.ProjectionFallbackRouteChanged
	case session.windowID != "" &&
		request.Projection.WindowID != "" &&
		session.windowID != request.Projection.WindowID:
		receipt.FallbackReason = provider.ProjectionFallbackCompaction
	case session.property != property:
		receipt.FallbackReason = provider.ProjectionFallbackPropertyChanged
	case !strictExtension(session.prefix, input):
		receipt.FallbackReason = provider.ProjectionFallbackHistoryRebased
	default:
		receipt.Mode = provider.ProjectionModeIncrementalSession
		receipt.IncrementalEligible = true
		receipt.FallbackReason = ""
	}
	return receipt
}

func rawMessagesEqual(left, right []json.RawMessage) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !jsonEqual(left[index], right[index]) {
			return false
		}
	}
	return true
}

func projectTransportInput(
	session *responsesSession,
	logical []json.RawMessage,
	receipt provider.ProjectionReceipt,
) ([]json.RawMessage, provider.ProjectionReceipt, error) {
	transport := logical
	reconstructed := logical
	if receipt.IncrementalEligible {
		if len(logical) < len(session.prefix) {
			return nil, receipt, errors.New(
				"incremental transport prefix exceeds logical input",
			)
		}
		transport = logical[len(session.prefix):]
		reconstructed = append(
			append([]json.RawMessage(nil), session.prefix...),
			transport...,
		)
	}
	receipt.TransportItems = len(transport)
	receipt.DeltaDigest = rawMessagesDigest(transport)
	receipt.LogicalTransportEquivalent = rawMessagesEqual(
		reconstructed,
		logical,
	)
	if !receipt.LogicalTransportEquivalent {
		return nil, receipt, errors.New(
			"incremental transport does not reconstruct logical input",
		)
	}
	return transport, receipt, nil
}
