package telemetry

import (
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type MetricSnapshot struct {
	OperationsSubmitted           uint64 `json:"operations_submitted"`
	OperationsProcessed           uint64 `json:"operations_processed"`
	EventsPublished               uint64 `json:"events_published"`
	SubscribersDropped            uint64 `json:"subscribers_dropped"`
	ProviderRequests              uint64 `json:"provider_requests"`
	AgentTurns                    uint64 `json:"agent_turns"`
	ToolExecutions                uint64 `json:"tool_executions"`
	Errors                        uint64 `json:"errors"`
	RepoIndexState                string `json:"repo_index_state,omitempty"`
	ContextTailBytes              uint64 `json:"context_tail_bytes,omitempty"`
	ContextTailTruncations        uint64 `json:"context_tail_truncations,omitempty"`
	EvidenceRisks                 uint64 `json:"evidence_risks,omitempty"`
	PolicyReminders               uint64 `json:"policy_reminders,omitempty"`
	Compactions                   uint64 `json:"compactions,omitempty"`
	CompactionSavedBytes          uint64 `json:"compaction_saved_bytes,omitempty"`
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
	ApprovalEvaluatedTotal        uint64 `json:"approval_evaluated_total,omitempty"`
	ApprovalAutoAllowedTotal      uint64 `json:"approval_auto_allowed_total,omitempty"`
	ApprovalHumanRequiredTotal    uint64 `json:"approval_human_required_total,omitempty"`
	ApprovalDeniedTotal           uint64 `json:"approval_denied_total,omitempty"`
	ApprovalGrantHitTotal         uint64 `json:"approval_grant_hit_total,omitempty"`
	ApprovalReviewerLatencyMS     uint64 `json:"approval_reviewer_latency_ms,omitempty"`
	ApprovalWaitLatencyMS         uint64 `json:"approval_wait_latency_ms,omitempty"`
}

type Metrics struct {
	operationsSubmitted, operationsProcessed, eventsPublished       atomic.Uint64
	subscribersDropped, providerRequests, agentTurns                atomic.Uint64
	toolExecutions, errors, contextTailBytes, contextTailCuts       atomic.Uint64
	evidenceRisks, policyReminders, compactions, compactionSaved    atomic.Uint64
	turnKernelTransitions, turnKernelDrifts, turnKernelDigestErrors atomic.Uint64
	agentSpawns, agentCompleted, agentFailed, agentInterrupted      atomic.Uint64
	agentCompletionLatencyMS, agentCompletionLatencySamples         atomic.Uint64
	agentIntegrationsApplied, agentIntegrationsFailed               atomic.Uint64
	agentIntegrationsDiscarded, agentCostMicrounits                 atomic.Uint64
	agentCostKnownSamples, agentCostUnknownSamples                  atomic.Uint64
	approvalEvaluated, approvalAutoAllowed, approvalHumanRequired   atomic.Uint64
	approvalDenied, approvalGrantHits, approvalReviewerLatencyMS    atomic.Uint64
	approvalWaitLatencyMS                                           atomic.Uint64
	repoIndexState                                                  atomic.Pointer[string]
	agentStarted                                                    sync.Map
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

func (m *Metrics) Approval(
	outcome, effect, risk, reasonCode string,
	latency time.Duration,
) {
	if m == nil || effect == "" || risk == "" || len(reasonCode) > 64 {
		return
	}
	switch outcome {
	case "evaluated":
		m.approvalEvaluated.Add(1)
	case "auto_allowed":
		m.approvalAutoAllowed.Add(1)
		m.approvalReviewerLatencyMS.Add(uint64(max(0, latency.Milliseconds())))
	case "human_required":
		m.approvalHumanRequired.Add(1)
		m.approvalReviewerLatencyMS.Add(uint64(max(0, latency.Milliseconds())))
	case "denied":
		m.approvalDenied.Add(1)
	case "grant_hit":
		m.approvalGrantHits.Add(1)
	case "waited":
		m.approvalWaitLatencyMS.Add(uint64(max(0, latency.Milliseconds())))
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
		ApprovalEvaluatedTotal:        m.approvalEvaluated.Load(),
		ApprovalAutoAllowedTotal:      m.approvalAutoAllowed.Load(),
		ApprovalHumanRequiredTotal:    m.approvalHumanRequired.Load(),
		ApprovalDeniedTotal:           m.approvalDenied.Load(),
		ApprovalGrantHitTotal:         m.approvalGrantHits.Load(),
		ApprovalReviewerLatencyMS:     m.approvalReviewerLatencyMS.Load(),
		ApprovalWaitLatencyMS:         m.approvalWaitLatencyMS.Load(),

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
