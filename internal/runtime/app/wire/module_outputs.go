package wire

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/admission"
	workbudget "github.com/fwtllh-png/CodeHelper/internal/orchestration/budget"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type providerBuildState struct {
	routes            model.RouteSet
	route             model.ReadyRoute
	selectableRoutes  map[string]model.ReadyRoute
	egress            *egress.Gate
	provider          provider.Provider
	toolSampler       *agentengine.ToolSampler
	providerCatalog   protocol.ProviderCatalog
	modelCatalog      protocol.ModelCatalog
	modelCapabilities protocol.ModelCapabilities
}

type platformBuildState struct {
	helperPath      string
	backend         sandbox.Backend
	web             webtool.Options
	processes       *process.SessionManager
	leaseAuthority  *toolguard.LeaseAuthority
	repositoryIndex *repoindex.Index
}

type persistenceBuildState struct {
	content        *contentstore.Memory
	jobLogs        *joblog.Store
	runtimeStore   *sqlitestate.Store
	ephemeralState *sqlitestate.Store
}

type extensionBuildState struct {
	receipts     []ContributionReceipt
	registry     *runtimeextension.Registry
	skillCatalog *skill.Catalog
	memory       *memory.Store
	hooks        *hooks.Manager
	mcpPool      *mcpruntime.Pool
	mcpPrewarm   *MCPPrewarm
	dynamicTools *dynamictool.Manager
}

type securityBuildState struct {
	runtime      *policy.Runtime
	journal      *workspacejournal.Manager
	constitution constitution.Bundle
	permissions  *permissions.Store
	guardFactory guardFactory
	diagnostics  diagnostics.Runner
	verify       verify.Runner
	guard        *toolguard.Guard
}

type orchestrationBuildState struct {
	workBudget    *workbudget.Ledger
	childGovernor *admission.Governor
	children      *childRuntime
	childToolsets *childToolsets
	chatTrees     *childWorktrees
	parentFiles   *filetool.Tools
	subagents     *subagent.AgentControl
}
