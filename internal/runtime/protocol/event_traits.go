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

func Traits(kind EventKind) (EventTraits, bool) {
	traits, ok := eventTraits[kind]
	return traits, ok
}
