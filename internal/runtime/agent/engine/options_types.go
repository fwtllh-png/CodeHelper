package engine

import (
	"context"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type ProviderConfig struct {
	Provider         provider.Provider
	Route            model.ReadyRoute
	Routes           model.RouteSet
	SelectableRoutes map[string]model.ReadyRoute

	MaxOutputTokens uint64
	MaxSteps        int
	MaxRetries      int
	MaxRetryDelay   time.Duration
	ReasoningEffort string
	NativeSearch    bool
	TokenEstimator  TokenEstimator
}

type ContextConfig struct {
	StaticContext         []provider.Message
	ContextBudgets        map[string]promptcontext.Budget
	CodingPolicy          bool
	SummaryMaxBytes       int
	MaxDigestEntries      int
	Context               ContextPolicy
	Budget                Budget
	WorkingSet            []string
	CriticalPaths         []string
	StaticContextReceipts []promptcontext.Receipt
	PromptCacheKey        string
	TurnSnapshots         TurnSnapshotSources
	RepoContext           RepoContext
	WorkingSetLimit       int
	EvidenceLimit         int
}

type ToolConfig struct {
	Tools          *tool.Registry
	Authorize      func(provider.ToolCall) bool
	Guard          *toolguard.Guard
	GuardFactory   func(context.Context) (*toolguard.Guard, error)
	OnNetworkAllow toolguard.NetworkAllow
	Diagnostics    diagnostics.Runner
	Verify         VerifyOptions

	RequireCompletionDeclaration bool
	MaxToolConcurrent            int
	MaxToolStreamBytes           int
	MaxToolDefinitions           int
	MaxToolSchemaBytes           int
	ToolCatalogSync              func() error
}

type SecurityConfig struct {
	Security                 *policy.Runtime
	ProfilePermissionCeiling policy.Permission
	Workspace                string
	WorkspaceIdentity        string
	WorkspaceIsolation       string
	Journal                  *workspacejournal.Manager
	ReadTracker              *workspacejournal.ReadTracker
	WorkspaceTurnGate        *WorkspaceTurnGate
}

type TelemetryConfig struct {
	Metrics            Metrics
	Observability      trace.Runtime
	TurnKernelObserver func(turnkernel.TransitionRecord)
}

type LifecycleConfig struct {
	TurnCoordinatorRuntime turnkernel.CoordinatorRuntime
	ReleaseTurnResources   func(TurnIdentity)
	SessionID              string
	DelegationMode         string
	InputHost              *interact.Host
	ProfileRevision        uint64
}

type Options struct {
	ProviderConfig
	ContextConfig
	ToolConfig
	SecurityConfig
	TelemetryConfig
	LifecycleConfig
}
