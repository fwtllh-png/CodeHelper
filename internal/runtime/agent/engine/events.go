package engine

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type State string

type Event struct {
	State State
	Turn  uint64
	Data  []protocol.EventData
	Audit EventAudit
}

// EventAudit carries observations used to build the terminal receipt but not
// exposed as standalone Runtime events.
type EventAudit struct {
	Purpose        string
	ProviderRetry  *ProviderRetry
	ModelExecution *ModelExecution
	ToolResult     *tool.Result
	Verification   *VerificationReceipt
	Completion     *tool.CompletionDeclaration
	Compaction     *CompactionReceipt
	ContextBudget  *ContextBudgetSnapshot
}

type ModelExecution struct {
	Kind     string `json:"kind"`
	SampleID string `json:"sample_id"`
	Attempt  uint32 `json:"attempt,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ProviderRetry = providerwire.RetryDecision

// TerminalIssue is a cleanup/finalization failure that happened after the
// primary turn outcome was already known.
type TerminalIssue struct {
	Phase   string             `json:"phase"`
	Code    protocol.ErrorCode `json:"code"`
	Message string             `json:"message"`
}

type MCPHealthSnapshot = mcp.HealthSnapshot

type CompactionReceipt = promptcontext.CompactionReceipt

const (
	CompactionPhasePreSampling = "pre_sampling"
	CompactionPhaseMidTurn     = "mid_turn"
	CompactionPhasePostTurn    = "post_turn"
)

// ContextBudgetSnapshot freezes the exact retained context visible when a
// terminal event is emitted. Receipts consume this snapshot instead of racing
// a later read of mutable engine history.
type ContextBudgetSnapshot = agentcontext.BudgetSnapshot

type Budget struct {
	MaxTokens     uint64
	MaxTurnTokens uint64
	MaxCostUSD    float64
}

type CompactWindowPolicy = agentcontext.WindowPolicy

type TokenEstimator interface {
	Estimate([]provider.Message) (uint64, error)
}

type HeuristicTokenEstimator struct{}

func (HeuristicTokenEstimator) Estimate(messages []provider.Message) (uint64, error) {
	return agentcontext.EstimateMessageTokens(messages), nil
}

func (HeuristicTokenEstimator) EstimateImage(
	attachment provider.Attachment,
) (uint64, error) {
	return agentcontext.EstimateImageTokens(attachment), nil
}

type Result struct {
	Turn         uint64                  `json:"turn"`
	Text         string                  `json:"text"`
	Reasoning    string                  `json:"reasoning,omitempty"`
	State        State                   `json:"state"`
	Usage        provider.Usage          `json:"usage"`
	CostUSD      float64                 `json:"cost_usd"`
	Tools        []provider.ToolCall     `json:"tools,omitempty"`
	Searches     []provider.SearchResult `json:"searches,omitempty"`
	Citations    []provider.Citation     `json:"citations,omitempty"`
	Verification *VerificationReceipt    `json:"verification,omitempty"`
}

// PendingSource tags why an input was enqueued into the turn-local queue (N1).
type PendingSource string

const (
	PendingSteer   PendingSource = "steer"
	PendingMailbox PendingSource = "mailbox"
)

// PendingInput is one typed pending-work item drained into the active turn history.
type PendingInput struct {
	Source      PendingSource
	Prompt      string
	TriggerTurn bool // mailbox only; non-trigger stays in mailboxHold until the next turn
}

// BudgetSnapshot is the pool this engine draws from: what previous turns spent
// and the ceilings they were spent against. A zero ceiling is no ceiling.
//
// It counts finished turns only, because a turn's own usage is folded into the
// pool when it completes. A caller reporting "remaining" for the turn in flight
// has to add that turn's own spend, which it has and the engine does not.
//
// Child agents and background turns run their own engines and therefore their
// own pools; this reports one pool and never merges them.
func (e *Engine) BudgetSnapshot() BudgetSnapshot {
	if e == nil {
		return BudgetSnapshot{}
	}

	return BudgetSnapshot{
		TokensUsed: e.usage.Total(), MaxTokens: e.options.Budget.MaxTokens,
		CostUSD: e.costUSD, MaxCostUSD: e.options.Budget.MaxCostUSD,
	}
}

// BudgetSnapshot is what a host needs to show how much budget is left without
// recomputing the engine's accounting for itself.
type BudgetSnapshot struct {
	TokensUsed uint64
	MaxTokens  uint64
	CostUSD    float64
	MaxCostUSD float64
}
