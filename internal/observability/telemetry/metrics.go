package telemetry

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type MetricSnapshot struct {
	OperationsSubmitted uint64 `json:"operations_submitted"`
	OperationsProcessed uint64 `json:"operations_processed"`
	EventsPublished     uint64 `json:"events_published"`
	SubscribersDropped  uint64 `json:"subscribers_dropped"`
	ProviderRequests    uint64 `json:"provider_requests"`
	AgentTurns          uint64 `json:"agent_turns"`
	ToolExecutions      uint64 `json:"tool_executions"`
	Errors              uint64 `json:"errors"`
	// RepoIndexState is how the repository symbol index was configured for the
	// session: a reader needs it to tell a run with no symbol tools from one whose
	// index broke and fell back to text search.
	RepoIndexState string `json:"repo_index_state,omitempty"`
	// ContextTailBytes totals the repository map and working set bytes sent with
	// requests, and ContextTailTruncations counts the sections a budget cut. The
	// tail rides on every sample, so these are what tell an operator whether the
	// ceilings are set sensibly for their repository.
	ContextTailBytes       uint64 `json:"context_tail_bytes,omitempty"`
	ContextTailTruncations uint64 `json:"context_tail_truncations,omitempty"`
	// EvidenceRisks counts the unproved changes reported to the model, and
	// PolicyReminders the wasteful call patterns. Both are per report rather than
	// per distinct path, so they measure how often the agent had to be told
	// something — which is the point of watching them.
	EvidenceRisks   uint64 `json:"evidence_risks,omitempty"`
	PolicyReminders uint64 `json:"policy_reminders,omitempty"`
	// Compactions counts how many times a thread's history was replaced by a
	// summary, and CompactionSavedBytes the history bytes that replacement
	// removed. Together they say whether compaction is earning its complexity: a
	// thread that compacts often while saving little is one whose budgets are
	// wrong.
	Compactions          uint64 `json:"compactions,omitempty"`
	CompactionSavedBytes uint64 `json:"compaction_saved_bytes,omitempty"`
	// TurnKernelTransitions counts Coordinator-committed transitions. Drifts are
	// rejected transitions or terminal decisions that disagree with the
	// committed Kernel state. DigestErrors mean the transition state could not be
	// validated and hashed, so Replay comparison is not trustworthy.
	TurnKernelTransitions         uint64 `json:"turn_kernel_transitions,omitempty"`
	TurnKernelDrifts              uint64 `json:"turn_kernel_drifts,omitempty"`
	TurnKernelDigestErrors        uint64 `json:"turn_kernel_digest_errors,omitempty"`
	AgentSpawns                   uint64 `json:"agent_spawns,omitempty"`
	AgentCompleted                uint64 `json:"agent_completed,omitempty"`
	AgentFailed                   uint64 `json:"agent_failed,omitempty"`
	AgentInterrupted              uint64 `json:"agent_interrupted,omitempty"`
	AgentCompletionLatencyMS      uint64 `json:"agent_completion_latency_ms,omitempty"`
	AgentCompletionLatencySamples uint64 `json:"agent_completion_latency_samples,omitempty"`
	AgentIntegrationsApplied      uint64 `json:"agent_integrations_applied,omitempty"`
	AgentIntegrationsFailed       uint64 `json:"agent_integrations_failed,omitempty"`
	AgentIntegrationsDiscarded    uint64 `json:"agent_integrations_discarded,omitempty"`
	AgentCostMicrounits           uint64 `json:"agent_cost_microunits,omitempty"`
	AgentCostKnownSamples         uint64 `json:"agent_cost_known_samples,omitempty"`
	AgentCostUnknownSamples       uint64 `json:"agent_cost_unknown_samples,omitempty"`
}

