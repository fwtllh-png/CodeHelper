package provider

// ProjectionContext is Runtime-owned continuity attached to one complete
// logical request. Provider adapters may use it to prove that an incremental
// transport is safe, but it never enters the provider payload.
type ProjectionContext struct {
	ContextRevision uint64
	WindowID        string
	WindowNumber    uint64
	Retry           bool
	RecoveryID      string
}

type ProjectionMode string

const (
	ProjectionModeFullHTTP           ProjectionMode = "complete_http_sse"
	ProjectionModeFullSession        ProjectionMode = "complete_session"
	ProjectionModeIncrementalSession ProjectionMode = "incremental_session"
)

type ProjectionFallbackReason string

const (
	ProjectionFallbackCompleteRequest     ProjectionFallbackReason = "complete_request"
	ProjectionFallbackNoCommittedResponse ProjectionFallbackReason = "no_committed_response"
	ProjectionFallbackCapabilityDisabled  ProjectionFallbackReason = "incremental_capability_disabled"
	ProjectionFallbackProtocolUnsupported ProjectionFallbackReason = "incremental_protocol_unsupported"
	ProjectionFallbackCacheKeyMissing     ProjectionFallbackReason = "prompt_cache_key_missing"
	ProjectionFallbackSessionUnavailable  ProjectionFallbackReason = "session_transport_unavailable"
	ProjectionFallbackConnectionReset     ProjectionFallbackReason = "session_connection_reset"
	ProjectionFallbackRouteChanged        ProjectionFallbackReason = "route_changed"
	ProjectionFallbackRetry               ProjectionFallbackReason = "provider_retry"
	ProjectionFallbackCompaction          ProjectionFallbackReason = "context_compacted"
	ProjectionFallbackResume              ProjectionFallbackReason = "turn_resumed"
	ProjectionFallbackPropertyChanged     ProjectionFallbackReason = "request_properties_changed"
	ProjectionFallbackHistoryRebased      ProjectionFallbackReason = "logical_history_rebased"
)

// ProjectionReceipt explains one logical-request to transport projection using
// only counts, booleans, and content-safe digests.
type ProjectionReceipt struct {
	Mode                       ProjectionMode
	IncrementalEligible        bool
	FallbackReason             ProjectionFallbackReason
	RouteDigest                string
	PropertyDigest             string
	StablePrefixDigest         string
	InputDigest                string
	DeltaDigest                string
	ContextRevision            uint64
	WindowID                   string
	WindowNumber               uint64
	LogicalItems               int
	TransportItems             int
	LogicalTransportEquivalent bool
}

func CompleteProjection(
	context ProjectionContext,
	reason ProjectionFallbackReason,
) ProjectionReceipt {
	if reason == "" {
		reason = ProjectionFallbackCompleteRequest
	}
	return ProjectionReceipt{
		Mode:                       ProjectionModeFullHTTP,
		FallbackReason:             reason,
		ContextRevision:            context.ContextRevision,
		WindowID:                   context.WindowID,
		WindowNumber:               context.WindowNumber,
		LogicalTransportEquivalent: true,
	}
}
