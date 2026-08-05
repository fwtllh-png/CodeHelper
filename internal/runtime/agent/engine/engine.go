package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type State string

const (
	Preparing        State = "preparing"
	Compacting       State = "compacting"
	CallingModel     State = "calling_model"
	Streaming        State = "streaming"
	PreparingTools   State = "preparing_tools"
	RunningTools     State = "running_tools"
	AwaitingApproval State = "awaiting_approval"
	AwaitingInput    State = "awaiting_input"
	FeedingResults   State = "feeding_results"
	Verifying        State = "verifying"
	Completed        State = "completed"
	Failed           State = "failed"
	Canceled         State = "canceled"
)

type Event struct {
	State    State  `json:"state"`
	Turn     uint64 `json:"turn"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// Purpose is which route the turn's samples go to, and so why this provider
	// and model rather than the session's default pair.
	Purpose   string                 `json:"purpose,omitempty"`
	Mode      string                 `json:"mode,omitempty"`
	Posture   string                 `json:"posture,omitempty"`
	Workspace string                 `json:"workspace,omitempty"`
	Sandbox   string                 `json:"sandbox,omitempty"`
	Text      string                 `json:"text,omitempty"`
	Block     *provider.ContentBlock `json:"block,omitempty"`
	ToolCall  *provider.ToolCall     `json:"tool_call,omitempty"`
	Result    *tool.Result           `json:"result,omitempty"`
	Search    *provider.SearchResult `json:"search,omitempty"`
	Citation  *provider.Citation     `json:"citation,omitempty"`
	Usage     *provider.Usage        `json:"usage,omitempty"`
	CostUSD   float64                `json:"cost_usd,omitempty"`
	// CostKnown reports whether the model has pricing at all, so a consumer can
	// tell a free call from an unpriced one instead of reading both as zero.
	CostKnown bool `json:"cost_known,omitempty"`
	// Sample is which provider call within the turn a usage report belongs to.
	// Usage is cumulative within a sample, so a consumer keeps the last report
	// per sample rather than adding them up.
	Sample               uint32                     `json:"sample,omitempty"`
	EstimatedInputTokens uint64                     `json:"estimated_input_tokens,omitempty"`
	InputTokenDelta      int64                      `json:"input_token_delta,omitempty"`
	ErrorCode            protocol.ErrorCode         `json:"error_code,omitempty"`
	Error                string                     `json:"error,omitempty"`
	Compaction           *CompactionReceipt         `json:"compaction,omitempty"`
	Approval             *toolguard.ApprovalRequest `json:"approval,omitempty"`
	Input                *interact.Request          `json:"input,omitempty"`
	Diagnostics          []diagnostics.Receipt      `json:"diagnostics,omitempty"`
	Plan                 *ProposedPlanUpdate        `json:"plan,omitempty"`
	Verification         *VerificationReceipt       `json:"verification,omitempty"`
	ToolOutput           *ToolOutput                `json:"tool_output,omitempty"`
	CatalogChanged       *CatalogChanged            `json:"catalog_changed,omitempty"`
	MCPHealthChanged     *MCPHealthChanged          `json:"mcp_health_changed,omitempty"`
	ExtensionLifecycle   *ExtensionLifecycleChanged `json:"extension_lifecycle,omitempty"`
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
)

type Budget struct {
	MaxTokens  uint64
	MaxCostUSD float64
}

// BudgetReminderThresholdTokens triggers a one-shot model-visible reminder when
// remaining session tokens fall at or below this value (0 → 10% of MaxTokens).
type budgetReminderState struct {
	delivered bool
}

type TokenEstimator interface {
	Estimate([]provider.Message) (uint64, error)
}

type HeuristicTokenEstimator struct{}

func (HeuristicTokenEstimator) Estimate(messages []provider.Message) (uint64, error) {
	return estimateMessageTokens(messages), nil
}

type Options struct {
	Provider provider.Provider
	// Route is the act route: what a turn samples on unless its purpose has a
	// route of its own.
	Route model.ReadyRoute
	// Routes is the whole per-purpose table. It is optional, and a caller that
	// only has one model can leave it zero: New then builds a table from Route,
	// which resolves every purpose to it. When both are given, Routes wins and
	// Route is set from its act slot, so there is one source of truth afterwards.
	Routes          model.RouteSet
	Tools           *tool.Registry
	PromptContext   []provider.Message
	MaxOutputTokens uint64
	MaxSteps        int
	MaxRetries      int
	// MaxContextBytes is the history size that triggers a compaction.
	MaxContextBytes int
	// SummaryMaxBytes caps a rendered compaction summary. Zero derives it from
	// MaxContextBytes.
	SummaryMaxBytes int
	// MaxDigestEntries bounds the per-message running record a summary carries.
	MaxDigestEntries int
	ReasoningEffort  string
	NativeSearch     bool
	Budget           Budget
	// BudgetReminderThreshold is remaining tokens that trigger a one-shot reminder
	// (0 → max(256, MaxTokens/10)).
	BudgetReminderThreshold uint64
	TokenEstimator          TokenEstimator
	WorkingSet              []string
	CriticalPaths           []string
	ContextReceipts         []promptcontext.Receipt
	Authorize               func(provider.ToolCall) bool
	Security                *policy.Runtime
	Guard                   *toolguard.Guard
	// OnNetworkAllow is wired into a Guard that New allocates when Guard is
	// nil. Without it, mid-flight egress approvals update the approval cache
	// but never Grant the session Gate, so the retry still gets egress denied.
	OnNetworkAllow func(host, protocol string)
	Workspace      string
	Metrics        *telemetry.Metrics
	// Now is the clock every duration the turn reports is measured against
	// (nil → time.Now). One clock rather than several is what lets a test assert
	// a latency exactly.
	Now func() time.Time
	// Trace persists the turn's spans. A nil sink still leaves the spans
	// collected in memory, because the receipt's latency partition is read from
	// the same tree: a runtime without a database still reports how long its
	// turns took.
	Trace       trace.Sink
	Journal     *workspacejournal.Manager
	ReadTracker *workspacejournal.ReadTracker
	// WorkspaceTurnGate is shared only by engines intentionally operating on
	// the same writable root. Isolated worktree engines leave it nil.
	WorkspaceTurnGate *WorkspaceTurnGate
	Diagnostics       diagnostics.Runner
	Verify            VerifyOptions
	Hooks             *hooks.Manager
	SessionID         string
	InputHost         *interact.Host
	// PromptCacheKey is the session sticky hint; samples only attach it when
	// StickyPromptCacheKey drops the session default when the route lacks
	// prompt_cache, so Validate/encode stay consistent across protocols.
	PromptCacheKey string
	// MaxToolConcurrent bounds simultaneous concurrent-policy tools (0 → 8).
	MaxToolConcurrent int
	// MaxToolStreamBytes bounds how much of one tool call's output is delivered as
	// live chunks (0 → DefaultMaxToolStreamBytes). The tool result is unaffected.
	MaxToolStreamBytes int
	// ToolSearchThreshold enables tool_search when available tools ≥ N (0 → 24).
	ToolSearchThreshold int
	// MaxToolDefinitions and MaxToolSchemaBytes hard-bound each provider request.
	MaxToolDefinitions int
	MaxToolSchemaBytes int
	ToolCatalogBudget  promptcontext.Budget
	// ToolCatalogSync reconciles externally managed tools immediately before
	// each sampling snapshot. Background notifications remain an optimization;
	// a sync error must prevent a stale catalog from reaching the provider.
	ToolCatalogSync func() error
	// MCPHealthSnapshot returns one isolated status row per configured server.
	MCPHealthSnapshot func() []MCPHealthSnapshot
	// ExtensionSnapshot returns trusted extension identities. Changes are
	// projected at sampling boundaries, like MCP health and catalog changes.
	ExtensionSnapshot func() ([]ExtensionSnapshot, error)
	// SkillSnapshot returns the enabled skill catalog for turn freezing (N10).
	SkillSnapshot func() []SkillSummary
	// RepoContext renders the repository map and working set appended to every
	// request. A nil provider leaves the request as it was before (W1.2).
	RepoContext RepoContext
	// WorkingSetLimit bounds how many self-discovered paths the working set
	// reports; pinned paths are always reported (0 → 16).
	WorkingSetLimit int
	// EvidenceLimit bounds how many facts the evidence set reports per sample.
	// Risks and reminders are always reported: they are what the section is for
	// (0 → 24).
	EvidenceLimit int
}

type Result struct {
	Turn                 uint64                  `json:"turn"`
	Text                 string                  `json:"text"`
	Reasoning            string                  `json:"reasoning,omitempty"`
	ReasoningSignature   string                  `json:"reasoning_signature,omitempty"`
	State                State                   `json:"state"`
	Usage                provider.Usage          `json:"usage"`
	CostUSD              float64                 `json:"cost_usd"`
	EstimatedInputTokens uint64                  `json:"estimated_input_tokens"`
	InputTokenDelta      int64                   `json:"input_token_delta"`
	Tools                []provider.ToolCall     `json:"tools,omitempty"`
	Searches             []provider.SearchResult `json:"searches,omitempty"`
	Citations            []provider.Citation     `json:"citations,omitempty"`
	Verification         *VerificationReceipt    `json:"verification,omitempty"`
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

// WorkspaceTurnGate serializes whole turns that share one writable workspace.
// A channel rather than sync.Mutex makes admission cancelable: a queued child
// must stop waiting when its turn is interrupted or its wall-time expires.
type WorkspaceTurnGate struct {
	token chan struct{}
}

func NewWorkspaceTurnGate() *WorkspaceTurnGate {
	gate := &WorkspaceTurnGate{token: make(chan struct{}, 1)}
	gate.token <- struct{}{}
	return gate
}

func (g *WorkspaceTurnGate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-g.token:
	}
	var once sync.Once
	return func() {
		once.Do(func() { g.token <- struct{}{} })
	}, nil
}

type Engine struct {
	mu      sync.Mutex
	steerMu sync.Mutex
	options Options
	history []provider.Message
	pending []PendingInput // inject into current turn (steer + mailbox+trigger)
	// mailboxHold buffers non-trigger mailbox until the next turn begins.
	mailboxHold []PendingInput
	turn        uint64
	// spendMu guards the sample counter and what the turn's tools spent
	// sampling. Both are written from the tool goroutines, which run several at
	// a time, as well as from the turn's own loop.
	spendMu sync.Mutex
	// samples counts the provider calls the current turn has started, its own
	// and its tools'. It resets with the turn because usage rows are identified
	// by turn and sample.
	samples uint32
	// toolSamples is the latest cumulative report per tool-initiated sample.
	toolSamples map[uint32]toolSpend
	running     bool
	cancel      context.CancelFunc
	usage       provider.Usage
	costUSD     float64
	guard       *toolguard.Guard
	journal     *workspacejournal.Manager
	turnIDs     map[string]uint64

	approvalMu   sync.Mutex
	approvalEmit func(Event) error

	// routeMu guards the route the active turn samples on. It is nil between
	// turns, when the act route is the only sensible answer.
	routeMu   sync.RWMutex
	turnRoute *model.ReadyRoute

	// traceMu guards the active turn's span recorder and the tool spans an
	// approval wait attaches itself to. Both are written from the tool
	// goroutines, which run several at a time.
	traceMu sync.Mutex
	// recorder is the active turn's spans, and nil between turns.
	recorder *trace.Recorder
	// toolSpans maps a tool call to its open span, so the guard's approval wait
	// lands under the call that parked rather than under the turn.
	toolSpans map[string]uint64

	planMu sync.Mutex
	// planText is the rendered plan partition; plan keeps the structure behind it
	// so a compaction can report the steps that are still open rather than
	// replaying the whole plan text.
	planText    string
	plan        interact.Plan
	planReceipt *promptcontext.Receipt

	scheduler *ToolScheduler
	turnDiff  *TurnDiffTracker
	// working accumulates the paths the thread touched. Unlike turnDiff it is not
	// reset per turn: what mattered last turn usually still matters.
	working *workingset.Ledger
	// evidence accumulates what the thread found and what it has not yet proved.
	// It outlives a turn for the same reason: a file changed three turns ago and
	// still unverified is exactly what it exists to remember.
	evidence *evidence.Set
	// failures remembers the attempts that did not work. Nothing else does: the
	// receipt's failed-tool list, the verify verdict and the diagnostics set are
	// all rebuilt per turn, so once a turn is compacted away its dead ends are
	// invisible and the model walks into them again.
	failures *compact.Failures

	// turnContextMu guards the receipts of the volatile tail of the last sample.
	turnContextMu   sync.Mutex
	turnContextSeen []promptcontext.Receipt
	catalogSeen     *tool.CatalogSnapshot
	mcpHealthSeen   map[string]MCPHealthSnapshot
	extensionSeen   map[string]ExtensionSnapshot

	// diagnosticsMu guards the post-edit receipts collected during the active
	// turn, which the verify gate reads when its scope is diagnostics.
	diagnosticsMu       sync.Mutex
	turnDiagnosticsSeen []diagnostics.Receipt

	// rollbackMu guards the conflicts an automatic rollback of the active turn
	// left unresolved.
	rollbackMu        sync.Mutex
	rollbackConflicts []string

	// compactions counts how many times history was replaced by a summary. It is
	// what tells a long thread apart from one that merely looks long.
	compactions int

	budgetReminderDelivered bool
}

// activeRoute is the route to charge, measure and size the context against.
//
// Everything derived from the model — pricing, the context window, the output
// ceiling — has to come from the same route the request goes to, or a turn on a
// plan model would be budgeted against the act model's window and billed at its
// prices.
func (e *Engine) activeRoute() model.ReadyRoute {
	e.routeMu.RLock()
	defer e.routeMu.RUnlock()
	if e.turnRoute != nil {
		return *e.turnRoute
	}
	return e.options.Route
}

func (e *Engine) setTurnRoute(route model.ReadyRoute) {
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	e.turnRoute = &route
}

func (e *Engine) clearTurnRoute() {
	e.routeMu.Lock()
	defer e.routeMu.Unlock()
	e.turnRoute = nil
}

func (e *Engine) recordTurnDiagnostics(receipts []diagnostics.Receipt) {
	if len(receipts) == 0 {
		return
	}
	for _, receipt := range receipts {
		e.observePath(workingset.SourceDiagnostic, receipt.Path)
	}
	e.diagnosticsMu.Lock()
	defer e.diagnosticsMu.Unlock()
	e.turnDiagnosticsSeen = append(e.turnDiagnosticsSeen, receipts...)
}

func (e *Engine) turnDiagnostics() []diagnostics.Receipt {
	e.diagnosticsMu.Lock()
	defer e.diagnosticsMu.Unlock()
	return append([]diagnostics.Receipt(nil), e.turnDiagnosticsSeen...)
}

func (e *Engine) resetTurnDiagnostics() {
	e.diagnosticsMu.Lock()
	defer e.diagnosticsMu.Unlock()
	e.turnDiagnosticsSeen = nil
}

func New(options Options) (*Engine, error) {
	if options.Provider == nil {
		return nil, errors.New("provider is required")
	}
	if options.Tools == nil {
		options.Tools = tool.NewRegistry(nil, nil)
	}
	if options.Routes.Ready() {
		options.Route = options.Routes.Act()
	}
	if err := options.Route.Validate(); err != nil {
		return nil, err
	}
	if !options.Routes.Ready() {
		routes, err := model.NewRouteSet(options.Route, nil, false)
		if err != nil {
			return nil, err
		}
		options.Routes = routes
	}
	if options.MaxOutputTokens == 0 {
		options.MaxOutputTokens = min(4096, options.Route.Model().Limits.MaxOutputTokens)
	}
	if options.MaxSteps == 0 {
		options.MaxSteps = 64
	}
	if options.MaxSteps < 1 {
		return nil, errors.New("max steps must be positive")
	}
	if options.MaxRetries < 0 {
		return nil, errors.New("max retries cannot be negative")
	}
	if options.MaxContextBytes <= 0 {
		options.MaxContextBytes = 256 << 10
	}
	if options.TokenEstimator == nil {
		options.TokenEstimator = HeuristicTokenEstimator{}
	}
	if options.Security == nil {
		options.Security = policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	}
	if options.Verify.Mode == "" {
		// Match config.Defaults: embedded Engine users that provide a verifier
		// should not silently bypass it merely because they omitted the mode.
		options.Verify.Mode = VerifyModeSoft
	}
	if options.Verify.OnFailure == "" {
		options.Verify.OnFailure = VerifyOnFailureFail
	}
	if options.Verify.Scope == "" {
		options.Verify.Scope = verify.ScopeDiagnostics
	}
	if err := policy.Validate(options.Security); err != nil {
		return nil, err
	}
	if options.WorkingSetLimit <= 0 {
		options.WorkingSetLimit = 16
	}
	if options.EvidenceLimit <= 0 {
		options.EvidenceLimit = 24
	}
	if options.MaxToolDefinitions <= 0 {
		options.MaxToolDefinitions = 128
	}
	if options.MaxToolSchemaBytes <= 0 {
		options.MaxToolSchemaBytes = 128 << 10
	}
	if options.ToolCatalogBudget.MaxBytes <= 0 {
		options.ToolCatalogBudget.MaxBytes = 16 << 10
	}
	if options.ToolCatalogBudget.MaxTokens == 0 {
		options.ToolCatalogBudget.MaxTokens = 4 << 10
	}
	options.Tools.SetMaterializeLimits(
		tool.DefaultMaxMaterialized, tool.DefaultMaxMaterializedSchemaBytes,
	)
	if options.Now == nil {
		options.Now = time.Now
	}
	engine := &Engine{
		options: options, guard: options.Guard, journal: options.Journal,
		turnIDs:   make(map[string]uint64),
		scheduler: NewToolScheduler(options.MaxToolConcurrent),
		turnDiff:  NewTurnDiffTracker(),
		working:   workingset.New(),
		evidence:  evidence.New(),
		failures:  compact.NewFailures(),
	}
	engine.seedWorkingSet()
	if engine.guard == nil {
		guard, err := toolguard.New(toolguard.Options{
			Registry: options.Tools, Policy: options.Security, Workspace: options.Workspace,
			ReadTracker: options.ReadTracker, Journal: options.Journal, Diagnostics: options.Diagnostics,
			OnNetworkAllow: options.OnNetworkAllow,
			// One clock for the turn: the guard measures the approval wait that the
			// engine places in the trace, and two clocks would put a wait somewhere
			// its own tool span does not contain it.
			Now: options.Now,
		})
		if err != nil {
			return nil, err
		}
		engine.guard = guard
	}
	engine.guard.SetApprovalHandler(engine.emitApproval)
	engine.guard.SetApprovalWaitObserver(engine.observeApprovalWait)
	return engine, nil
}

// CloneEmpty builds a sibling Engine with the same Options seed, empty history,
// and a fresh Guard (Guard is cleared so New allocates one per clone).
func (e *Engine) CloneEmpty() (*Engine, error) {
	if e == nil {
		return nil, errors.New("engine is nil")
	}
	e.mu.Lock()
	options := e.options
	e.mu.Unlock()
	options.Guard = nil
	return New(options)
}

// OptionsSeed returns a copy of the engine Options with Guard cleared, suitable
// for constructing per-thread Engines that share Provider/Tools/Security.
func (e *Engine) OptionsSeed() Options {
	e.mu.Lock()
	defer e.mu.Unlock()
	options := e.options
	options.Guard = nil
	return options
}

func (e *Engine) Run(
	ctx context.Context, prompt string, emit func(Event) error,
) (Result, error) {
	return e.RunForTurn(ctx, "", prompt, emit)
}

func (e *Engine) RunForTurn(
	ctx context.Context, turnID, prompt string, emit func(Event) error,
) (result Result, resultErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if prompt == "" {
		return Result{}, errors.New("prompt is required")
	}
	if emit == nil {
		emit = func(Event) error { return nil }
	}
	if err := e.beginTurn(); err != nil {
		return Result{}, err
	}
	defer e.endTurn()
	releaseWorkspace, err := e.options.WorkspaceTurnGate.Acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer releaseWorkspace()
	// Only a turn the caller named can have its trace persisted: a turn started
	// through Run has no durable row to hang spans off, so it is traced in memory
	// and dropped.
	persistedTurnID := turnID
	if turnID == "" {
		turnID = fmt.Sprintf("engine-turn-%d", e.turn+1)
	}
	// The route is frozen before anything is measured or announced, so the trace
	// and the first event agree with the request that eventually goes out. A
	// locked route set that cannot serve this turn's purpose fails here, which is
	// before the turn has claimed to be doing anything.
	turnContext, err := SnapshotTurnContext(e.options, turnID)
	if err != nil {
		return result, err
	}
	e.setTurnRoute(turnContext.Route)
	defer e.clearTurnRoute()
	// The trace opens before the turn does any work, so the root span covers hook
	// execution and prompt assembly as well. Its defer is registered here, ahead
	// of the handler that sends the terminal event, so it runs after that handler:
	// deferred calls run in reverse, and a trace written before the turn reached
	// its terminal state would record the turn as still open.
	recorder, turnSpan := e.beginTrace(turnContext.Purpose)
	defer func() {
		e.endTrace(context.WithoutCancel(ctx), recorder, turnSpan, persistedTurnID, result.State)
	}()
	if e.options.SkillSnapshot != nil {
		names := make([]string, 0, len(turnContext.Skills))
		for _, summary := range turnContext.Skills {
			names = append(names, summary.Name)
		}
		ctx = skilltool.WithAllowedNames(ctx, names)
	}
	if e.guard != nil && turnContext.Policy != nil {
		sessionPolicy := e.guard.SwapPolicy(turnContext.Policy)
		defer e.guard.SwapPolicy(sessionPolicy)
	}
	if e.options.Hooks != nil {
		if err := e.options.Hooks.MessageSubmit(ctx, hooks.MessageSubmitInput{
			SessionID: e.options.SessionID, TurnID: turnID, Message: prompt,
		}); err != nil {
			return Result{}, err
		}
		defer func() {
			status := string(result.State)
			if status == "" {
				status = string(Failed)
			}
			e.options.Hooks.TurnEnd(context.WithoutCancel(ctx), hooks.TurnEndInput{
				SessionID: e.options.SessionID, TurnID: turnID, Status: status,
			})
		}()
	}
	e.setApprovalEmit(func(event Event) error {
		event.State, event.Turn = AwaitingApproval, e.turn
		return emit(event)
	})
	defer e.setApprovalEmit(nil)
	if e.options.InputHost != nil {
		e.options.InputHost.SetEmitter(func(ctx context.Context, request interact.Request) error {
			copy := request
			return emit(Event{State: AwaitingInput, Turn: e.turn, Input: &copy})
		})
		defer e.options.InputHost.SetEmitter(nil)
	}
	e.options.Metrics.AgentTurn()
	e.turn++
	e.resetToolSpend()
	result.Turn = e.turn
	journalCommitted := false
	journalRolledBack := false
	e.turnDiff.Reset()
	e.resetTurnDiagnostics()
	e.resetRollbackConflicts()
	e.evidence.BeginTurn(e.turn)
	if e.journal != nil {
		if err := e.journal.Begin(turnID); err != nil {
			return result, err
		}
	}
	transaction := cloneMessages(e.history)
	terminal := false
	send := func(state State, event Event) error {
		event.State, event.Turn = state, e.turn
		if state == Completed || state == Failed || state == Canceled {
			if terminal {
				return nil
			}
			terminal = true
		}
		return emit(event)
	}
	defer func() {
		if terminal {
			return
		}
		if errors.Is(resultErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			_ = send(Canceled, Event{Error: "turn canceled"})
			result.State = Canceled
			return
		}
		var decision *policy.DecisionError
		if errors.As(resultErr, &decision) && decision.Code == "approval_canceled" {
			_ = send(Canceled, Event{Error: "approval canceled"})
			result.State = Canceled
			return
		}
		_ = send(Failed, Event{ErrorCode: protocol.CodeOf(resultErr), Error: errorText(resultErr)})
		result.State = Failed
	}()
	if e.journal != nil {
		defer func() {
			if journalRolledBack {
				return
			}
			var receipt workspacejournal.Receipt
			var rollbackErr error
			if journalCommitted {
				if resultErr == nil {
					return
				}
				receipt, rollbackErr = e.journal.Revert(context.Background(), turnID)
			} else {
				receipt, rollbackErr = e.journal.Rollback(context.Background(), turnID)
			}
			e.recordRollbackConflicts(receipt)
			if rollbackErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf(
					"automatic workspace rollback (%d restored, %d conflicts): %w",
					len(receipt.Restored), len(receipt.Conflicts), rollbackErr,
				))
			}
		}()
	}
	if err := send(Preparing, Event{
		Provider: turnContext.Provider, Model: turnContext.Model,
		Purpose: string(turnContext.Purpose),
		Mode:    string(turnContext.Mode), Posture: string(turnContext.Posture),
		Workspace: turnContext.Workspace, Sandbox: turnContext.Sandbox,
	}); err != nil {
		return result, err
	}
	user := provider.TextMessage(provider.RoleUser, prompt)
	user.Turn = e.turn
	transaction = append(transaction, user)
	if err := e.runPreSamplingCompactGate(&transaction, send); err != nil {
		return result, err
	}
	executed := make(map[string]tool.Result)
	var finalText string
	// sampled is what the turn's own sampling used, kept apart from what its
	// tools sampled: the turn is priced at its own route's rates, and a tool's
	// tokens belong to whichever model that tool used.
	var sampled provider.Usage
	var toolSpent toolSpend
	toolSpent.known = true
	gate := &verifyGate{engine: e}
	for step := 0; step < e.options.MaxSteps+gate.extraSteps(); step++ {
		e.appendSteering(&transaction)
		if err := send(CallingModel, Event{}); err != nil {
			return result, err
		}
		blocks, calls, usage, estimatedInput, err := e.modelStep(
			ctx, &transaction, result.Usage, emitState(send),
		)
		if err != nil {
			return result, err
		}
		result.Usage.Add(usage)
		sampled.Add(usage)
		if estimatedInput > result.EstimatedInputTokens {
			result.EstimatedInputTokens = estimatedInput
		}
		text := blocksText(blocks)
		result.Reasoning += blocksReasoning(blocks)
		result.ReasoningSignature += blocksSignature(blocks)
		for _, block := range blocks {
			if block.Type == provider.ContentSearch && block.Search != nil {
				result.Searches = append(result.Searches, *block.Search)
			}
			if block.Type == provider.ContentCitation && block.Citation != nil {
				result.Citations = append(result.Citations, *block.Citation)
			}
		}
		if len(calls) == 0 {
			if e.appendSteering(&transaction) {
				if len(blocks) != 0 {
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
				}
				finalText += text
				continue
			}
			transaction = append(transaction, provider.Message{
				Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
			})
			finalText += text
			outcome, err := gate.evaluate(ctx, send)
			if err != nil {
				return result, err
			}
			result.Verification = outcome.receipt
			switch outcome.action {
			case verifyActionRepair:
				transaction = append(transaction, verifyFeedback(outcome.receipt, e.turn))
				continue
			case verifyActionFailed:
				return result, protocol.NewProblem(
					protocol.CodeConflict, outcome.receipt.problemMessage(), false, nil,
				)
			}
			pricing := e.activeRoute().Model().Pricing
			cost := estimateCost(pricing, sampled) + toolSpent.cost
			costKnown := pricing.Known && (toolSpent.samples == 0 || toolSpent.known)
			result.CostUSD = cost
			// The delta measures how good the input estimate was, and the
			// estimate only ever covered this turn's own requests. Folding a
			// tool's input tokens in would make a perfect estimate look wrong.
			result.InputTokenDelta = int64(sampled.InputTokens) - int64(result.EstimatedInputTokens)
			result.Text, result.State = finalText, Completed
			if e.journal != nil {
				// A reverted turn skips the commit and restores the workspace
				// here rather than in the deferred handler: the rollback must be
				// done before a host sees turn.completed, or a host that reads
				// files on completion observes changes that are about to vanish.
				if outcome.action == verifyActionReverted {
					receipt, err := e.journal.Rollback(context.Background(), turnID)
					e.recordRollbackConflicts(receipt)
					if err != nil {
						return result, fmt.Errorf(
							"verification rollback (%d restored, %d conflicts): %w",
							len(receipt.Restored), len(receipt.Conflicts), err,
						)
					}
					journalRolledBack = true
				} else {
					if err := e.journal.Commit(turnID); err != nil {
						return result, err
					}
					journalCommitted = true
					e.turnIDs[turnID] = e.turn
				}
			}
			if err := send(Completed, Event{
				Text: finalText, Usage: &result.Usage, CostUSD: cost,
				CostKnown:            costKnown,
				EstimatedInputTokens: result.EstimatedInputTokens,
				InputTokenDelta:      result.InputTokenDelta,
				Verification:         outcome.receipt,
			}); err != nil {
				return result, err
			}
			e.history = cloneMessages(transaction)
			e.usage.Add(result.Usage)
			e.costUSD += cost
			return result, nil
		}
		if err := send(PreparingTools, Event{}); err != nil {
			return result, err
		}
		for _, call := range calls {
			callCopy := call
			blocks = append(blocks, provider.ContentBlock{Type: provider.ContentToolCall, ToolCall: &callCopy})
		}
		transaction = append(transaction, provider.Message{
			Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
		})
		results, err := e.runTools(ctx, turnID, calls, executed, send)
		// Whatever the tools sampled is collected even when the phase failed:
		// tokens a failed tool already bought were still bought.
		spend := e.drainToolSpend()
		result.Usage.Add(spend.usage)
		toolSpent.usage.Add(spend.usage)
		toolSpent.cost += spend.cost
		toolSpent.samples += spend.samples
		if spend.samples != 0 {
			toolSpent.known = toolSpent.known && spend.known
		}
		if err != nil {
			return result, err
		}
		result.Tools = append(result.Tools, calls...)
		if err := send(FeedingResults, Event{}); err != nil {
			return result, err
		}
		for index, call := range calls {
			data, err := json.Marshal(results[index])
			if err != nil {
				return result, err
			}
			transaction = append(transaction, provider.Message{
				Role: provider.RoleTool, Turn: e.turn,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolResult,
					ToolResult: &provider.ToolResult{
						CallID: call.ID, Content: string(data), IsError: results[index].IsError,
					},
				}},
			})
		}
		if err := e.runMidTurnCompactGate(&transaction, send); err != nil {
			return result, err
		}
	}
	return result, protocol.NewProblem(
		protocol.CodeResourceExhausted,
		fmt.Sprintf(
			"engine exceeded %d steps (raise execution.max_steps, CODEHELPER_MAX_STEPS, or --max-steps)",
			e.options.MaxSteps,
		),
		false,
		nil,
	)
}

func (e *Engine) modelStep(
	ctx context.Context,
	history *[]provider.Message,
	turnUsage provider.Usage,
	send func(State, Event) error,
) ([]provider.ContentBlock, []provider.ToolCall, provider.Usage, uint64, error) {
	if err := e.emitExtensionLifecycleChanges(send); err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	if err := e.emitMCPHealthChanges(send); err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	if e.options.ToolCatalogSync != nil {
		if err := e.options.ToolCatalogSync(); err != nil {
			return nil, nil, provider.Usage{}, 0, protocol.WrapProblem(
				protocol.CodeUnavailable,
				"tool catalog synchronization failed",
				true,
				err,
			)
		}
	}
	catalog, err := e.options.Tools.Snapshot()
	if err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	if changed := e.catalogChange(catalog); changed != nil {
		if err := send(CallingModel, Event{CatalogChanged: changed}); err != nil {
			return nil, nil, provider.Usage{}, 0, err
		}
	}
	definitions, advertised, err := e.toolDefinitionsFromSnapshot(catalog)
	if err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	var totalUsage provider.Usage
	var lastEstimate uint64
	for attempt := 0; ; attempt++ {
		messages := append(e.promptMessages(), cloneMessages(*history)...)
		turnContext, turnReceipts := e.turnContextMessagesForCatalog(ctx, catalog, advertised)
		messages = append(messages, turnContext...)
		e.recordTurnContextReceipts(turnReceipts)
		// Budgeting happens after the tail is in place, so what the request
		// actually costs is what gets checked.
		estimatedInput, err := e.checkBudget(messages, turnUsage, totalUsage)
		if err != nil {
			return nil, nil, totalUsage, estimatedInput, err
		}
		e.maybeInjectBudgetReminder(&messages)
		lastEstimate = estimatedInput
		requestContext, cancel := context.WithCancel(ctx)
		e.setActiveCancel(cancel)
		// Every attempt that reaches the provider is its own sample, including a
		// retry: a retried call that already reported tokens really did spend
		// them, and folding it into the previous number would hide the cost of
		// retrying. Attempts that never reach the provider simply leave a gap.
		route := e.activeRoute()
		call := sample{
			index: e.nextSample(), provider: route.ProviderID(),
			model: route.Model().ID, pricing: route.Model().Pricing,
		}
		// The span opens before the request leaves, so connecting and waiting for
		// the first byte are inside the call rather than beside it. A retry is its
		// own span for the same reason it is its own sample: the attempt spent the
		// time whether or not it produced anything.
		callSpan := e.tracer().Start(trace.NameModelCall, 0, map[string]any{
			"provider": call.provider, "model": call.model,
			"sample": call.index, "attempt": attempt + 1,
		})
		stream, err := e.options.Provider.Stream(requestContext, provider.ModelRequest{
			Route: route, Messages: messages,
			MaxOutputTokens: e.maxOutputFor(route), Tools: definitions,
			ReasoningEffort: e.options.ReasoningEffort, NativeSearch: e.options.NativeSearch,
			Idempotent:     true,
			PromptCacheKey: provider.StickyPromptCacheKey(e.options.PromptCacheKey, route),
		})
		if err != nil {
			e.clearActiveCancel()
			cancel()
			callSpan.Set("error", errorText(err))
			callSpan.End(trace.StatusError)
			if errors.Is(err, context.Canceled) && ctx.Err() == nil && e.appendSteering(history) {
				attempt = -1
				continue
			}
			if attempt < e.options.MaxRetries && ctx.Err() == nil {
				continue
			}
			return nil, nil, totalUsage, lastEstimate, err
		}
		blocks, calls, usage, meaningful, err := consume(
			stream, call, func(event Event) error {
				return send(Streaming, event)
			},
			e.tracer().NoteFirstOutput,
		)
		e.clearActiveCancel()
		cancel()
		if err != nil {
			callSpan.Set("error", errorText(err))
			callSpan.End(trace.StatusError)
		} else {
			callSpan.End(trace.StatusOK)
		}
		totalUsage.Add(usage)
		pending := e.drainPending()
		if ctx.Err() == nil && len(pending) != 0 {
			if len(blocks) != 0 {
				*history = append(*history, provider.Message{
					Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
				})
			}
			e.appendPendingInputs(history, pending)
			attempt = -1
			continue
		}
		if err == nil {
			for index := range calls {
				binding, known := catalog.Binding(calls[index].Name)
				entry, _ := catalog.Lookup(calls[index].Name)
				unavailable := known &&
					entry.Descriptor.Visibility == tool.VisibleModel &&
					entry.Descriptor.Availability == tool.AvailabilityUnavailable
				if !known || (!advertised[calls[index].Name] && !unavailable) {
					// Keep malformed model output inside the tool-result loop.
					// Revision zero is a sampled-catalog denial marker: it lets
					// runTools report a recoverable unknown-tool result without
					// granting an omitted catalog entry execution authority.
					calls[index].CatalogID = catalog.CatalogID
					calls[index].CatalogGeneration = catalog.Generation
					continue
				}
				calls[index].CatalogID = binding.CatalogID
				calls[index].CatalogGeneration = binding.Generation
				calls[index].CatalogRevision = binding.Revision
				calls[index].CatalogAuthority = binding.Authority
			}
			return blocks, calls, totalUsage, lastEstimate, nil
		}
		if meaningful || attempt >= e.options.MaxRetries || ctx.Err() != nil {
			return blocks, nil, totalUsage, lastEstimate, err
		}
	}
}

func (e *Engine) emitExtensionLifecycleChanges(send func(State, Event) error) error {
	if e.options.ExtensionSnapshot == nil {
		return nil
	}
	current, err := e.options.ExtensionSnapshot()
	if err != nil {
		return err
	}
	sort.Slice(current, func(i, j int) bool {
		if current[i].Kind != current[j].Kind {
			return current[i].Kind < current[j].Kind
		}
		return current[i].Name < current[j].Name
	})
	if e.extensionSeen == nil {
		e.extensionSeen = make(map[string]ExtensionSnapshot)
	}
	present := make(map[string]bool, len(current))
	for _, snapshot := range current {
		if snapshot.Kind == "" || snapshot.Name == "" {
			continue
		}
		key := snapshot.Kind + "\x00" + snapshot.Name
		present[key] = true
		previous, exists := e.extensionSeen[key]
		if exists && sameExtension(previous, snapshot) {
			continue
		}
		action := extensionAction(nil, snapshot)
		previousVersion := ""
		if exists {
			action = extensionAction(&previous, snapshot)
			previousVersion = previous.Version
		}
		if err := send(CallingModel, Event{
			ExtensionLifecycle: &ExtensionLifecycleChanged{
				Action: action, PreviousVersion: previousVersion, Current: snapshot,
			},
		}); err != nil {
			return err
		}
		e.extensionSeen[key] = snapshot
	}
	var removed []string
	for key := range e.extensionSeen {
		if !present[key] {
			removed = append(removed, key)
		}
	}
	sort.Strings(removed)
	for _, key := range removed {
		previous := e.extensionSeen[key]
		revoked := previous
		revoked.Enabled = false
		revoked.ChangedAt = e.options.Now().UTC()
		if err := send(CallingModel, Event{
			ExtensionLifecycle: &ExtensionLifecycleChanged{
				Action: "revoked", PreviousVersion: previous.Version, Current: revoked,
			},
		}); err != nil {
			return err
		}
		delete(e.extensionSeen, key)
	}
	return nil
}

func extensionAction(previous *ExtensionSnapshot, current ExtensionSnapshot) string {
	if previous == nil {
		if current.Enabled {
			return "active"
		}
		return "disabled"
	}
	if previous.Enabled != current.Enabled {
		if current.Enabled {
			return "enabled"
		}
		return "disabled"
	}
	if previous.Digest != current.Digest ||
		previous.Version != current.Version ||
		previous.Generation != current.Generation {
		switch current.LastAction {
		case "install":
			return "installed"
		case "update":
			return "updated"
		case "rollback":
			return "rolled_back"
		}
		return "updated"
	}
	if current.Enabled {
		return "active"
	}
	return "disabled"
}

func sameExtension(left, right ExtensionSnapshot) bool {
	return left.Kind == right.Kind &&
		left.Name == right.Name &&
		left.Version == right.Version &&
		left.Source == right.Source &&
		left.Publisher == right.Publisher &&
		left.Trust == right.Trust &&
		left.Digest == right.Digest &&
		left.Generation == right.Generation &&
		left.Enabled == right.Enabled &&
		left.LastAction == right.LastAction &&
		left.ChangedAt.Equal(right.ChangedAt)
}

func (e *Engine) emitMCPHealthChanges(send func(State, Event) error) error {
	if e.options.MCPHealthSnapshot == nil {
		return nil
	}
	current := e.options.MCPHealthSnapshot()
	sort.Slice(current, func(i, j int) bool { return current[i].Server < current[j].Server })
	if e.mcpHealthSeen == nil {
		e.mcpHealthSeen = make(map[string]MCPHealthSnapshot)
	}
	present := make(map[string]bool, len(current))
	for _, snapshot := range current {
		if snapshot.Server == "" {
			continue
		}
		present[snapshot.Server] = true
		previous, exists := e.mcpHealthSeen[snapshot.Server]
		if exists && sameMCPHealth(previous, snapshot) {
			continue
		}
		change := &MCPHealthChanged{Current: snapshot}
		if exists {
			change.PreviousState = previous.State
		}
		if err := send(CallingModel, Event{MCPHealthChanged: change}); err != nil {
			return err
		}
		e.mcpHealthSeen[snapshot.Server] = snapshot
	}
	var removedServers []string
	for server := range e.mcpHealthSeen {
		if present[server] {
			continue
		}
		removedServers = append(removedServers, server)
	}
	sort.Strings(removedServers)
	for _, server := range removedServers {
		previous := e.mcpHealthSeen[server]
		removed := previous
		removed.State = "removed"
		removed.ChangedAt = e.options.Now().UTC()
		removed.RetryAt = time.Time{}
		if err := send(CallingModel, Event{MCPHealthChanged: &MCPHealthChanged{
			PreviousState: previous.State, Current: removed,
		}}); err != nil {
			return err
		}
		delete(e.mcpHealthSeen, server)
	}
	return nil
}

func sameMCPHealth(left, right MCPHealthSnapshot) bool {
	return left.Server == right.Server &&
		left.State == right.State &&
		left.ConsecutiveFailures == right.ConsecutiveFailures &&
		left.LastError == right.LastError &&
		left.RetryAt.Equal(right.RetryAt)
}

func (e *Engine) catalogChange(current tool.CatalogSnapshot) *CatalogChanged {
	e.turnContextMu.Lock()
	defer e.turnContextMu.Unlock()
	if e.catalogSeen != nil && e.catalogSeen.CatalogID == current.CatalogID &&
		e.catalogSeen.Generation == current.Generation {
		return nil
	}
	changed := &CatalogChanged{
		CatalogID: current.CatalogID, Generation: current.Generation, Digest: current.Digest,
	}
	old := make(map[string]tool.CatalogEntrySnapshot)
	if e.catalogSeen != nil && e.catalogSeen.CatalogID == current.CatalogID {
		for _, entry := range e.catalogSeen.Entries() {
			old[entry.Name] = entry
		}
	}
	for _, entry := range current.Entries() {
		previous, exists := old[entry.Name]
		change := tool.CatalogChange{
			Name: entry.Name, Source: entry.Source, Revision: entry.Revision,
		}
		switch {
		case !exists:
			changed.Added = append(changed.Added, change)
		case previous.Revision != entry.Revision || previous.State != entry.State:
			changed.Replaced = append(changed.Replaced, change)
		}
		delete(old, entry.Name)
	}
	for _, entry := range old {
		changed.Revoked = append(changed.Revoked, tool.CatalogChange{
			Name: entry.Name, Source: entry.Source, Revision: entry.Revision + 1,
		})
	}
	sort.Slice(changed.Revoked, func(i, j int) bool {
		return changed.Revoked[i].Name < changed.Revoked[j].Name
	})
	return changed
}

// sample names one provider call within a turn: which call it is, who answered
// it, and what its tokens cost. It travels with every usage report so a
// consumer can tell a second report about this call from the first report about
// the next one.
type sample struct {
	index    uint32
	provider string
	model    string
	pricing  model.Pricing
}

func consume(
	stream provider.Stream,
	call sample,
	emit func(Event) error,
	firstOutput func(),
) ([]provider.ContentBlock, []provider.ToolCall, provider.Usage, bool, error) {
	defer stream.Close()
	var blocks []provider.ContentBlock
	var usage provider.Usage
	fragments := make(map[int]provider.ToolCall)
	meaningful := false
	// output marks that the model produced something. It is also the first-token
	// stamp, and the two are deliberately the same event: usage arrives before any
	// content on some providers, and a stamp taken there would report a first
	// token that had not been generated yet.
	output := func() {
		meaningful = true
		if firstOutput != nil {
			firstOutput()
		}
	}
	var planParser ProposedPlanParser
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return blocks, nil, usage, meaningful, errors.New("model stream ended without stop")
		}
		if err != nil {
			return blocks, nil, usage, meaningful, err
		}
		switch event.Type {
		case provider.EventMessageStart:
		case provider.EventTextDelta:
			output()
			block := eventBlock(event, provider.ContentText)
			blocks = appendStreamBlock(blocks, event.Index, block)
			if err := emit(Event{Text: event.Text, Block: &block}); err != nil {
				return nil, nil, usage, meaningful, err
			}
			for _, update := range planParser.Feed(event.Text) {
				copy := update
				if err := emit(Event{Plan: &copy}); err != nil {
					return nil, nil, usage, meaningful, err
				}
			}
		case provider.EventReasoningDelta:
			output()
			block := eventBlock(event, provider.ContentReasoning)
			blocks = appendStreamBlock(blocks, event.Index, block)
			if err := emit(Event{Text: event.Text, Block: &block}); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventReasoningSignature:
			output()
			block := eventBlock(event, provider.ContentReasoning)
			blocks = appendStreamBlock(blocks, event.Index, block)
			if err := emit(Event{Block: &block}); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventSearchResult, provider.EventCitation:
			output()
			block := eventBlock(event, "")
			blocks = append(blocks, block)
			engineEvent := Event{Block: &block, Search: event.Search, Citation: event.Citation}
			if err := emit(engineEvent); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventUsage:
			usage.Add(*event.Usage)
			copy := usage
			// Cost travels with the tokens it was computed from, so a downstream
			// usage row never records tokens without their cost. Both stay
			// call-cumulative, keeping cost exactly as accurate as the tokens
			// beside it — and the sample says which call they are cumulative
			// over, without which a consumer cannot tell replacement from
			// addition.
			cost := estimateCost(call.pricing, copy)
			if err := emit(Event{
				Usage: &copy, CostUSD: cost, CostKnown: call.pricing.Known,
				Sample: call.index, Provider: call.provider, Model: call.model,
			}); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventToolCallDelta:
			output()
			call := fragments[event.ToolCall.Index]
			if event.ToolCall.ID != "" {
				call.ID = event.ToolCall.ID
			}
			if event.ToolCall.Name != "" {
				call.Name = event.ToolCall.Name
			}
			call.Arguments += event.ToolCall.Arguments
			fragments[event.ToolCall.Index] = call
		case provider.EventMessageStop:
			indexes := make([]int, 0, len(fragments))
			for index := range fragments {
				indexes = append(indexes, index)
			}
			sort.Ints(indexes)
			calls := make([]provider.ToolCall, 0, len(indexes))
			for _, index := range indexes {
				call := fragments[index]
				if call.ID == "" {
					call.ID = fmt.Sprintf("call_%d", index)
				}
				calls = append(calls, call)
			}
			return blocks, calls, usage, meaningful, nil
		default:
			return nil, nil, usage, meaningful, errors.New("unknown provider event")
		}
	}
}

func (e *Engine) runTools(
	ctx context.Context,
	turnID string,
	calls []provider.ToolCall,
	executed map[string]tool.Result,
	send func(State, Event) error,
) ([]tool.Result, error) {
	if err := send(RunningTools, Event{}); err != nil {
		return nil, err
	}
	identity := tool.InvocationIdentityFrom(ctx)
	if identity.ThreadID == "" {
		identity.ThreadID = e.options.SessionID
	}
	if identity.TurnID == "" {
		identity.TurnID = turnID
	}
	toolCtx, cancel := context.WithCancel(ctx)
	toolCtx = tool.WithInvocationIdentity(toolCtx, identity)
	// A tool that samples a model does it on this turn's account. The handle is
	// scoped to the tool phase because that is the only stretch in which a tool
	// call exists to charge.
	toolCtx = withToolAccount(toolCtx, &toolAccount{
		engine: e,
		emit:   func(event Event) error { return send(RunningTools, event) },
	})
	stream := newToolStream(e.options.MaxToolStreamBytes, send)
	defer stream.close()
	// Steer / mailbox cancel also aborts the tool phase; wait for cleanup below.
	e.setActiveCancel(cancel)
	defer e.clearActiveCancel()
	defer cancel()

	sched := e.scheduler
	if sched == nil {
		sched = NewToolScheduler(e.options.MaxToolConcurrent)
	}
	results := make([]tool.Result, len(calls))
	errorsByIndex := make([]error, len(calls))
	for _, call := range calls {
		if _, exists := executed[call.ID]; exists {
			continue
		}
		e.noteToolCall(call)
		callCopy := call
		if err := send(RunningTools, Event{ToolCall: &callCopy}); err != nil {
			return nil, err
		}
	}
	var group sync.WaitGroup
	for index, call := range calls {
		if previous, exists := executed[call.ID]; exists {
			results[index] = previous
			continue
		}
		group.Add(1)
		go func(index int, call provider.ToolCall) {
			defer group.Done()
			policyKind := tool.ParallelSerial
			binding := tool.CatalogBinding{
				CatalogID: call.CatalogID, Generation: call.CatalogGeneration,
				Revision: call.CatalogRevision, Authority: call.CatalogAuthority,
			}
			if _, desc, _, err := e.options.Tools.ResolveBound(call.Name, binding); err == nil {
				policyKind = desc.ParallelPolicy
			}
			release, err := sched.Admit(toolCtx, policyKind)
			if err != nil {
				results[index] = tool.Result{
					Content: "tool aborted: " + err.Error(), IsError: true,
				}
				return
			}
			defer release()

			// The span starts after admission: time spent queued behind the
			// concurrency limit is not time this tool took, and folding the two
			// together makes a fast tool look slow whenever the queue is busy.
			span := e.beginToolSpan(call)
			e.options.Metrics.ToolExecution()
			// The observer is per call: several tools run at once, and a chunk with
			// nothing to attribute it to is worse than no chunk.
			callCtx := tool.WithOutputObserver(toolCtx, stream.observe(call))
			result, err := e.guard.ExecuteBound(
				callCtx, call.ID, call.Name, json.RawMessage(call.Arguments), binding,
			)
			e.endToolSpan(call, span, result, err)
			if err != nil {
				if content, recoverable := recoverableToolFailure(err); recoverable {
					results[index] = tool.Result{Content: content, IsError: true}
					if category := toolFailureCategory(err); category != "" {
						results[index].Metadata = map[string]any{"error_category": category}
					}
					return
				}
				if errors.Is(err, context.Canceled) || errors.Is(toolCtx.Err(), context.Canceled) {
					results[index] = tool.Result{
						Content: "tool aborted: context canceled", IsError: true,
					}
					return
				}
				results[index], errorsByIndex[index] = result, err
				return
			}
			results[index], errorsByIndex[index] = result, err
		}(index, call)
	}
	group.Wait() // wait for cleanup after cancel before returning aborted results
	for index, err := range errorsByIndex {
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", calls[index].Name, err)
		}
	}
	for index := range calls {
		executed[calls[index].ID] = results[index]
		copy := results[index]
		call := calls[index]
		if !copy.IsError {
			for _, change := range observedFileChanges(copy.Metadata) {
				e.turnDiff.Record(TurnDiffEntry{
					Path: change.Path, Tool: call.Name, Kind: change.Kind,
					Added: change.Added, Removed: change.Removed,
				})
				e.observePath(workingset.SourceEdited, change.Path)
				e.observeChangeEvidence(change)
			}
			e.observePath(workingset.SourceRead, observedFileRead(copy.Metadata))
			e.observeEvidence(call, copy)
		} else {
			e.observeToolFailure(call, copy)
		}
		var diagnosticReceipts []diagnostics.Receipt
		if copy.Metadata != nil {
			diagnosticReceipts, _ = copy.Metadata["diagnostics"].([]diagnostics.Receipt)
		}
		e.recordTurnDiagnostics(diagnosticReceipts)
		e.observeDiagnosticsEvidence(diagnosticReceipts)
		if err := send(RunningTools, Event{
			ToolCall: &call, Result: &copy, Diagnostics: diagnosticReceipts,
		}); err != nil {
			return nil, err
		}
	}
	return results, nil
}

// TurnDiff returns the net file-tool changes recorded for the active/last turn (N18).
func (e *Engine) TurnDiff() []TurnDiffEntry {
	if e == nil || e.turnDiff == nil {
		return nil
	}
	return e.turnDiff.Snapshot()
}

// RollbackConflicts describes the paths an automatic rollback of the last turn
// could not restore. They are the turn's real residue: the workspace holds
// changes nobody accepted, so the receipt must name them instead of burying the
// count in an error string.
func (e *Engine) RollbackConflicts() []string {
	if e == nil {
		return nil
	}
	e.rollbackMu.Lock()
	defer e.rollbackMu.Unlock()
	return append([]string(nil), e.rollbackConflicts...)
}

func (e *Engine) recordRollbackConflicts(receipt workspacejournal.Receipt) {
	if e == nil || len(receipt.Conflicts) == 0 {
		return
	}
	e.rollbackMu.Lock()
	defer e.rollbackMu.Unlock()
	for _, conflict := range receipt.Conflicts {
		e.rollbackConflicts = append(e.rollbackConflicts, fmt.Sprintf(
			"workspace rollback could not restore %s: %s", conflict.Path, conflict.Reason,
		))
	}
}

func (e *Engine) resetRollbackConflicts() {
	if e == nil {
		return
	}
	e.rollbackMu.Lock()
	defer e.rollbackMu.Unlock()
	e.rollbackConflicts = nil
}

// FormatTurnDiff renders the turn-diff tracker, or empty when nothing was recorded.
func (e *Engine) FormatTurnDiff() string {
	if e == nil || e.turnDiff == nil {
		return ""
	}
	return e.turnDiff.Format()
}

func (e *Engine) DecideApproval(decision toolguard.ApprovalDecision) error {
	return e.guard.Decide(decision)
}

func (e *Engine) StageApprovalDecision(decision toolguard.ApprovalDecision) error {
	return e.guard.StageDecision(decision)
}

func (e *Engine) ResumeApproval(requestID string) error {
	return e.guard.Resume(requestID)
}

func (e *Engine) StageInputReply(reply interact.Reply) error {
	if e.options.InputHost == nil {
		return interact.HostUnavailableError{}
	}
	return e.options.InputHost.StageReply(reply)
}

func (e *Engine) ResumeInput(requestID string) error {
	if e.options.InputHost == nil {
		return interact.HostUnavailableError{}
	}
	return e.options.InputHost.Resume(requestID)
}

func (e *Engine) ApplyPlan(plan interact.Plan) {
	text := interact.FormatPlan(plan)
	receipt := interact.PlanReceipt(plan)
	e.planMu.Lock()
	e.planText = text
	e.plan = plan
	e.planReceipt = &receipt
	e.planMu.Unlock()
	// A file the plan calls critical is pinned for the rest of the task, not just
	// for the turn that named it.
	e.observePaths(workingset.SourcePlan, plan.CriticalFiles)
}

func (e *Engine) ContextReceipts() []promptcontext.Receipt {
	return e.contextReceipts()
}

// promptMessages is the stable prefix of every request. It holds marked
// skills/constitution fragments, which are reinjected on every sample after
// compact strips them from history.
//
// Nothing that changes during a session belongs here: the bytes are identical
// from one sample to the next so a provider can serve them from its prompt cache.
// The volatile partitions (repository map, working set, plan) are appended after
// the history instead, by turnContextMessages.
func (e *Engine) promptMessages() []provider.Message {
	return cloneMessages(e.options.PromptContext)
}

func (e *Engine) contextReceipts() []promptcontext.Receipt {
	receipts := append([]promptcontext.Receipt(nil), e.options.ContextReceipts...)
	e.planMu.Lock()
	if e.planReceipt != nil {
		filtered := receipts[:0]
		for _, receipt := range receipts {
			if receipt.Kind != promptcontext.PartitionPlan {
				filtered = append(filtered, receipt)
			}
		}
		receipts = append(filtered, *e.planReceipt)
	}
	e.planMu.Unlock()
	return append(receipts, e.turnContextReceipts()...)
}

func (e *Engine) setApprovalEmit(emit func(Event) error) {
	e.approvalMu.Lock()
	e.approvalEmit = emit
	e.approvalMu.Unlock()
}

func (e *Engine) emitApproval(_ context.Context, request toolguard.ApprovalRequest) error {
	e.approvalMu.Lock()
	emit := e.approvalEmit
	e.approvalMu.Unlock()
	if emit == nil {
		return errors.New("approval host is not connected to an active turn")
	}
	return emit(Event{Approval: &request})
}

func (e *Engine) toolDefinitions() []provider.ToolDefinition {
	snapshot, err := e.options.Tools.Snapshot()
	if err != nil {
		return nil
	}
	definitions, _, err := e.toolDefinitionsFromSnapshot(snapshot)
	if err != nil {
		return nil
	}
	return definitions
}

func (e *Engine) toolDefinitionsFromSnapshot(
	snapshot tool.CatalogSnapshot,
) ([]provider.ToolDefinition, map[string]bool, error) {
	var descriptors []tool.Descriptor
	for _, entry := range snapshot.Entries() {
		if entry.Descriptor.Visibility == tool.VisibleModel &&
			entry.Descriptor.Availability != tool.AvailabilityUnavailable {
			descriptors = append(descriptors, entry.Descriptor)
		}
	}
	if onlyRetrievalHelpers(descriptors) {
		return nil, map[string]bool{}, nil
	}
	threshold := e.options.ToolSearchThreshold
	if threshold <= 0 {
		threshold = toolsearch.DefaultThresh
	}
	useSearch := toolsearch.ShouldEnable(descriptors, threshold)
	for _, entry := range snapshot.Entries() {
		if entry.State == tool.CatalogEntryDeferred {
			useSearch = true
			break
		}
	}
	result := make([]provider.ToolDefinition, 0, len(descriptors))
	advertised := make(map[string]bool)
	schemaBytes := 0
	add := func(entry tool.CatalogEntrySnapshot, required bool) error {
		descriptor := entry.Descriptor
		data, _ := json.Marshal(descriptor.InputSchema)
		if len(result)+1 > e.options.MaxToolDefinitions ||
			schemaBytes+len(data) > e.options.MaxToolSchemaBytes {
			if required {
				return fmt.Errorf(
					"%w: provider tools[] cannot fit required tool %q",
					tool.ErrCatalogLimit, descriptor.Name,
				)
			}
			return nil
		}
		result = append(result, provider.ToolDefinition{
			Name: descriptor.Name, Description: descriptor.Description,
			InputSchema: descriptor.InputSchema,
		})
		advertised[descriptor.Name] = true
		schemaBytes += len(data)
		return nil
	}
	var search *tool.CatalogEntrySnapshot
	for _, entry := range snapshot.Entries() {
		descriptor := entry.Descriptor
		if descriptor.Visibility != tool.VisibleModel ||
			descriptor.Availability == tool.AvailabilityUnavailable {
			continue
		}
		if descriptor.Name == toolsearch.ToolName {
			copy := entry
			search = &copy
			continue
		}
		if entry.State != tool.CatalogEntryMaterialized {
			continue
		}
		if err := add(entry, true); err != nil {
			return nil, nil, err
		}
	}
	for _, entry := range snapshot.Entries() {
		descriptor := entry.Descriptor
		if descriptor.Visibility != tool.VisibleModel ||
			descriptor.Availability == tool.AvailabilityUnavailable ||
			descriptor.Name == toolsearch.ToolName ||
			entry.State == tool.CatalogEntryDeferred ||
			entry.State == tool.CatalogEntryMaterialized {
			continue
		}
		// Eager tools form the runtime's core contract. The search threshold
		// decides whether deferred tools need discovery; it must not silently
		// remove eager tools from tools[], because models then guess aliases such
		// as "read" and burn the turn retrying unknown calls.
		if err := add(entry, true); err != nil {
			return nil, nil, err
		}
	}
	if search != nil {
		if err := add(*search, useSearch); err != nil {
			return nil, nil, err
		}
	}
	return result, advertised, nil
}

func onlyRetrievalHelpers(descriptors []tool.Descriptor) bool {
	if len(descriptors) == 0 {
		return false
	}
	for _, descriptor := range descriptors {
		switch descriptor.Name {
		case "result_get", "handle_read":
		default:
			return false
		}
	}
	return true
}

func (e *Engine) compact() *CompactionReceipt {
	return e.compactHistory(&e.history, false)
}

// runPreSamplingCompactGate compresses history before the first model sample
// when byte or context-token budgets are exceeded.
func (e *Engine) runPreSamplingCompactGate(
	history *[]provider.Message,
	send func(State, Event) error,
) error {
	receipt := e.compactHistory(history, false)
	if receipt == nil && e.contextTokenLimitReached(*history) {
		receipt = e.compactHistory(history, true)
	}
	if receipt == nil {
		return nil
	}
	receipt.Phase = CompactionPhasePreSampling
	return send(Compacting, Event{Compaction: receipt})
}

func (e *Engine) runMidTurnCompactGate(
	history *[]provider.Message,
	send func(State, Event) error,
) error {
	receipt := e.compactHistory(history, false)
	if receipt == nil {
		return nil
	}
	receipt.Phase = CompactionPhaseMidTurn
	return send(Compacting, Event{Compaction: receipt})
}

func (e *Engine) contextTokenLimitReached(history []provider.Message) bool {
	route := e.activeRoute()
	limit := route.Model().Limits.ContextTokens
	if limit == 0 {
		return false
	}
	messages := append(e.promptMessages(), cloneMessages(history)...)
	estimated, err := e.options.TokenEstimator.Estimate(messages)
	if err != nil {
		return false
	}
	return estimated+e.maxOutputFor(route) > limit
}

// maxOutputFor is the output ceiling to ask this route for. The configured
// ceiling is a session-level number, so a turn routed to a model with a smaller
// output limit is clamped to it: sending the larger number would be refused by
// the provider, which reads as a routing failure rather than as a ceiling.
func (e *Engine) maxOutputFor(route model.ReadyRoute) uint64 {
	limit := route.Model().Limits.MaxOutputTokens
	if limit == 0 || e.options.MaxOutputTokens <= limit {
		return e.options.MaxOutputTokens
	}
	return limit
}

// Compact applies the auto budget policy under the engine lock.
func (e *Engine) Compact() *CompactionReceipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compact()
}

// CompactForced summarizes older turns even when history is under MaxContextBytes.
// Used by explicit thread.compact operations.
func (e *Engine) CompactForced() *CompactionReceipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compactHistory(&e.history, true)
}

func (e *Engine) compactHistory(history *[]provider.Message, force bool) *CompactionReceipt {
	if e.options.Hooks != nil {
		if err := e.options.Hooks.PreCompact(context.Background(), hooks.CompactInput{
			SessionID: e.options.SessionID, Forced: force, Messages: len(*history),
		}); err != nil {
			return nil
		}
	}
	size := 0
	for _, message := range *history {
		size += messageSize(message)
	}
	if len(*history) <= 1 {
		return nil
	}
	if !force && size <= e.options.MaxContextBytes {
		return nil
	}
	originalMessages := len(*history)
	target := max(1, e.options.MaxContextBytes*3/4)
	if force && size <= e.options.MaxContextBytes {
		// Keep roughly the last turn's worth; summarize everything before the tail turn.
		target = max(1, size/4)
	}
	tailSize := 0
	cut := len(*history)
	lastTurn := (*history)[len(*history)-1].Turn
	for cut > 0 {
		groupStart := cut - 1
		turn := (*history)[groupStart].Turn
		for groupStart > 0 && (*history)[groupStart-1].Turn == turn {
			groupStart--
		}
		groupSize := 0
		for _, message := range (*history)[groupStart:cut] {
			groupSize += messageSize(message)
		}
		if cut < len(*history) && tailSize+groupSize > target {
			break
		}
		tailSize += groupSize
		cut = groupStart
		if turn == lastTurn && cut == 0 {
			return nil
		}
	}
	if cut == 0 {
		return nil
	}
	toSummarize := promptcontext.StripContextualFragments(cloneMessages((*history)[:cut]))
	// The summary is built from the live ledgers, not from the frozen options:
	// compaction is exactly the moment the model loses the history that told it
	// which files were in play and what it had already tried.
	summary := e.buildCompactSummary(toSummarize)
	// The working set is reported to the host but not written into the summary:
	// the volatile tail already carries it on every sample, so spending summary
	// bytes on it would pay for the same list twice.
	workingSet, criticalPaths := e.compactionPaths()
	rendered, summaryTruncated, sections := summary.Render(e.summaryBudget())
	removed := cloneMessages((*history)[:cut])
	compacted := provider.TextMessage(provider.RoleSystem, rendered)
	tail := promptcontext.StripContextualFragments(cloneMessages((*history)[cut:]))
	*history = append([]provider.Message{compacted}, tail...)
	retainedBytes := 0
	for _, message := range *history {
		retainedBytes += messageSize(message)
	}
	e.compactions++
	receipt := &CompactionReceipt{
		OriginalMessages: originalMessages, RemovedMessages: cut,
		OriginalBytes: size, RetainedBytes: retainedBytes,
		SummaryOriginalBytes: digestOriginalBytes(toSummarize),
		SummaryRetainedBytes: len(rendered),
		SummaryTruncated:     summaryTruncated,
		Sections:             sections,
		RemovedTurns:         uniqueMessageTurns(removed),
		// The partition receipts stay on the host-facing receipt and out of the
		// summary text: byte and digest detail is for an audit, and every sample
		// after this one would have paid to carry it.
		PromptContextReceipts: e.contextReceipts(),
		WorkingSet:            workingSet, CriticalPaths: criticalPaths,
	}
	if summaryTruncated {
		receipt.TruncationReason = "summary_byte_budget"
	}
	e.options.Metrics.Compaction(max(0, size-retainedBytes))
	if e.options.Hooks != nil {
		e.options.Hooks.PostCompact(context.Background(), hooks.CompactInput{
			SessionID: e.options.SessionID, Forced: force, Messages: len(*history),
		})
	}
	e.resetBudgetReminder()
	return receipt
}

func (e *Engine) History() []provider.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneMessages(e.history)
}

// ReplaceHistory installs a compacted replacement window as the model-visible history.
func (e *Engine) ReplaceHistory(messages []provider.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = cloneMessages(messages)
	e.resetBudgetReminder()
	var maxTurn uint64
	for _, message := range e.history {
		if message.Turn > maxTurn {
			maxTurn = message.Turn
		}
	}
	if maxTurn > e.turn {
		e.turn = maxTurn
	}
}

func (e *Engine) Fork() *Engine {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	e.planMu.Lock()
	defer e.planMu.Unlock()
	forked := &Engine{
		options: e.options, history: cloneMessages(e.history),
		pending:     append([]PendingInput(nil), e.pending...),
		mailboxHold: append([]PendingInput(nil), e.mailboxHold...),
		turn:        e.turn,
		scheduler:   NewToolScheduler(e.options.MaxToolConcurrent),
		turnDiff:    NewTurnDiffTracker(),
		// A fork inherits what the parent learned about the repository but not its
		// future: the two threads diverge from here.
		working:  e.working.Clone(),
		evidence: e.evidence.Clone(),
		failures: e.failures.Clone(),
		// The plan travels with the fork: a child that cannot see the plan will
		// rediscover its own, and the two will disagree.
		planText: e.planText,
		plan:     e.plan.Clone(),
	}
	if e.planReceipt != nil {
		receipt := *e.planReceipt
		forked.planReceipt = &receipt
	}
	return forked
}

func (e *Engine) Undo() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.history) == 0 {
		return false
	}
	lastTurn := e.history[len(e.history)-1].Turn
	if lastTurn != 0 {
		start := len(e.history) - 1
		for start > 0 && e.history[start-1].Turn == lastTurn {
			start--
		}
		e.history = e.history[:start]
		return true
	}
	start := -1
	for index := len(e.history) - 1; index >= 0; index-- {
		if e.history[index].Role == provider.RoleUser {
			start = index
			break
		}
	}
	if start < 0 {
		return false
	}
	e.history = e.history[:start]
	return true
}

// LastTurnID returns the workspace journal turn id with the highest turn number.
func (e *Engine) LastTurnID() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.turnIDs) == 0 {
		return "", errors.New("no turn to revert")
	}
	var bestID string
	var bestTurn uint64
	for id, turn := range e.turnIDs {
		if bestID == "" || turn >= bestTurn {
			bestID = id
			bestTurn = turn
		}
	}
	return bestID, nil
}

func (e *Engine) RevertWorkspace(
	ctx context.Context, targetTurnID string,
) (workspacejournal.Receipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.journal == nil {
		return workspacejournal.Receipt{}, errors.New("workspace journal is not configured")
	}
	turn, exists := e.turnIDs[targetTurnID]
	if !exists {
		return workspacejournal.Receipt{}, errors.New("target turn is not present in workspace history")
	}
	receipt, err := e.journal.Revert(ctx, targetTurnID)
	if err != nil {
		return receipt, err
	}
	history := e.history[:0]
	for _, message := range e.history {
		if message.Turn != turn {
			history = append(history, message)
		}
	}
	e.history = history
	delete(e.turnIDs, targetTurnID)
	return receipt, nil
}

func (e *Engine) Steer(prompt string) error {
	if prompt == "" {
		return errors.New("steering prompt is required")
	}
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	if !e.running {
		return errors.New("no active turn to steer")
	}
	e.pending = append(e.pending, PendingInput{Source: PendingSteer, Prompt: prompt})
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

// EnqueueMailbox queues an inter-agent mailbox message.
// triggerTurn=true injects into the current turn (cancels sampling, like Steer).
// triggerTurn=false buffers until the next turn begins (avoids late-mail pollution).
func (e *Engine) EnqueueMailbox(prompt string, triggerTurn bool) error {
	if prompt == "" {
		return errors.New("mailbox prompt is required")
	}
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	item := PendingInput{Source: PendingMailbox, Prompt: prompt, TriggerTurn: triggerTurn}
	if !triggerTurn {
		e.mailboxHold = append(e.mailboxHold, item)
		return nil
	}
	if !e.running {
		// No active turn: hold for the next turn start (aligns with start_new_turn / buffer).
		e.mailboxHold = append(e.mailboxHold, item)
		return nil
	}
	e.pending = append(e.pending, item)
	if e.cancel != nil {
		e.cancel()
	}
	return nil
}

func (e *Engine) appendSteering(history *[]provider.Message) bool {
	pending := e.drainPending()
	e.appendPendingInputs(history, pending)
	return len(pending) != 0
}

func (e *Engine) drainPending() []PendingInput {
	e.steerMu.Lock()
	pending := e.pending
	e.pending = nil
	e.steerMu.Unlock()
	return pending
}

func (e *Engine) appendPendingInputs(history *[]provider.Message, pending []PendingInput) {
	for _, item := range pending {
		text := item.Prompt
		if item.Source == PendingMailbox {
			text = "[mailbox] " + item.Prompt
		}
		message := provider.TextMessage(provider.RoleUser, text)
		message.Turn = e.turn
		*history = append(*history, message)
	}
}

// appendSteeringPrompts keeps the historical name used by modelStep drain path.
func (e *Engine) appendSteeringPrompts(history *[]provider.Message, pending []PendingInput) {
	e.appendPendingInputs(history, pending)
}

func (e *Engine) beginTurn() error {
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	if e.running {
		return errors.New("engine turn is already running")
	}
	e.running = true
	// Promote held mailbox into the new turn's inject queue; drop stale steer.
	e.pending = append([]PendingInput(nil), e.mailboxHold...)
	e.mailboxHold = nil
	return nil
}

func (e *Engine) endTurn() {
	e.steerMu.Lock()
	for _, item := range e.pending {
		if item.Source == PendingMailbox {
			e.mailboxHold = append(e.mailboxHold, item)
		}
	}
	e.running = false
	e.pending = nil
	e.cancel = nil
	e.steerMu.Unlock()
}

func (e *Engine) setActiveCancel(cancel context.CancelFunc) {
	e.steerMu.Lock()
	e.cancel = cancel
	e.steerMu.Unlock()
}

func (e *Engine) clearActiveCancel() {
	e.steerMu.Lock()
	e.cancel = nil
	e.steerMu.Unlock()
}

// RequestCancel aborts the active model/tool phase if one is running (N14 Abort).
func (e *Engine) RequestCancel() {
	e.steerMu.Lock()
	cancel := e.cancel
	e.steerMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func messageSize(message provider.Message) int {
	size := 0
	for _, block := range message.Blocks {
		size += len(block.Text) + len(block.Signature) + len(block.ProviderData)
		if block.ToolCall != nil {
			size += len(block.ToolCall.ID) + len(block.ToolCall.Name) + len(block.ToolCall.Arguments)
		}
		if block.ToolResult != nil {
			size += len(block.ToolResult.CallID) + len(block.ToolResult.Content)
		}
	}
	return size
}

func uniqueMessageTurns(messages []provider.Message) []uint64 {
	seen := make(map[uint64]struct{})
	var turns []uint64
	for _, message := range messages {
		if message.Turn == 0 {
			continue
		}
		if _, exists := seen[message.Turn]; exists {
			continue
		}
		seen[message.Turn] = struct{}{}
		turns = append(turns, message.Turn)
	}
	return turns
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func cloneMessages(messages []provider.Message) []provider.Message {
	cloned := make([]provider.Message, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].Blocks = cloneBlocks(message.Blocks)
	}
	return cloned
}

func cloneBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	cloned := make([]provider.ContentBlock, len(blocks))
	for index, block := range blocks {
		cloned[index] = block
		if block.ToolCall != nil {
			copy := *block.ToolCall
			cloned[index].ToolCall = &copy
		}
		if block.ToolResult != nil {
			copy := *block.ToolResult
			cloned[index].ToolResult = &copy
		}
		if block.Search != nil {
			copy := *block.Search
			copy.Sources = append([]provider.Source(nil), block.Search.Sources...)
			cloned[index].Search = &copy
		}
		if block.Citation != nil {
			copy := *block.Citation
			cloned[index].Citation = &copy
		}
		cloned[index].ProviderData = append([]byte(nil), block.ProviderData...)
	}
	return cloned
}

func eventBlock(event provider.StreamEvent, fallback provider.ContentType) provider.ContentBlock {
	if event.Block != nil {
		return cloneBlocks([]provider.ContentBlock{*event.Block})[0]
	}
	switch event.Type {
	case provider.EventTextDelta:
		return provider.ContentBlock{Type: provider.ContentText, Text: event.Text}
	case provider.EventReasoningDelta:
		return provider.ContentBlock{Type: provider.ContentReasoning, Text: event.Text}
	case provider.EventReasoningSignature:
		return provider.ContentBlock{Type: provider.ContentReasoning, Signature: event.Signature}
	case provider.EventSearchResult:
		return provider.ContentBlock{Type: provider.ContentSearch, Search: event.Search}
	case provider.EventCitation:
		return provider.ContentBlock{Type: provider.ContentCitation, Citation: event.Citation}
	default:
		return provider.ContentBlock{Type: fallback, Text: event.Text}
	}
}

func appendStreamBlock(
	blocks []provider.ContentBlock,
	_ int,
	block provider.ContentBlock,
) []provider.ContentBlock {
	if len(blocks) != 0 && block.Type == blocks[len(blocks)-1].Type {
		last := &blocks[len(blocks)-1]
		if block.Type == provider.ContentText {
			last.Text += block.Text
			return blocks
		}
		if block.Type == provider.ContentReasoning &&
			(len(last.ProviderData) == 0 || len(block.ProviderData) == 0 ||
				(last.ID != "" && last.ID == block.ID)) {
			// Deltas append; a later output_item.done / response.completed may
			// repeat the full text — keep the longer form once, never concatenate
			// the same chain twice.
			if block.Text == "" && len(block.ProviderData) != 0 {
				var item map[string]any
				if json.Unmarshal(block.ProviderData, &item) == nil {
					block.Text = reasoningTextFromProviderData(item)
				}
			}
			switch {
			case last.Text == "":
				last.Text = block.Text
			case block.Text == "":
				// keep last
			case strings.Contains(block.Text, last.Text) && len(block.Text) >= len(last.Text):
				last.Text = block.Text
			case strings.Contains(last.Text, block.Text):
				// keep last
			case len(last.ProviderData) == 0:
				last.Text += block.Text
			}
			last.Signature += block.Signature
			if len(last.ProviderData) == 0 {
				last.ProviderType = block.ProviderType
				last.ProviderData = append([]byte(nil), block.ProviderData...)
				last.ID = block.ID
			} else if last.ID == "" {
				last.ID = block.ID
			}
			return blocks
		}
	}
	return append(blocks, block)
}

func reasoningTextFromProviderData(item map[string]any) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"content", "summary"} {
		switch content := item[key].(type) {
		case string:
			if content != "" {
				return content
			}
		case []any:
			var parts []string
			for _, raw := range content {
				part, _ := raw.(map[string]any)
				if part == nil {
					continue
				}
				switch typ, _ := part["type"].(string); typ {
				case "reasoning_text", "output_text", "summary_text", "text", "":
					if text, _ := part["text"].(string); text != "" {
						parts = append(parts, text)
					}
				}
			}
			if joined := strings.Join(parts, ""); joined != "" {
				return joined
			}
		}
	}
	return ""
}

func blocksText(blocks []provider.ContentBlock) string {
	var result string
	for _, block := range blocks {
		if block.Type == provider.ContentText {
			result += block.Text
		}
	}
	return result
}

func blocksReasoning(blocks []provider.ContentBlock) string {
	var result string
	for _, block := range blocks {
		if block.Type == provider.ContentReasoning {
			result += block.Text
		}
	}
	return result
}

func blocksSignature(blocks []provider.ContentBlock) string {
	var result string
	for _, block := range blocks {
		if block.Type == provider.ContentReasoning {
			result += block.Signature
		}
	}
	return result
}

func messageToolCalls(message provider.Message) []provider.ToolCall {
	var calls []provider.ToolCall
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolCall && block.ToolCall != nil {
			calls = append(calls, *block.ToolCall)
		}
	}
	return calls
}

func messageToolResultID(message provider.Message) string {
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolResult && block.ToolResult != nil {
			return block.ToolResult.CallID
		}
	}
	return ""
}

func estimateMessageTokens(messages []provider.Message) uint64 {
	characters := 0
	for _, message := range messages {
		for _, block := range message.Blocks {
			characters += len([]rune(block.Text))
			characters += len([]rune(block.Signature))
			if block.ToolCall != nil {
				characters += len([]rune(block.ToolCall.Name + block.ToolCall.Arguments))
			}
			if block.ToolResult != nil {
				characters += len([]rune(block.ToolResult.Content))
			}
		}
	}
	return uint64(max(1, (characters+3)/4))
}

func estimateCost(pricing model.Pricing, usage provider.Usage) float64 {
	return float64(usage.InputTokens)/1_000_000*pricing.InputPerMillion +
		float64(usage.OutputTokens)/1_000_000*pricing.OutputPerMillion
}

func (e *Engine) checkBudget(
	messages []provider.Message,
	turnUsage provider.Usage,
	stepUsage provider.Usage,
) (uint64, error) {
	estimatedInput, err := e.options.TokenEstimator.Estimate(messages)
	if err != nil {
		return 0, protocol.NewProblem(protocol.CodeInternal, "estimate input tokens", false, err)
	}
	route := e.activeRoute()
	if estimatedInput+e.maxOutputFor(route) > route.Model().Limits.ContextTokens {
		return estimatedInput, protocol.NewProblem(
			protocol.CodeResourceExhausted, "context window exceeded", false, nil,
		)
	}
	projectedTokens := e.usage.Total() + turnUsage.Total() + stepUsage.Total() +
		estimatedInput + e.options.MaxOutputTokens
	if limit := e.options.Budget.MaxTokens; limit > 0 && projectedTokens > limit {
		return estimatedInput, protocol.NewProblem(
			protocol.CodeResourceExhausted,
			fmt.Sprintf("token budget exceeded: projected %d, limit %d", projectedTokens, limit),
			false,
			nil,
		)
	}
	if limit := e.options.Budget.MaxCostUSD; limit > 0 {
		pricing := route.Model().Pricing
		if !pricing.Known {
			return estimatedInput, protocol.NewProblem(
				protocol.CodeInvalidArgument, "cost budget requires known model pricing", false, nil,
			)
		}
		projectedUsage := turnUsage
		projectedUsage.Add(stepUsage)
		projectedUsage.Add(provider.Usage{
			InputTokens: estimatedInput, OutputTokens: e.options.MaxOutputTokens,
		})
		if projected := e.costUSD + estimateCost(pricing, projectedUsage); projected > limit {
			return estimatedInput, protocol.NewProblem(
				protocol.CodeResourceExhausted,
				fmt.Sprintf("cost budget exceeded: projected %.6f, limit %.6f", projected, limit),
				false,
				nil,
			)
		}
	}
	return estimatedInput, nil
}

func (e *Engine) maybeInjectBudgetReminder(messages *[]provider.Message) {
	limit := e.options.Budget.MaxTokens
	if limit == 0 || e.budgetReminderDelivered || messages == nil {
		return
	}
	threshold := e.options.BudgetReminderThreshold
	if threshold == 0 {
		threshold = limit / 10
		if threshold < 256 {
			threshold = 256
		}
		if threshold > limit {
			threshold = limit
		}
	}
	used := e.usage.Total()
	if used >= limit {
		return
	}
	remaining := limit - used
	if remaining > threshold {
		return
	}
	e.budgetReminderDelivered = true
	text := fmt.Sprintf(
		"[budget reminder] Approximately %d tokens remaining of session budget %d. "+
			"Prefer wrapping up or asking the user before starting large work.",
		remaining, limit,
	)
	*messages = append(*messages, provider.TextMessage(provider.RoleUser, text))
}

func (e *Engine) resetBudgetReminder() {
	e.budgetReminderDelivered = false
}

func (e *Engine) Usage() (provider.Usage, float64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.usage, e.costUSD
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
	// No lock, by the same convention as ContextBudget: the turn holds the engine
	// mutex for its whole run, and this is read from inside a turn.
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

func emitState(send func(State, Event) error) func(State, Event) error {
	return send
}

func errorText(err error) string {
	if err == nil {
		return "turn failed"
	}
	return err.Error()
}
