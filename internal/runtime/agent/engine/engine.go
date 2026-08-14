package engine

import (
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
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
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
	Routes           model.RouteSet
	Tools            *tool.Registry
	PromptContext    []provider.Message
	ModePromptBudget promptcontext.Budget
	MaxOutputTokens  uint64
	MaxSteps         int
	MaxRetries       int
	CompactWindow    CompactWindowPolicy
	SummaryMaxBytes  int
	// MaxDigestEntries bounds the per-message running record a summary carries.
	MaxDigestEntries     int
	ReasoningEffort      string
	FixedReasoningEffort string
	NativeSearch         bool
	Budget               Budget
	// BudgetReminderThreshold is remaining tokens that trigger a one-shot reminder
	// (0 → max(256, MaxTokens/10)).
	BudgetReminderThreshold uint64
	TokenEstimator          TokenEstimator
	WorkingSet              []string
	CriticalPaths           []string
	ContextReceipts         []promptcontext.Receipt
	Authorize               func(provider.ToolCall) bool
	Security                *policy.Runtime
	// ProfilePermissionCeiling is fixed by the Host at construction. An empty
	// value inherits Security.Permission for callers that do not use Profiles.
	ProfilePermissionCeiling policy.Permission
	Guard                    *toolguard.Guard
	// OnNetworkAllow is wired into a Guard that New allocates when Guard is
	// nil. Without it, mid-flight egress approvals update the approval cache
	// but never Grant the session Gate, so the retry still gets egress denied.
	OnNetworkAllow     func(host, protocol string)
	Workspace          string
	WorkspaceIsolation string
	Metrics            Metrics
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
	// RequireCompletionDeclaration makes tool-assisted completion depend on an
	// accepted turn_complete result. Mutating declarations bind to the current
	// mutation revision; read-only declarations bind to revision zero.
	RequireCompletionDeclaration bool
	// TurnKernelObserver receives deterministic records after Coordinator
	// commits a transition. The callback is diagnostics-only: panics are
	// contained and it cannot fail or alter the active Turn.
	TurnKernelObserver func(turnkernel.TransitionRecord)
	// TurnCoordinatorRuntime owns per-Turn Coordinator construction and
	// persistence. Production hosts inject it from runtime/app/wire.
	TurnCoordinatorRuntime turnkernel.CoordinatorRuntime
	Hooks                  *hooks.Manager
	SessionID              string
	InputHost              *interact.Host
	// PromptCacheKey is the session sticky hint; samples only attach it when
	// StickyPromptCacheKey drops the session default when the route lacks
	// prompt_cache, so Validate/encode stay consistent across protocols.
	PromptCacheKey string
	// ProfileRevision is the durable Session Profile revision frozen into each
	// TurnCoordinator snapshot. Hosts without a profile store use revision 1.
	ProfileRevision uint64
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
	TurnSnapshots   TurnSnapshotSources
	// RepoContext snapshots the repository, working set, and evidence.
	RepoContext RepoContext
	// WorkingSetLimit excludes pinned paths from its bound (0 → 16).
	WorkingSetLimit int
	// EvidenceLimit bounds facts, but not risks and reminders (0 → 24).
	EvidenceLimit int
}

type Metrics interface {
	AgentTurn()
	ToolExecution()
	Error()
	Evidence(int, int)
	Compaction(int)
	TurnKernelObserver(bool, bool)
}

type noopMetrics struct{}

func (noopMetrics) AgentTurn()                    {}
func (noopMetrics) ToolExecution()                {}
func (noopMetrics) Error()                        {}
func (noopMetrics) Evidence(int, int)             {}
func (noopMetrics) Compaction(int)                {}
func (noopMetrics) TurnKernelObserver(bool, bool) {}

type TurnSnapshotSources struct {
	MCP        func() []MCPHealthSnapshot
	Extensions func() ([]ExtensionSnapshot, error)
	Skills     func() []SkillSummary
}

