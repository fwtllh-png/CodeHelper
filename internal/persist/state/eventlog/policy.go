package eventlog

import "github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"

// ShouldPersist reports whether a protocol event kind belongs on the durable
// eventlog. Live Runtime subscribers still receive every event; only
// persist/state.Store.Append applies this filter.
//
// Durability is declared once in protocol Event Traits. Unknown future kinds
// default to persist (fail-open) so new audit events are not silently lost.
func ShouldPersist(kind protocol.EventKind) bool {
	traits, ok := protocol.Traits(kind)
	if !ok {
		return true
	}
	return traits.Durability.Persisted()
}
