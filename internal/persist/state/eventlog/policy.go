package eventlog

import "github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"

// ShouldPersist reports whether a protocol event kind belongs on the durable
// eventlog. Live Runtime subscribers still receive every event; only
// persist/state.Store.Append applies this filter.
//
// Dropped kinds are streaming / mid-turn noise. Unknown future kinds default
// to persist (fail-open) so new audit events are not silently lost.
func ShouldPersist(kind protocol.EventKind) bool {
	switch kind {
	case protocol.EventOutputDelta,
		protocol.EventReasoningDelta,
		protocol.EventToolState,
		// A tool's live output is a running commentary on a call whose full output
		// is persisted with tool.result. Keeping the chunks too would multiply the
		// same bytes across the log for no audit value.
		protocol.EventToolOutput,
		protocol.EventTurnCompaction:
		return false
	default:
		return true
	}
}