type Metrics struct {
	operationsSubmitted           atomic.Uint64
	operationsProcessed           atomic.Uint64
	eventsPublished               atomic.Uint64
	subscribersDropped            atomic.Uint64
	providerRequests              atomic.Uint64
	agentTurns                    atomic.Uint64
	toolExecutions                atomic.Uint64
	errors                        atomic.Uint64
	repoIndexState                atomic.Pointer[string]
	contextTailBytes              atomic.Uint64
	contextTailCuts               atomic.Uint64
	evidenceRisks                 atomic.Uint64
	policyReminders               atomic.Uint64
	compactions                   atomic.Uint64
	compactionSaved               atomic.Uint64
	turnKernelTransitions         atomic.Uint64
	turnKernelDrifts              atomic.Uint64
	turnKernelDigestErrors        atomic.Uint64
	agentSpawns                   atomic.Uint64
	agentCompleted                atomic.Uint64
	agentFailed                   atomic.Uint64
	agentInterrupted              atomic.Uint64
	agentCompletionLatencyMS      atomic.Uint64
	agentCompletionLatencySamples atomic.Uint64
	agentIntegrationsApplied      atomic.Uint64
	agentIntegrationsFailed       atomic.Uint64
	agentIntegrationsDiscarded    atomic.Uint64
	agentCostMicrounits           atomic.Uint64
	agentCostKnownSamples         atomic.Uint64
	agentCostUnknownSamples       atomic.Uint64
	agentStarted                  sync.Map
}

func NewMetrics() *Metrics {
	return &Metrics{}
}

func (m *Metrics) OperationSubmitted() {
	if m != nil {
		m.operationsSubmitted.Add(1)
	}
}

func (m *Metrics) OperationProcessed() {
	if m != nil {
		m.operationsProcessed.Add(1)
	}
}

func (m *Metrics) EventPublished() {
	if m != nil {
		m.eventsPublished.Add(1)
	}
}

func (m *Metrics) SubscriberDropped() {
	if m != nil {
		m.subscribersDropped.Add(1)
	}
}

func (m *Metrics) ProviderRequest() {
	if m != nil {
		m.providerRequests.Add(1)
	}
}

func (m *Metrics) AgentTurn() {
	if m != nil {
		m.agentTurns.Add(1)
	}
}

func (m *Metrics) ToolExecution() {
	if m != nil {
		m.toolExecutions.Add(1)
	}
}

func (m *Metrics) Error() {
	if m != nil {
		m.errors.Add(1)
	}
}

// ContextTail records one rendered volatile section: how many bytes it sent and
// whether a budget cut it.
func (m *Metrics) ContextTail(bytes int, truncated bool) {
	if m == nil {
		return
	}
	if bytes > 0 {
		m.contextTailBytes.Add(uint64(bytes))
	}
	if truncated {
		m.contextTailCuts.Add(1)
	}
}

// Evidence records one reported evidence section: how many unproved changes and
// wasteful call patterns the model was told about.
func (m *Metrics) Evidence(risks, reminders int) {
	if m == nil {
		return
	}
	if risks > 0 {
		m.evidenceRisks.Add(uint64(risks))
	}
	if reminders > 0 {
		m.policyReminders.Add(uint64(reminders))
	}
}

// Compaction records one history replacement and the bytes it saved.
func (m *Metrics) Compaction(savedBytes int) {
	if m == nil {
		return
	}
	m.compactions.Add(1)
	if savedBytes > 0 {
		m.compactionSaved.Add(uint64(savedBytes))
	}
}

func (m *Metrics) TurnKernelObserver(drift, digestError bool) {
	if m == nil {
		return
	}
	m.turnKernelTransitions.Add(1)
	if drift {
		m.turnKernelDrifts.Add(1)
	}
	if digestError {
		m.turnKernelDigestErrors.Add(1)
	}
}

// AgentSpawn records one admitted Child and starts its completion clock.
func (m *Metrics) AgentSpawn(agentID string, at time.Time) {
	if m == nil {
		return
	}
	m.agentSpawns.Add(1)
	if agentID != "" {
		m.agentStarted.Store(agentID, at)
	}
}

// AgentStatus records release-facing terminal outcomes and Child spend.
func (m *Metrics) AgentStatus(
	agentID, status string,
	at time.Time,
	costMicrounits uint64,
	costKnown bool,
) {
	if m == nil {
		return
	}
	switch status {
	case "completed":
		m.agentCompleted.Add(1)
	case "failed":
		m.agentFailed.Add(1)
	case "interrupted":
		m.agentInterrupted.Add(1)
	default:
		return
	}
	if started, ok := m.agentStarted.LoadAndDelete(agentID); ok {
		if began, valid := started.(time.Time); valid && !at.Before(began) {
			m.agentCompletionLatencyMS.Add(uint64(at.Sub(began).Milliseconds()))
			m.agentCompletionLatencySamples.Add(1)
		}
	}
	if costKnown {
		m.agentCostMicrounits.Add(costMicrounits)
		m.agentCostKnownSamples.Add(1)
	} else {
		m.agentCostUnknownSamples.Add(1)
	}
}