type Engine struct {
	mu      sync.Mutex
	scopeMu sync.Mutex
	options Options
	history []provider.Message
	// mailboxHold buffers non-trigger mailbox until the next turn begins.
	mailboxHold     []PendingInput
	turn            uint64
	usage           provider.Usage
	costUSD         float64
	sessionRevision uint64
	appliedDeltas   map[string]string
	guard           *toolguard.Guard
	journal         *workspacejournal.Manager
	turnIDs         map[string]uint64

	planMu sync.Mutex
	// plan keeps the structure required by compaction and durable recovery.
	planText    string
	plan        interact.Plan
	planReceipt *promptcontext.Receipt

	// working and evidence are durable thread state; TurnDiff is not.
	working  *workingset.Ledger
	evidence *evidence.Set
	// failures prevents compacted dead ends from being repeated.
	failures        *compact.Failures
	promptCacheBase string
	profileReadOnly bool
	enabledTools    map[string]struct{}

	approvalRecovery recoveredInteraction[toolguard.ApprovalDecision]
	inputRecovery    recoveredInteraction[interact.Reply]

	compactions int

	budgetReminderDelivered bool
	activeScope             *Scope
	lastScope               *Scope
}

var testTurnCoordinatorRuntimeFactory func() turnkernel.CoordinatorRuntime

// activeRoute is the route to charge, measure and size the context against.
//
// Everything derived from the model — pricing, the context window, the output
// ceiling — has to come from the same route the request goes to, or a turn on a
// plan model would be budgeted against the act model's window and billed at its
// prices.
func (e *Engine) activeRoute() model.ReadyRoute {
	if scope := e.runningScope(); scope != nil {
		return scope.spec.Route
	}
	return e.options.Route
}

func (e *Engine) recordTurnDiagnostics(receipts []diagnostics.Receipt) {
	if len(receipts) == 0 {
		return
	}
	for _, receipt := range receipts {
		e.observePath(workingset.SourceDiagnostic, receipt.Path)
	}
	scope := e.runningScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.diagnostics = append(scope.state.diagnostics, receipts...)
	scope.mu.Unlock()
}

func (e *Engine) turnDiagnostics() []diagnostics.Receipt {
	scope := e.currentScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return append([]diagnostics.Receipt(nil), scope.state.diagnostics...)
}

func New(options Options) (*Engine, error) {
	if options.Provider == nil {
		return nil, errors.New("provider is required")
	}
	if options.Tools == nil {
		options.Tools = tool.NewRegistry(nil, nil)
	}
	if options.RequireCompletionDeclaration {
		if _, _, _, err := options.Tools.Resolve("turn_complete"); err != nil {
			return nil, fmt.Errorf(
				"completion declaration requires turn_complete: %w", err,
			)
		}
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
		options.MaxSteps = 256
	}
	if options.MaxSteps < 1 {
		return nil, errors.New("max steps must be positive")
	}
	if options.MaxRetries < 0 {
		return nil, errors.New("max retries cannot be negative")
	}
	if options.TokenEstimator == nil {
		options.TokenEstimator = HeuristicTokenEstimator{}
	}
	if options.Metrics == nil {
		options.Metrics = noopMetrics{}
	}
	if options.Security == nil {
		options.Security = policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	}
	if options.ProfileRevision == 0 {
		options.ProfileRevision = 1
	}
	if options.TurnCoordinatorRuntime == nil {
		if testTurnCoordinatorRuntimeFactory == nil {
			return nil, errors.New("turn coordinator runtime is required")
		}
		options.TurnCoordinatorRuntime =
			testTurnCoordinatorRuntimeFactory()
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
		profileReadOnly: profileReadOnlyFromOptions(options),
		turnIDs:         make(map[string]uint64),
		appliedDeltas:   make(map[string]string),
		working:         workingset.New(),
		evidence:        evidence.New(),
		failures:        compact.NewFailures(),
	}
	engine.seedWorkingSet()
	engine.lastScope = &Scope{engine: engine, state: newScopeState(engine)}
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
	engine.configureApprovalHandlers()
	return engine, nil
}

