package protocol

type EventClass string
type ItemOwner string
type Durability string
type CorrelationKind string

const (
	EventClassLifecycle         EventClass = "lifecycle"
	EventClassStream            EventClass = "stream"
	EventClassAudit             EventClass = "audit"
	EventClassEvidence          EventClass = "evidence"
	EventClassAccounting        EventClass = "accounting"
	EventClassTerminal          EventClass = "terminal"
	EventClassTerminalOperation EventClass = "terminal_operation"
	EventClassInteraction       EventClass = "interaction"
	EventClassArtifact          EventClass = "artifact"
	EventClassArtifactStream    EventClass = "artifact_stream"
	EventClassOrchestration     EventClass = "orchestration"
)

type EventTraits struct {
	Class       EventClass      `json:"class"`
	ItemOwner   ItemOwner       `json:"item_owner"`
	Durability  Durability      `json:"durability"`
	Correlation CorrelationKind `json:"correlation"`
	Terminal    bool            `json:"terminal"`
}

// Persisted reports whether the durable event log owns this event kind.
// Bounded stream payloads and terminal projections are retained by their
// dedicated owners rather than duplicated in events-v1.jsonl.
func (d Durability) Persisted() bool {
	switch d {
	case "retained", "atomic":
		return true
	case "bounded", "terminal_projection", "transient":
		return false
	default:
		// Unknown future durability values fail open at runtime. The manifest
		// generator and protocol tests still reject them before release.
		return true
	}
}

func Traits(kind EventKind) (EventTraits, bool) {
	traits, ok := eventTraits[kind]
	return traits, ok
}