// AgentIntegration records terminal Integration Candidate outcomes.
func (m *Metrics) AgentIntegration(status string) {
	if m == nil {
		return
	}
	switch status {
	case "applied":
		m.agentIntegrationsApplied.Add(1)
	case "failed":
		m.agentIntegrationsFailed.Add(1)
	case "discarded":
		m.agentIntegrationsDiscarded.Add(1)
	}
}

// ObserveAgentEvent derives rollout metrics from successfully published facts.
func (m *Metrics) ObserveAgentEvent(data protocol.EventData, at time.Time) {
	if spawned, ok := data.(*protocol.AgentSpawnedData); ok {
		m.AgentSpawn(spawned.AgentID, at)
		return
	}
	if status, ok := data.(*protocol.AgentStatusData); ok {
		cost, known := agentStatusCost(status.Detail)
		m.AgentStatus(status.AgentID, status.Status, at, cost, known)
		return
	}
	if integration, ok := data.(*protocol.AgentIntegrationData); ok {
		m.AgentIntegration(integration.Status)
	}
}

func agentStatusCost(detail json.RawMessage) (uint64, bool) {
	if len(detail) == 0 {
		return 0, false
	}
	type usage struct {
		CostMicrounits uint64 `json:"cost_microunits"`
		CostKnown      bool   `json:"cost_known"`
	}
	var transition struct {
		Result *struct {
			Usage usage `json:"usage"`
		} `json:"result"`
		Completion *struct {
			Usage usage `json:"usage"`
		} `json:"completion"`
	}
	if json.Unmarshal(detail, &transition) != nil {
		return 0, false
	}
	if transition.Result != nil {
		return transition.Result.Usage.CostMicrounits, transition.Result.Usage.CostKnown
	}
	if transition.Completion != nil {
		return transition.Completion.Usage.CostMicrounits,
			transition.Completion.Usage.CostKnown
	}
	return 0, false
}

// SetRepositoryIndexState records the state of the repository symbol index.
func (m *Metrics) SetRepositoryIndexState(state string) {
	if m != nil {
		m.repoIndexState.Store(&state)
	}
}

func (m *Metrics) Snapshot() MetricSnapshot {
	if m == nil {
		return MetricSnapshot{}
	}
	indexState := ""
	if state := m.repoIndexState.Load(); state != nil {
		indexState = *state
	}
	return MetricSnapshot{
		RepoIndexState:                indexState,
		ContextTailBytes:              m.contextTailBytes.Load(),
		ContextTailTruncations:        m.contextTailCuts.Load(),
		EvidenceRisks:                 m.evidenceRisks.Load(),
		PolicyReminders:               m.policyReminders.Load(),
		Compactions:                   m.compactions.Load(),
		CompactionSavedBytes:          m.compactionSaved.Load(),
		TurnKernelTransitions:         m.turnKernelTransitions.Load(),
		TurnKernelDrifts:              m.turnKernelDrifts.Load(),
		TurnKernelDigestErrors:        m.turnKernelDigestErrors.Load(),
		AgentSpawns:                   m.agentSpawns.Load(),
		AgentCompleted:                m.agentCompleted.Load(),
		AgentFailed:                   m.agentFailed.Load(),
		AgentInterrupted:              m.agentInterrupted.Load(),
		AgentCompletionLatencyMS:      m.agentCompletionLatencyMS.Load(),
		AgentCompletionLatencySamples: m.agentCompletionLatencySamples.Load(),
		AgentIntegrationsApplied:      m.agentIntegrationsApplied.Load(),
		AgentIntegrationsFailed:       m.agentIntegrationsFailed.Load(),
		AgentIntegrationsDiscarded:    m.agentIntegrationsDiscarded.Load(),
		AgentCostMicrounits:           m.agentCostMicrounits.Load(),
		AgentCostKnownSamples:         m.agentCostKnownSamples.Load(),
		AgentCostUnknownSamples:       m.agentCostUnknownSamples.Load(),

		OperationsSubmitted: m.operationsSubmitted.Load(),
		OperationsProcessed: m.operationsProcessed.Load(),
		EventsPublished:     m.eventsPublished.Load(),
		SubscribersDropped:  m.subscribersDropped.Load(),
		ProviderRequests:    m.providerRequests.Load(),
		AgentTurns:          m.agentTurns.Load(),
		ToolExecutions:      m.toolExecutions.Load(),
		Errors:              m.errors.Load(),
	}
}
