package wire

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	interacttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire/assembly"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type ExecOptions struct {
	ConfigPath          string
	ConfigOverrides     config.Overrides
	BaseURL             string
	APIKeyEnv           string
	FixturePath         string
	Permission          string
	RepositoryRulesPath string
	PluginBundle        string
	PluginReceipt       string
	MCPConfigPath       string
	MetricsPath         string
	LogPath             string
	ModelMetadata       ModelMetadataOptions
	WorkingSet          []ContextFile
	PromptBudgets       map[string]promptcontext.Budget
	PersistentStore     *state.Store
	Extensions          ExtensionOptions
	// TrustProbe lets probe "supported" observations widen catalog capabilities.
	// Without it, probes may only tighten.
	TrustProbe bool
	// TrustedDynamicTools exposes the host-managed dynamic catalog. It is off by
	// default and requires the normal tool/Guard runtime.
	TrustedDynamicTools bool
	// ForceEditPlanApproval is enabled by interactive editor hosts that must
	// preview every workspace write regardless of broader policy grants.
	ForceEditPlanApproval bool
	// WorkspaceIdentity binds editor-visible URI identity for editor hosts.
	// Non-editor hosts leave it empty and retain local file URI behavior.
	WorkspaceIdentity protocol.WorkspaceIdentity
}

// ContextFile is a file a host named for the session (`exec --file`, an editor
// attachment). Its contents go into the prompt once at bootstrap, and its path
// stays in the working set for the rest of the session.
//
// Hosts express this in wire's own vocabulary rather than the agent's, because a
// host must not depend on execution packages.
type ContextFile struct {
	Path string
	// Content replaces what is read from disk, for a buffer the user has not
	// saved. Nil means read the file.
	Content *string
	// Critical pins the path: it never decays out of the working set.
	Critical bool
}

type Session struct {
	Runtime *app.Runtime

	metrics              *telemetry.Metrics
	metricsPath          string
	logger               *slog.Logger
	logFile              *os.File
	fixture              *fixture.Server
	plugins              []*pluginruntime.Loaded
	pluginRegistry       *pluginruntime.Registry
	pluginTools          *plugintool.Adapter
	contributionReceipts []ContributionReceipt
	dynamicTools         *dynamictool.Manager
	providerID           string
	modelID              string
	modelCapabilities    protocol.ModelCapabilities
	providerCatalog      protocol.ProviderCatalog
	modelCatalog         protocol.ModelCatalog
	processes            *process.SessionManager
	jobLogs              *joblog.Store
	mcpPool              *mcpruntime.Pool
	mcpPrewarm           *MCPPrewarm
	sandbox              sandbox.Backend
	content              *contentstore.Memory
	memory               *memory.Store
	automations          *automation.Repository
	tasks                *taskstate.Repository
	ephemeralTasks       *sqlitestate.Store
	hooks                *hooks.Manager
	inputHost            *interacttool.Host
	applyPlan            func(interacttool.Plan)
	security             *policy.Runtime
	rlmStore             *rlm.Store
	children             *childRuntime
	childTools           *childToolsets
	chatWorkspaces       *chatWorkspaces
	threads              *app.ThreadManager
	turnCoordinators     *durableCoordinatorRuntime
	journal              *workspacejournal.Manager
	journalRecovery      workspacejournal.Recovery
	subagents            *subagent.AgentControl
	scheduler            *worker.Scheduler
	Constitution         constitution.Status
	constitutionPrompt   string
	resources            *assembly.ResourceStack
	closeOnce            sync.Once
	closeErr             error
}

func NewExec(ctx context.Context, options ExecOptions) (_ *Session, resultErr error) {
	return newExec(ctx, options, defaultBuildModules())
}

func defaultBuildModules() []buildModule {
	return []buildModule{
		configModule{},
		providerModule{},
		persistenceModule{},
		platformModule{},
		builtinToolsModule{},
		newExtensionToolsModule(),
		securityModule{},
		orchestrationModule{},
		agentModule{},
		runtimeModule{},
		backgroundModule{},
	}
}

func newExec(
	ctx context.Context,
	options ExecOptions,
	modules []buildModule,
) (_ *Session, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session := &Session{
		metrics: telemetry.NewMetrics(), metricsPath: options.MetricsPath,
		resources: assembly.NewResourceStack(),
	}
	if err := session.registerResourceClosers(); err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = session.resources.Close(context.Background())
		}
	}()
	state := &buildState{options: options, session: session}
	if err := buildModules(ctx, state, modules...); err != nil {
		return nil, err
	}
	return session, nil
}

func configuredDiagnosticCommands(
	configured map[string]config.DiagnosticCommand,
) map[string]diagnostics.Command {
	commands := make(map[string]diagnostics.Command, len(configured))
	for extension, command := range configured {
		commands[extension] = diagnostics.Command{
			Name: command.Name,
			Args: append([]string(nil), command.Args...),
		}
	}
	return commands
}

func diagnosticCommandReadRoots(
	commands map[string]diagnostics.Command,
) []string {
	seen := make(map[string]struct{})
	addExecutableTree := func(name string, includePackageRoot bool) {
		path, err := exec.LookPath(name)
		if err != nil {
			return
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return
		}
		root := filepath.Dir(resolved)
		if includePackageRoot {
			root = filepath.Dir(root)
		}
		seen[root] = struct{}{}
	}
	for _, command := range commands {
		path, err := exec.LookPath(command.Name)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			continue
		}
		seen[filepath.Dir(resolved)] = struct{}{}
		for _, dependency := range diagnosticDependencyReadFiles(resolved) {
			seen[filepath.Dir(dependency)] = struct{}{}
		}
		switch filepath.Ext(resolved) {
		case ".js", ".cjs", ".mjs":
			addExecutableTree("node", true)
			node, nodeErr := exec.LookPath("node")
			if nodeErr == nil {
				for _, dependency := range diagnosticDependencyReadFiles(node) {
					seen[filepath.Dir(dependency)] = struct{}{}
				}
			}
		}
	}
	roots := make([]string, 0, len(seen))
	for root := range seen {
		roots = append(roots, root)
	}
	slices.Sort(roots)
	return roots
}

