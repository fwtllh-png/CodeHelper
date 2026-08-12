package wire

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	handletool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/handle"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// buildState exists only while NewExec assembles a Session. Runtime components
// receive explicit dependencies and must never retain this construction state.
type buildState struct {
	options ExecOptions
	session *Session

	config        configBuildState
	provider      providerBuildState
	platform      platformBuildState
	persistence   persistenceBuildState
	tools         toolBuildState
	security      securityBuildState
	orchestration orchestrationBuildState
	agent         agentBuildState
	runtime       runtimeBuildState
}

type configBuildState struct {
	snapshot            config.Snapshot
	execution           config.Execution
	extensionPaths      ExtensionPaths
	hookSessionID       string
	diagnosticCommands  map[string]diagnostics.Command
	diagnosticReadRoots []string
	diagnosticReadFiles []string
}

type providerBuildState struct {
	routes      model.RouteSet
	route       model.ReadyRoute
	egress      *egress.Gate
	client      *httpclient.Client
	toolSampler *agentengine.ToolSampler
}

type platformBuildState struct {
	helperPath string
	backend    sandbox.Backend
	web        webtool.Options
}

type persistenceBuildState struct {
	repositoryIndex *repoindex.Index
	workflowRuns    workflowRunStore
}

type toolBuildState struct {
	registry     *tool.Registry
	handleStore  *handletool.Store
	skillCatalog *skill.Catalog
}

type securityBuildState struct {
	runtime     *policy.Runtime
	journal     *workspacejournal.Manager
	diagnostics diagnostics.Runner
	verify      verify.Runner
	guard       *toolguard.Guard
}

type orchestrationBuildState struct {
	sharedGovernor *rlm.Governor
	childGovernor  *rlm.Governor
	children       *childRuntime
	childToolsets  *childToolsets
	chatTrees      *childWorktrees
	parentFiles    *filetool.Tools
	subagents      *subagent.Manager
}

type agentBuildState struct {
	workspaceTurnGate   *agentengine.WorkspaceTurnGate
	coordinatorRuntime  turnkernel.CoordinatorRuntime
	seedOptions         agentengine.Options
	defaultProfile      protocol.SessionProfile
	profileCapabilities protocol.SessionProfileCapabilities
	threads             *app.ThreadManager
}

type runtimeBuildState struct {
	application *app.Runtime
}

type buildModule interface {
	Name() string
	Build(context.Context, *buildState) error
}

func buildModules(
	ctx context.Context,
	state *buildState,
	modules ...buildModule,
) error {
	for _, module := range modules {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := module.Build(ctx, state); err != nil {
			return &moduleBuildError{name: module.Name(), err: err}
		}
	}
	return nil
}

type moduleBuildError struct {
	name string
	err  error
}

func (e *moduleBuildError) Error() string {
	return "build wire module " + e.name + ": " + e.err.Error()
}

func (e *moduleBuildError) Unwrap() error { return e.err }
