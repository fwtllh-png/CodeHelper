package engine

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type State string

type Event struct {
	State    State  `json:"state"`
	Turn     uint64 `json:"turn"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Purpose is which route the turn's samples go to, and so why this provider
	// and model rather than the session's default pair.
	Purpose            string                 `json:"purpose,omitempty"`
	Mode               string                 `json:"mode,omitempty"`
	Posture            string                 `json:"posture,omitempty"`
	Workspace          string                 `json:"workspace,omitempty"`
	WorkspaceIsolation string                 `json:"workspace_isolation,omitempty"`
	Sandbox            string                 `json:"sandbox,omitempty"`
	Text               string                 `json:"text,omitempty"`
	Block              *provider.ContentBlock `json:"block,omitempty"`
	ToolCall           *provider.ToolCall     `json:"tool_call,omitempty"`
	Result             *tool.Result           `json:"result,omitempty"`
	Search             *provider.SearchResult `json:"search,omitempty"`
	Citation           *provider.Citation     `json:"citation,omitempty"`
	Usage              *provider.Usage        `json:"usage,omitempty"`
	CostUSD            float64                `json:"cost_usd,omitempty"`
	// CostKnown reports whether the model has pricing at all, so a consumer can
	// tell a free call from an unpriced one instead of reading both as zero.
	CostKnown bool `json:"cost_known,omitempty"`
	// Sample is which provider call within the turn a usage report belongs to.
	// Usage is cumulative within a sample, so a consumer keeps the last report
	// per sample rather than adding them up.
	Sample             uint32                      `json:"sample,omitempty"`
	SampleContext      *protocol.SampleContextData `json:"sample_context,omitempty"`
	ErrorCode          protocol.ErrorCode          `json:"error_code,omitempty"`
	Error              string                      `json:"error,omitempty"`
	CancelReason       string                      `json:"cancel_reason,omitempty"`
	SecondaryIssues    []TerminalIssue             `json:"secondary_issues,omitempty"`
	Compaction         *CompactionReceipt          `json:"compaction,omitempty"`
	ContextBudget      *ContextBudgetSnapshot      `json:"context_budget,omitempty"`
	Approval           *toolguard.ApprovalRequest  `json:"approval,omitempty"`
	Input              *interact.Request           `json:"input,omitempty"`
	Diagnostics        []diagnostics.Receipt       `json:"diagnostics,omitempty"`
	FileChanges        []toolguard.FileChange      `json:"file_changes,omitempty"`
	Plan               *ProposedPlanUpdate         `json:"plan,omitempty"`
	Verification       *VerificationReceipt        `json:"verification,omitempty"`
	Completion         *tool.CompletionDeclaration `json:"completion,omitempty"`
	ProviderRetry      *ProviderRetry              `json:"provider_retry,omitempty"`
	ModelExecution     *ModelExecution             `json:"model_execution,omitempty"`
	ToolOutput         *ToolOutput                 `json:"tool_output,omitempty"`
	CatalogChanged     *CatalogChanged             `json:"catalog_changed,omitempty"`
	MCPHealthChanged   *MCPHealthChanged           `json:"mcp_health_changed,omitempty"`
	ExtensionLifecycle *ExtensionLifecycleChanged  `json:"extension_lifecycle,omitempty"`
}

type ModelExecution struct {
	Kind     string `json:"kind"`
	SampleID string `json:"sample_id"`
	Attempt  uint32 `json:"attempt,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type ProviderRetry struct {
	Attempt        int                `json:"attempt"`
	Retry          uint32             `json:"retry"`
	Code           protocol.ErrorCode `json:"code"`
	Category       string             `json:"category"`
	Failure        provider.Failure   `json:"failure"`
	EffectiveDelay time.Duration      `json:"effective_delay"`
	RetryAt        time.Time          `json:"retry_at"`
	PolicyRevision string             `json:"policy_revision"`
}

// TerminalIssue is a cleanup/finalization failure that happened after the
// primary turn outcome was already known.
type TerminalIssue struct {
	Phase   string             `json:"phase"`
	Code    protocol.ErrorCode `json:"code"`
	Message string             `json:"message"`
}

