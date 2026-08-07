package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
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
	toolSamples  map[uint32]toolSpend
	running      bool
	cancel       context.CancelFunc
	cancelReason string
	usage        provider.Usage
	costUSD      float64
	guard        *toolguard.Guard
	journal      *workspacejournal.Manager
	turnIDs      map[string]uint64

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
	failures        *compact.Failures
	promptCacheBase string

	// turnContextMu guards the receipts of the volatile tail of the last sample.
	turnContextMu   sync.Mutex
	turnContextSeen []promptcontext.Receipt
	turnSelections  []promptcontext.Selection
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
		promptCacheBase: options.PromptCacheKey,
		turnIDs:         make(map[string]uint64),
		scheduler:       NewToolScheduler(options.MaxToolConcurrent),
		turnDiff:        NewTurnDiffTracker(),
		working:         workingset.New(),
		evidence:        evidence.New(),
		failures:        compact.NewFailures(),
	}
	engine.seedWorkingSet()
	if engine.guard == nil {
		guard, err := toolguard.New(toolguard.Options{
			Registry: options.Tools, Policy: options.Security, Workspace: options.Workspace,
			ReadTracker: options.ReadTracker, Journal: options.Journal, Diagnostics: options.Diagnostics,
			OnNetworkAllow: options.OnNetworkAllow,

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

func (e *Engine) ValidateSessionProfile(profile protocol.SessionProfile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.running {
		return errors.New("session profile cannot change while a turn is active")
	}
	route := e.options.Routes.Act()
	if profile.Provider != route.ProviderID() || profile.Model != route.Model().ID {
		return errors.New("session profile route is unavailable in this runtime")
	}
	if profile.ReasoningEffort != "" && !route.Model().Capabilities.Reasoning {
		return errors.New("session profile model does not support reasoning effort")
	}
	return nil
}

func (e *Engine) ApplySessionProfile(profile protocol.SessionProfile) error {
	if err := e.ValidateSessionProfile(profile); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.options.ReasoningEffort = profile.ReasoningEffort
	e.options.MaxSteps = profile.MaxSteps
	e.options.PromptCacheKey = fmt.Sprintf(
		"%s-profile-%d",
		e.promptCacheBase,
		profile.PromptCacheRevision,
	)
	e.options.Security.Mode = policy.Mode(profile.Mode)
	e.options.Security.Permission = policy.Permission(profile.ApprovalPosture)
	return nil
}

func (e *Engine) SetPolicyMode(mode policy.Mode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.options.Security.Mode = mode
}

func (e *Engine) SetPermission(permission policy.Permission) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.options.Security.Permission = permission
}

func (e *Engine) SetGranular(granular policy.Granular) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.options.Security.Granular = granular
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
