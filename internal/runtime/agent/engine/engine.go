package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextview"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
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
	AwaitingRecovery State = "awaiting_recovery"
	Completed        State = "completed"
	Failed           State = "failed"
	Canceled         State = "canceled"
)

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

type Engine struct {
	mu              sync.Mutex
	scopeMu         sync.Mutex
	options         Options
	history         []provider.Message
	viewFold        viewFoldState
	mailboxHold     []PendingInput
	turn            uint64
	usage           provider.Usage
	costUSD         float64
	sessionRevision uint64
	stateEpoch      uint64
	appliedDeltas   map[string]string
	guard           *toolguard.Guard
	journal         *workspacejournal.Manager
	turnIDs         map[string]uint64
	historyTurns    map[string]uint64
	planMu          sync.Mutex
	planText        string
	plan            interact.Plan
	planReceipt     *promptcontext.Receipt

	context         agentcontext.Authority
	prefixMu        sync.Mutex
	prefixManifest  contextview.PrefixManifest
	promptCacheBase string
	profileReadOnly bool
	enabledTools    map[string]struct{}

	approvalRecovery turnkernel.RecoveredInteraction[toolguard.ApprovalDecision]
	inputRecovery    turnkernel.RecoveredInteraction[interact.Reply]

	activeScope *Scope
	lastScope   *Scope
}

var testTurnCoordinatorRuntimeFactory func() turnkernel.CoordinatorRuntime

// activeRoute is the single source for sampling, limits, and pricing.
func (e *Engine) activeRoute() model.ReadyRoute {
	if scope := e.runningScope(); scope != nil {
		if route := scope.spec.Route; route.Validate() == nil {
			return route
		}
	}
	return e.options.Route
}

func (e *Engine) recordTurnDiagnostics(receipts []diagnostics.Receipt) {
	if len(receipts) == 0 {
		return
	}
	for _, receipt := range receipts {
		e.contextAuthority().ObservePath(
			e.options.Workspace,
			agentcontext.SourceDiagnostic,
			e.turn,
			receipt.Path,
		)
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
	if options.MaxSteps < 0 {
		return nil, errors.New("max steps must be non-negative")
	}
	if err := normalizeEngineOptions(&options); err != nil {
		return nil, err
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
	options.StaticContext = cloneMessages(options.StaticContext)
	options.StaticContextReceipts = append(
		[]promptcontext.Receipt(nil),
		options.StaticContextReceipts...,
	)
	options.ContextBudgets = cloneContextBudgets(options.ContextBudgets)
	options.Tools.SetMaterializeLimits(
		tool.DefaultMaxMaterialized, tool.DefaultMaxMaterializedSchemaBytes,
	)
	window, err := agentcontext.CreateWindowLedger(1)
	if err != nil {
		return nil, fmt.Errorf("create token window: %w", err)
	}
	engine := &Engine{
		options: options, guard: options.Guard, journal: options.Journal,
		promptCacheBase: options.PromptCacheKey,
		profileReadOnly: profileReadOnlyFromOptions(options),
		turnIDs:         make(map[string]uint64),
		appliedDeltas:   make(map[string]string),
		stateEpoch:      1,
		context:         agentcontext.NewAuthority(),
	}
	engine.context.SetWindow(window)
	engine.seedWorkingSet()
	engine.lastScope = &Scope{engine: engine, state: newScopeState(engine)}
	if engine.guard == nil {
		var guard *toolguard.Guard
		if options.GuardFactory != nil {
			guard, err = options.GuardFactory(context.Background())
		} else {
			guard, err = toolguard.New(toolguard.Options{
				Registry: options.Tools, Policy: options.Security,

				Now: options.Observability.Now, Diagnostics: options.Diagnostics,
				OnNetworkAllow: options.OnNetworkAllow, Workspace: options.Workspace,
				ReadTracker: options.ReadTracker, Journal: options.Journal,
			})
		}
		if err != nil {
			return nil, err
		}
		engine.guard = guard
	}
	engine.configureApprovalHandlers()
	return engine, nil
}

func (e *Engine) ApplySessionProfile(profile protocol.SessionProfile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.validateSessionProfileLocked(profile); err != nil {
		return err
	}
	routes, err := e.routesForProfileLocked(profile)
	if err != nil {
		return err
	}
	routeChanged := tokenWindowRouteChanged(
		e.options.Routes.Act(),
		routes.Act(),
	)
	e.options.Routes = routes
	e.options.Route = routes.Act()
	if routeChanged {
		current := e.context.Window()
		next, windowErr := agentcontext.CreateWindowLedger(current.Number + 1)
		if windowErr != nil {
			next = agentcontext.FallbackWindowLedger(
				current,
				fmt.Sprintf(
					"%s:%s:%s",
					e.options.SessionID,
					routes.Act().ProviderID(),
					routes.Act().Model().ID,
				),
			)
		}
		e.context.SetWindow(next)
		compaction := e.context.Compaction()
		if compaction.State != nil &&
			compaction.State.Phase != "completed" {
			compaction.State = nil
			e.context.SetCompaction(compaction)
		}
	}
	e.options.ReasoningEffort = profile.ReasoningEffort
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
	e.applySessionPolicyLocked(profile)
	return nil
}

func tokenWindowRouteChanged(current, next model.ReadyRoute) bool {
	if current.Validate() != nil || next.Validate() != nil {
		return true
	}
	currentModel := current.Model()
	nextModel := next.Model()
	return current.ProviderID() != next.ProviderID() ||
		current.Adapter() != next.Adapter() ||
		current.Protocol() != next.Protocol() ||
		currentModel.ID != nextModel.ID ||
		currentModel.WireID != nextModel.WireID ||
		currentModel.Limits.ContextTokens != nextModel.Limits.ContextTokens ||
		currentModel.Limits.MaxOutputTokens != nextModel.Limits.MaxOutputTokens
}

func cloneContextBudgets(
	values map[string]promptcontext.Budget,
) map[string]promptcontext.Budget {
	cloned := make(map[string]promptcontext.Budget, len(values))
	for kind, budget := range values {
		cloned[kind] = budget
	}
	return cloned
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
		ceiling = options.Security.PermissionValue()
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
		e.options.Security.SetMode(mode)
	}
}

func (e *Engine) SetPermission(permission policy.Permission) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.options.Security != nil {
		e.options.Security.SetPermissionWithinCeiling(
			effectiveProfilePermission(e.profileReadOnly, permission),
			e.options.ProfilePermissionCeiling,
		)
	}
}

func (e *Engine) SetGranular(granular policy.Granular) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.options.Security != nil {
		e.options.Security.SetGranular(granular)
	}
}

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

func (e *Engine) OptionsSeed() Options {
	e.mu.Lock()
	defer e.mu.Unlock()
	options := e.options
	options.Guard = nil
	return options
}