type CatalogChanged struct {
	CatalogID  string
	Generation uint64
	Digest     string
	Added      []tool.CatalogChange
	Replaced   []tool.CatalogChange
	Revoked    []tool.CatalogChange
}

type MCPHealthSnapshot struct {
	Server              string
	State               string
	ConsecutiveFailures int
	LastError           string
	ChangedAt           time.Time
	RetryAt             time.Time
}

type MCPHealthChanged struct {
	PreviousState string
	Current       MCPHealthSnapshot
}

type ExtensionSnapshot struct {
	Kind       string
	Name       string
	Version    string
	Source     string
	Publisher  string
	Trust      string
	Digest     string
	Generation uint64
	Enabled    bool
	LastAction string
	ChangedAt  time.Time
}

type ExtensionLifecycleChanged struct {
	Action          string
	PreviousVersion string
	Current         ExtensionSnapshot
}

// ToolOutput is one piece of a tool's output, delivered while the tool is still
// running. It exists because a command that takes a minute used to produce nothing
// observable until it finished.
type ToolOutput struct {
	Tool   string `json:"tool"`
	CallID string `json:"call_id"`
	Stream string `json:"stream"`
	Chunk  string `json:"chunk"`
	// Cursor is the byte count of this stream through the end of this chunk, so a
	// consumer can tell that it missed something.
	Cursor uint64 `json:"cursor"`
	// Truncated marks the last chunk a call streams once it has spent its
	// streaming budget. The full output still arrives with the tool result; what
	// stops is the live commentary.
	Truncated bool `json:"truncated,omitempty"`
}

type CompactionReceipt struct {
	Phase                string `json:"phase,omitempty"`
	OriginalMessages     int    `json:"original_messages"`
	RemovedMessages      int    `json:"removed_messages"`
	OriginalBytes        int    `json:"original_bytes"`
	RetainedBytes        int    `json:"retained_bytes"`
	OriginalTokens       uint64 `json:"original_tokens"`
	RetainedTokens       uint64 `json:"retained_tokens"`
	SummaryOriginalBytes int    `json:"summary_original_bytes"`
	SummaryRetainedBytes int    `json:"summary_retained_bytes"`
	SummaryTruncated     bool   `json:"summary_truncated"`
	TruncationReason     string `json:"truncation_reason,omitempty"`
	// Sections names the parts of the summary that survived the budget, so a host
	// can tell a compaction that carried the goal from one that only had room for
	// a transcript.
	Sections              []string                `json:"sections,omitempty"`
	RemovedTurns          []uint64                `json:"removed_turns"`
	PromptContextReceipts []promptcontext.Receipt `json:"prompt_context_receipts"`
	WorkingSet            []string                `json:"working_set"`
	CriticalPaths         []string                `json:"critical_paths"`
}

const (
	CompactionPhasePreSampling = "pre_sampling"
	CompactionPhaseMidTurn     = "mid_turn"
	CompactionPhasePostTurn    = "post_turn"
)

// ContextBudgetSnapshot freezes the exact retained context visible when a
// terminal event is emitted. Receipts consume this snapshot instead of racing
// a later read of mutable engine history.
type ContextBudgetSnapshot struct {
	ActiveTokens      uint64 `json:"active_tokens"`
	AutoCompactTokens uint64 `json:"auto_compact_tokens"`
	EstimatedTokens   uint64 `json:"estimated_tokens,omitempty"`
	MaxContextTokens  uint64 `json:"max_context_tokens,omitempty"`
	Compactions       int    `json:"compactions"`
}

type Budget struct {
	MaxTokens  uint64
	MaxCostUSD float64
}

type CompactWindowPolicy struct {
	AutoTokens uint64
	Scope      string
}

type TokenEstimator interface {
	Estimate([]provider.Message) (uint64, error)
}

type HeuristicTokenEstimator struct{}

func (HeuristicTokenEstimator) Estimate(messages []provider.Message) (uint64, error) {
	return estimateMessageTokens(messages), nil
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