func diagnosticCommandReadFiles(
	commands map[string]diagnostics.Command,
) []string {
	seen := make(map[string]struct{})
	add := func(name string) {
		path, err := exec.LookPath(name)
		if err != nil {
			return
		}
		for _, dependency := range diagnosticDependencyReadFiles(path) {
			seen[dependency] = struct{}{}
		}
	}
	for _, command := range commands {
		add(command.Name)
		path, err := exec.LookPath(command.Name)
		if err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err == nil {
			switch filepath.Ext(resolved) {
			case ".js", ".cjs", ".mjs":
				add("node")
			}
		}
	}
	files := make([]string, 0, len(seen))
	for path := range seen {
		files = append(files, path)
	}
	slices.Sort(files)
	return files
}

func cloneThreadSecurity(source *policy.Runtime) *policy.Runtime {
	if source == nil {
		return nil
	}
	cloned := source.CloneSampling()
	cloned.Approvals = policy.NewApprovalCache()
	return cloned
}

func adaptEngine(
	engine *agentengine.Engine,
	identity protocol.WorkspaceIdentity,
) *app.EngineAdapter {
	if identity.Version == 0 {
		return app.AdaptEngine(engine)
	}
	return app.AdaptEngineWithWorkspaceIdentity(engine, identity)
}

// childEngineOptions derives a child agent's Engine from the host template.
// A child gets no InputHost because the emitter is a single shared slot and gets
// its own step/spend budget. Isolated and ordinary read-only children do not
// inherit the host journal or turn gate. A same-workspace child deliberately
// inherits the gate, and a writable one can then safely reuse the host journal.
func childEngineOptions(
	seed agentengine.Options, spec app.ChildSpec, security *policy.Runtime,
) agentengine.Options {
	options := seed
	options.Guard = nil
	options.InputHost = nil
	options.ReadTracker = nil
	options.Security = cloneThreadSecurity(security)
	if !spec.Serialized {
		options.Journal = nil
		options.WorkspaceTurnGate = nil
	} else if spec.ReadOnly {
		options.Journal = nil
	} else {
		options.ReadTracker = workspacejournal.NewReadTracker()
	}
	if spec.MaxSteps > 0 {
		options.MaxSteps = spec.MaxSteps
	}
	if spec.MaxTokens > 0 {
		options.Budget.MaxTokens = spec.MaxTokens
	}
	if spec.MaxCostUSD > 0 {
		options.Budget.MaxCostUSD = spec.MaxCostUSD
	}
	if spec.Workspace != "" {
		options.Workspace = spec.Workspace
		options.WorkspaceIsolation = app.SessionIsolationWorktree
	}
	if spec.ReadOnly && !spec.CanDelegate && options.Security != nil {
		// Plan mode is the existing, tested read-only enforcement: everything
		// that is not a read capability is denied with mode_denied. A read-only
		// stance that only shaped the prompt would not be a stance at all.
		options.Security.Mode = policy.ModePlan
		options.Security.Permission = policy.PermissionNever
	}
	if options.Security != nil {
		options.ProfilePermissionCeiling = options.Security.Permission
	}
	return options
}

func restrictChildTools(
	security *policy.Runtime,
	spec app.ChildSpec,
	parent, child *tool.Registry,
) {
	if security == nil || child == nil {
		return
	}
	parentTools := make(map[string]struct{})
	if parent != nil {
		for _, descriptor := range allToolDescriptors(parent) {
			parentTools[descriptor.Name] = struct{}{}
		}
	}
	for _, descriptor := range allToolDescriptors(child) {
		_, inherited := parentTools[descriptor.Name]
		if inherited && childRoleAllowsTool(spec, descriptor) {
			continue
		}
		security.Grants = append(security.Grants, policy.Rule{
			Tool: descriptor.Name, Resource: "*", Action: policy.ActionDeny,
			Code: "child_authority_denied",
		})
	}
}

func allToolDescriptors(registry *tool.Registry) []tool.Descriptor {
	var result []tool.Descriptor
	for _, visibility := range []tool.Visibility{
		tool.VisibleModel, tool.VisibleInternal, tool.VisibleHidden,
	} {
		result = append(result, registry.Descriptors(visibility)...)
	}
	return result
}

func childRoleAllowsTool(spec app.ChildSpec, descriptor tool.Descriptor) bool {
	if isAgentLifecycleTool(descriptor.Name) {
		return spec.CanDelegate
	}
	if len(spec.AllowedTools) == 0 {
		return true
	}
	for _, allowed := range spec.AllowedTools {
		switch allowed {
		case "read", "search":
			if descriptor.Capability == tool.CapabilityRead {
				return true
			}
		case "verify":
			if descriptor.Capability == tool.CapabilityProcess {
				return true
			}
		case descriptor.Name:
			return true
		}
	}
	return descriptor.Name == "turn_complete" || descriptor.Name == "result_get"
}

func isAgentLifecycleTool(name string) bool {
	switch name {
	case "spawn_agent", "send_message", "wait_agent", "list_agents",
		"followup_task", "interrupt_agent", "close_agent", "integrate_agent":
		return true
	default:
		return false
	}
}
