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

var eventTraits = map[EventKind]EventTraits{
	EventTurnStarted:        {EventClassLifecycle, "turn", "retained", "turn", false},
	EventOutputDelta:        {EventClassStream, "turn", "terminal_projection", "turn", false},
	EventReasoningDelta:     {EventClassStream, "turn", "retained", "turn", false},
	EventReasoningSignature: {EventClassAudit, "turn", "retained", "turn", false},
	EventSearchResult:       {EventClassEvidence, "turn", "retained", "turn", false},
	EventCitation:           {EventClassEvidence, "turn", "retained", "turn", false},
	EventUsage:              {EventClassAccounting, "turn", "retained", "sample", false},
	EventToolState:          {EventClassLifecycle, "turn", "transient", "turn", false},
	EventToolStart:          {EventClassLifecycle, "tool", "retained", "call", false},
	EventToolOutput:         {EventClassStream, "tool", "bounded", "call", false},
	EventToolResult:         {EventClassLifecycle, "tool", "retained", "call", false},
	EventToolCatalogChanged: {EventClassAudit, "turn", "retained", "catalog", false},
	EventMCPHealthChanged:   {EventClassAudit, "turn", "retained", "server", false},
	EventExtensionLifecycle: {EventClassAudit, "turn", "retained", "extension", false},
	EventDiagnostics:        {EventClassEvidence, "turn", "retained", "turn", false},
	EventTurnCompleted:      {EventClassTerminal, "turn", "atomic", "turn", true},
	EventTurnFailed:         {EventClassTerminal, "turn", "atomic", "turn", true},
	EventTurnCanceled:       {EventClassTerminal, "turn", "atomic", "turn", true},
	EventOperationRejected:  {EventClassTerminalOperation, "operation", "retained", "operation", false},
	EventTurnSteered:        {EventClassInteraction, "turn", "retained", "turn", false},
	EventApprovalRequired:   {EventClassInteraction, "approval", "retained", "request", false},
	EventApprovalResolved:   {EventClassInteraction, "approval", "retained", "request", false},
	EventInputRequired:      {EventClassInteraction, "input", "retained", "request", false},
	EventInputResolved:      {EventClassInteraction, "input", "retained", "request", false},
	EventThreadCompacted:    {EventClassLifecycle, "thread", "retained", "thread", false},
	EventThreadForked:       {EventClassLifecycle, "thread", "retained", "thread", false},
	EventTurnReverted:       {EventClassLifecycle, "turn", "retained", "target_turn", false},
	EventCheckpointCreated:  {EventClassArtifact, "checkpoint", "retained", "checkpoint", false},
	EventCheckpointRestored: {EventClassArtifact, "checkpoint", "retained", "checkpoint", false},
	EventCheckpointForked:   {EventClassArtifact, "checkpoint", "retained", "checkpoint", false},
	EventTurnCompaction:     {EventClassAudit, "turn", "retained", "turn", false},
	EventAgentSpawned:       {EventClassOrchestration, "agent", "retained", "agent", false},
	EventAgentStatus:        {EventClassOrchestration, "agent", "retained", "agent", false},
	EventAgentMessage:       {EventClassOrchestration, "agent", "retained", "agent", false},
	EventPlanDelta:          {EventClassArtifactStream, "turn", "retained", "plan", false},
	EventCommandExecution:   {EventClassAudit, "tool", "retained", "call", false},
	EventHostCommand:        {EventClassInteraction, "turn", "retained", "command", false},
	EventExecutionReceipt:   {EventClassEvidence, "turn", "atomic", "turn", false},
	EventTurnVerification:   {EventClassEvidence, "turn", "retained", "mutation", false},
}

func Traits(kind EventKind) (EventTraits, bool) {
	traits, ok := eventTraits[kind]
	return traits, ok
}