func (e *Engine) ValidateSessionProfile(profile protocol.SessionProfile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.runningScope() != nil {
		return errors.New("session profile cannot change while a turn is active")
	}
	route := e.options.Routes.Act()
	if profile.Provider != route.ProviderID() || profile.Model != route.Model().ID {
		return errors.New("session profile route is unavailable in this runtime")
	}
	if profile.ReasoningEffort != "" && !route.Model().Capabilities.Reasoning {
		return errors.New("session profile model does not support reasoning effort")
	}
	if len(profile.EnabledToolIDs) != 0 {
		for _, id := range profile.EnabledToolIDs {
			if _, _, ok := tool.ParseCatalogToolID(id); !ok {
				return fmt.Errorf("session profile tool id %q is invalid", id)
			}
		}
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
	if e.options.FixedReasoningEffort != "" {
		e.options.ReasoningEffort = e.options.FixedReasoningEffort
	}
	e.options.MaxSteps = profile.MaxSteps
	e.options.ProfileRevision = profile.Revision
	e.enabledTools = make(map[string]struct{}, len(profile.EnabledToolIDs))
	for _, id := range profile.EnabledToolIDs {
		e.enabledTools[id] = struct{}{}
	}
	e.options.PromptCacheKey = fmt.Sprintf(
		"%s-profile-%d",
		e.promptCacheBase,
		profile.PromptCacheRevision,
	)
	if e.options.Security != nil {
		e.options.Security.Mode = policy.Mode(profile.Mode)
		e.options.Security.Permission = effectiveProfilePermissionWithCeiling(
			e.profileReadOnly,
			policy.Permission(profile.ApprovalPosture),
			profilePermissionCeiling(e.options),
		)
	}
	e.refreshPromptMode(profile.Mode)
	return nil
}

func (e *Engine) toolEnabled(entry tool.CatalogEntrySnapshot) bool {
	if e.options.RequireCompletionDeclaration && entry.Name == "turn_complete" {
		return true
	}
	if len(e.enabledTools) == 0 {
		return true
	}
	id := tool.CatalogToolID(entry.Name, entry.Source)
	_, enabled := e.enabledTools[id]
	return enabled
}

func (e *Engine) toolCallEnabled(
	name string,
	binding tool.CatalogBinding,
) bool {
	if e.options.RequireCompletionDeclaration && name == "turn_complete" {
		return true
	}
	if len(e.enabledTools) == 0 {
		return true
	}
	id, err := e.options.Tools.ResolveCatalogToolID(name, binding)
	if err != nil {
		// Guard owns stale, revoked, and unknown binding classification.
		return true
	}
	_, enabled := e.enabledTools[id]
	return enabled
}

func effectiveProfilePermission(
	readOnly bool,
	requested policy.Permission,
) policy.Permission {
	if readOnly {
		return policy.PermissionNever
	}
	return requested
}

func effectiveProfilePermissionWithCeiling(
	readOnly bool,
	requested policy.Permission,
	ceiling policy.Permission,
) policy.Permission {
	return policy.TightenPermission(
		effectiveProfilePermission(readOnly, requested),
		ceiling,
	)
}

func profilePermissionCeiling(options Options) policy.Permission {
	ceiling := options.ProfilePermissionCeiling
	if ceiling == "" && options.Security != nil {
		ceiling = options.Security.Permission
	}
	return ceiling
}

func profileReadOnlyFromOptions(options Options) bool {
	return profilePermissionCeiling(options) == policy.PermissionNever
}

func (e *Engine) SetPolicyMode(mode policy.Mode) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.options.Security != nil {
		e.options.Security.Mode = mode
	}
	e.refreshPromptMode(string(mode))
}

func (e *Engine) refreshPromptMode(mode string) {
	e.options.PromptContext, e.options.ContextReceipts = promptcontext.RefreshMode(
		e.options.PromptContext,
		e.options.ContextReceipts,
		mode,
		e.options.ModePromptBudget,
	)
}

func (e *Engine) SetPermission(permission policy.Permission) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.options.Security != nil {
		e.options.Security.Permission = effectiveProfilePermissionWithCeiling(
			e.profileReadOnly,
			permission,
			profilePermissionCeiling(e.options),
		)
	}
}

func (e *Engine) SetGranular(granular policy.Granular) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.options.Security != nil {
		e.options.Security.Granular = granular
	}
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
