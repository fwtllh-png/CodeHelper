package wire

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/extension"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agenttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/agent"
	automationtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/automation"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/builtin"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	handletool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/handle"
	interacttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	memorytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/memory"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	reverttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/revert"
	rlmtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/rlm"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	tasktool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/task"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/persist/repoindex"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
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

	metrics            *telemetry.Metrics
	metricsPath        string
	logger             *slog.Logger
	logFile            *os.File
	fixture            *fixture.Server
	plugins            []*pluginruntime.Loaded
	pluginRegistry     *pluginruntime.Registry
	pluginTools        *plugintool.Adapter
	dynamicTools       *dynamictool.Manager
	providerID         string
	modelID            string
	processes          *process.SessionManager
	jobLogs            *joblog.Store
	mcpPool            *mcpruntime.Pool
	mcpPrewarm         *MCPPrewarm
	sandbox            sandbox.Backend
	content            *contentstore.Memory
	memory             *memory.Store
	automations        *automation.Repository
	tasks              *taskstate.Repository
	ephemeralTasks     *sqlitestate.Store
	hooks              *hooks.Manager
	inputHost          *interacttool.Host
	applyPlan          func(interacttool.Plan)
	security           *policy.Runtime
	rlmStore           *rlm.Store
	children           *childRuntime
	childTools         *childToolsets
	chatWorkspaces     *chatWorkspaces
	threads            *app.ThreadManager
	journal            *workspacejournal.Manager
	journalRecovery    workspacejournal.Recovery
	subagents          *subagent.Manager
	scheduler          *worker.Scheduler
	Constitution       constitution.Status
	constitutionPrompt string
	closeOnce          sync.Once
	closeErr           error
}

func NewExec(ctx context.Context, options ExecOptions) (_ *Session, resultErr error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snapshot, err := config.Load(config.LoadOptions{
		Path: options.ConfigPath, Overrides: options.ConfigOverrides,
	})
	if err != nil {
		return nil, fmt.Errorf("load execution config: %w", err)
	}
	execution := snapshot.Config.Execution
	if !execution.Tools &&
		(options.RepositoryRulesPath != "" || options.PluginBundle != "" ||
			options.PluginReceipt != "" || options.MCPConfigPath != "") {
		return nil, errors.New("repository policy, plugins, and MCP require tools to be enabled")
	}
	session := &Session{
		metrics: telemetry.NewMetrics(), metricsPath: options.MetricsPath,
	}
	hookSessionID := fmt.Sprintf("process-%d-%p", os.Getpid(), session)
	extensionPaths, err := ResolveExtensionPaths(options.Extensions, execution.Workspace)
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = session.closeResources(context.Background(), false)
		}
	}()

	if options.FixturePath != "" {
		path, err := resolveFixturePath(options.FixturePath)
		if err != nil {
			return nil, fmt.Errorf("provider fixture: %w", err)
		}
		server, err := fixture.Start(path)
		if err != nil {
			return nil, fmt.Errorf("provider fixture: %w", err)
		}
		session.fixture = server
		execution.Provider = "fixture"
		execution.Model = server.Config.Model
		options.BaseURL = server.URL
		execution.Protocol = string(server.Config.Protocol)
		options.APIKeyEnv = ""
	}
	session.providerID, session.modelID = execution.Provider, execution.Model
	if options.LogPath != "" {
		logFile, err := os.OpenFile(options.LogPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("open exec log: %w", err)
		}
		session.logFile = logFile
		var secrets []string
		if options.APIKeyEnv != "" {
			if value, exists := os.LookupEnv(options.APIKeyEnv); exists {
				secrets = append(secrets, value)
			}
		}
		session.logger = telemetry.NewJSONLogger(
			logFile, slog.LevelInfo, telemetry.NewRedactor(secrets...),
		)
		session.logger.Info("exec started", "provider", execution.Provider, "model", execution.Model)
	}

	wireProtocol, err := parseProtocol(execution.Protocol)
	if err != nil {
		return nil, err
	}
	var routeModel *model.Model
	if session.fixture != nil {
		routeModel = fixtureModel(execution.Model)
	} else if options.BaseURL != "" {
		routeModel, err = resolveModelMetadata(execution.Model, options.ModelMetadata)
		if err != nil {
			return nil, fmt.Errorf("model metadata: %w", err)
		}
	}
	credential := model.CredentialRef{
		Kind: snapshot.Config.Credential.Kind,
		Name: snapshot.Config.Credential.Name,
	}
	if session.fixture != nil {
		credential = model.CredentialRef{}
	}
	routes, err := resolveRouteSet(routeSetOptions{
		Act: execRouteOptions{
			ProviderID: execution.Provider, ModelID: execution.Model, BaseURL: options.BaseURL,
			Protocol: wireProtocol, APIKeyEnv: options.APIKeyEnv,
			Credential: credential,
			Fixture:    session.fixture != nil, Model: routeModel,
		},
		Slots: snapshot.Config.Route.Slots, Lock: snapshot.Config.Route.Lock,
	})
	if err != nil {
		return nil, fmt.Errorf("exec route: %w", err)
	}
	routes, err = overlayProbeCapabilities(ctx, routes, options.PersistentStore, options.TrustProbe)
	if err != nil {
		return nil, fmt.Errorf("capability probe overlay: %w", err)
	}
	route := routes.Act()
	egressGate := &egress.Gate{Enforce: true}
	grantRouteHosts(egressGate, routes)
	client := httpclient.New()
	client.Egress = egressGate
	// Tools that sample a model do it through the sampler rather than the client
	// directly, so their tokens, cost and latency land in the turn that asked for
	// them instead of nowhere.
	toolSampler := agentengine.NewToolSampler(client)
	client.Metrics = session.metrics
	client.HTTP.Timeout = execution.Timeout
	client.IdleTimeout = execution.IdleTimeout
	client.MaxConcurrent = execution.MaxConcurrent
	client.RequestsPerSecond = execution.RateLimit

	content := contentstore.NewMemory(contentstore.Options{})
	session.content = content
	registry := tool.NewRegistry(nil, tool.NewResultStoreWithStore(32<<10, content))
	session.processes = process.NewSessionManager(0)
	session.processes.SetJournalPath(filepath.Join(execution.Workspace, ".codehelper", "jobs-journal.jsonl"))
	_ = session.processes.LoadStaleJournal()
	// A job's output outlives the bounded buffer it streams through, so a poller
	// that fell behind can still read what it missed. Failing to open the log
	// leaves jobs working with the buffer alone rather than failing the session.
	if jobs, err := joblog.New(filepath.Join(execution.Workspace, ".codehelper", "jobs")); err == nil {
		session.jobLogs = jobs
		session.processes.SetArchive(jobs)
	}
	var securityRuntime *policy.Runtime
	var journal *workspacejournal.Manager
	var diagnosticRunner diagnostics.Runner
	var verifyRunner verify.Runner
	var skillCatalog *skill.Catalog
	var workflowRuns workflowRunStore
	var guard *toolguard.Guard
	var handleStore *handletool.Store
	// The child agent pieces outlive the tool block: the per-thread child Engine
	// factory and the pump that settles children are installed after the Runtime
	// exists, which is well past the point where the agent tool is registered.
	var sharedGovernor *rlm.Governor
	var subagentManager *subagent.Manager
	var childRT *childRuntime
	var childToolsets *childToolsets
	var chatTrees *childWorktrees
	var parentFiles *filetool.Tools
	// The index outlives the tool block: the repository map appended to every
	// request reads it too, and a session without tools simply has none.
	var repositoryIndex *repoindex.Index
	if execution.Tools {
		helperPath, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve sandbox helper executable: %w", err)
		}
		backend, err := newPlatformBackend(sandbox.Options{
			WorkspaceRoot: execution.Workspace, HelperPath: helperPath,
			AllowNetwork: true,
		})
		if err != nil {
			return nil, fmt.Errorf("create sandbox: %w", err)
		}
		session.sandbox = backend
		webOpts := webtool.OptionsFromEnv()
		if snapshot.Config.Web.SearchBackend != "" {
			webOpts.SearchBackend = snapshot.Config.Web.SearchBackend
		}
		grantWebBackendHosts(egressGate, webOpts)
		webOpts.HTTP = egress.WrapClient(&http.Client{}, egressGate)
		// The state database has to be open before the tools are registered: the
		// symbol tools take an index over it, and a tool registered without one
		// would stay unavailable for the whole session.
		tasks, automations, ephemeral, err := openDurableRepositories(
			ctx, options.PersistentStore, execution.Workspace,
		)
		if err != nil {
			return nil, fmt.Errorf("durable repositories: %w", err)
		}
		session.tasks = tasks
		session.automations = automations
		session.ephemeralTasks = ephemeral
		workflowRuns = newWorkflowRunStore(options.PersistentStore, ephemeral, content)
		index, indexStatus := openRepositoryIndex(
			execution.Workspace, backend, options.PersistentStore, ephemeral,
			snapshot.Config.Context.Index,
		)
		repositoryIndex = index
		session.metrics.SetRepositoryIndexState(indexStatus)
		registry, handleStore, err = builtin.NewWithIndex(
			execution.Workspace, backend, content, session.processes, repositoryIndex, webOpts,
		)
		if err != nil {
			return nil, fmt.Errorf("create tools: %w", err)
		}
		if options.TrustedDynamicTools {
			session.dynamicTools, err = dynamictool.NewManager(
				registry, dynamictool.DefaultRegistrationPolicy(),
			)
			if err != nil {
				return nil, fmt.Errorf("dynamic tool manager: %w", err)
			}
		}
		if options.PluginBundle != "" {
			receipt, err := pluginruntime.LoadReceipt(options.PluginReceipt)
			if err != nil {
				return nil, fmt.Errorf("plugin receipt: %w", err)
			}
			loader, err := pluginruntime.NewLoader(execution.Workspace, backend)
			if err != nil {
				return nil, fmt.Errorf("plugin loader: %w", err)
			}
			loadedPlugin, err := loader.Load(options.PluginBundle, receipt)
			if err != nil {
				return nil, fmt.Errorf("plugin load: %w", err)
			}
			session.plugins = append(session.plugins, loadedPlugin)
			if err := plugintool.Register(registry, loadedPlugin); err != nil {
				return nil, fmt.Errorf("plugin register: %w", err)
			}
		}
		session.pluginRegistry, err = NewPluginRegistry(extensionPaths, execution.Workspace, backend)
		if err != nil {
			return nil, fmt.Errorf("plugin registry: %w", err)
		}
		if err := session.pluginRegistry.Reload(); err != nil {
			return nil, fmt.Errorf("plugin registry reload: %w", err)
		}
		session.pluginTools, err = plugintool.NewAdapter(
			registry, session.pluginRegistry,
		)
		if err != nil {
			return nil, fmt.Errorf("register lifecycle plugin tools: %w", err)
		}
		skillState, err := skill.NewStateStore(extensionPaths.SkillsStatePath)
		if err != nil {
			return nil, fmt.Errorf("skill state: %w", err)
		}
		skillLock, err := skill.NewLockStore(extensionPaths.SkillsLockPath)
		if err != nil {
			return nil, fmt.Errorf("skill lock: %w", err)
		}
		skillCatalog, err = skill.Discover(skill.DiscoveryOptions{
			Workspace: execution.Workspace, ConfiguredDir: extensionPaths.SkillsConfiguredDir,
			UserHome: extensionPaths.UserHome, Locale: extensionPaths.SkillsLocale,
			State: skillState, Lock: skillLock, RuntimeVersion: buildinfo.Version,
		})
		if err != nil {
			return nil, fmt.Errorf("skill discovery: %w", err)
		}
		if err := skillCatalog.Verify(ctx); err != nil {
			return nil, fmt.Errorf("skill lock verify: %w", err)
		}
		if err := skilltool.Register(registry, skillCatalog); err != nil {
			return nil, fmt.Errorf("skill tool: %w", err)
		}
		if err := toolsearch.Register(registry); err != nil {
			return nil, fmt.Errorf("tool_search: %w", err)
		}
		if snapshot.Config.Memory.Enabled {
			session.memory, err = memory.Open(snapshot.Config.Memory.Path)
			if err != nil {
				return nil, fmt.Errorf("memory store: %w", err)
			}
			mem := session.memory
			ext := extension.NewRegistry(extension.FuncContributor{
				ID: "memory",
				Func: func(reg *tool.Registry) error {
					return memorytool.Register(reg, mem)
				},
			})
			if err := ext.ContributeAll(registry); err != nil {
				return nil, fmt.Errorf("remember tool: %w", err)
			}
		}
		if err := tasktool.Register(registry, tasktool.Options{
			Repository: tasks, SessionID: hookSessionID,
			Workspace: execution.Workspace, Backend: backend,
		}); err != nil {
			return nil, fmt.Errorf("task tools: %w", err)
		}
		if err := automationtool.Register(registry, automationtool.Options{
			Repository: automations, SessionID: hookSessionID,
			Workspace: execution.Workspace,
		}); err != nil {
			return nil, fmt.Errorf("automation tools: %w", err)
		}
		if _, err := session.automations.Tick(ctx, time.Time{}); err != nil {
			return nil, fmt.Errorf("automation reconcile: %w", err)
		}
		hooksPathExplicit := options.Extensions.HooksConfigPath != ""
		if hookInfo, statErr := os.Lstat(extensionPaths.HooksConfigPath); statErr == nil {
			if hookInfo.Mode()&os.ModeSymlink != 0 || !hookInfo.Mode().IsRegular() {
				return nil, errors.New("hooks config must be a regular non-symlink file")
			}
			hookConfig, loadErr := hooks.LoadConfig(extensionPaths.HooksConfigPath)
			if loadErr != nil {
				return nil, fmt.Errorf("hooks config: %w", loadErr)
			}
			session.hooks, err = hooks.New(hookConfig, hooks.Options{
				Workspace: execution.Workspace, Sandbox: backend, RequireStrongSandbox: true,
			})
			if err != nil {
				return nil, fmt.Errorf("hooks manager: %w", err)
			}
		} else if hooksPathExplicit || !errors.Is(statErr, os.ErrNotExist) {
			return nil, fmt.Errorf("hooks config: %w", statErr)
		}
		if options.MCPConfigPath != "" {
			var prewarm *MCPPrewarm
			session.mcpPool, prewarm, err = RegisterMCPTools(ctx, registry, options.MCPConfigPath)
			if err != nil {
				return nil, fmt.Errorf("MCP tools: %w", err)
			}
			session.mcpPrewarm = prewarm
		}
		securityRuntime = policy.DefaultRuntime(
			policy.Mode(execution.Mode), policy.Permission(options.Permission),
		)
		session.security = securityRuntime
		journal, err = openWorkspaceJournal(
			ctx, execution.Workspace, content, execution.Journal, session,
		)
		if err != nil {
			return nil, err
		}
		diagnosticRunner = diagnostics.NewCommandRunner(execution.Workspace, backend, nil)
		commandRunner := &verify.CommandRunner{
			Root: execution.Workspace, Sandbox: backend,
			Tests: repoindex.TestMapper{Index: repositoryIndex},
		}
		if execution.Verify.Command != "" {
			commandRunner.Commands = []verify.Command{
				{Name: "custom", Command: execution.Verify.Command},
			}
		}
		verifyRunner = commandRunner
		constitutionBundle, err := constitution.Load(execution.Workspace, "")
		if err != nil {
			return nil, fmt.Errorf("constitution: %w", err)
		}
		session.Constitution = constitutionBundle.Status
		if options.RepositoryRulesPath != "" {
			securityRuntime.Repository, err = loadRepositoryRules(options.RepositoryRulesPath)
			if err != nil {
				return nil, fmt.Errorf("repository rules: %w", err)
			}
		}
		if len(constitutionBundle.Rules) > 0 {
			securityRuntime.Repository = append(append([]policy.Rule{}, constitutionBundle.Rules...), securityRuntime.Repository...)
		}
		permissionsBundle, err := permissions.Load(execution.Workspace)
		if err != nil {
			return nil, fmt.Errorf("permissions: %w", err)
		}
		if len(permissionsBundle.Rules) > 0 {
			securityRuntime.Repository = append(append([]policy.Rule{}, permissionsBundle.Rules...), securityRuntime.Repository...)
		}
		session.constitutionPrompt = constitutionBundle.Prompt
		persistAllow := func(invocation policy.Invocation) error {
			rule, ruleErr := permissions.RuleFromInvocation(invocation)
			if ruleErr != nil {
				return ruleErr
			}
			if _, appendErr := permissions.AppendAllow(execution.Workspace, rule); appendErr != nil {
				return appendErr
			}
			securityRuntime.Repository = append([]policy.Rule{rule}, securityRuntime.Repository...)
			return nil
		}
		guard, err = toolguard.New(toolguard.Options{
			Registry: registry, Policy: securityRuntime, Workspace: execution.Workspace,
			Hooks: &hooks.Adapter{Manager: session.hooks}, Journal: journal,
			PermissionHooks: &hooks.Adapter{Manager: session.hooks},
			Diagnostics:     diagnosticRunner, PersistAllow: persistAllow,
			OnNetworkAllow:        egressGate.Allow,
			ForceEditPlanApproval: options.ForceEditPlanApproval,
		})
		if err != nil {
			return nil, fmt.Errorf("create tool guard: %w", err)
		}
		agentRoot := filepath.Join(execution.Workspace, ".codehelper", "agents")
		sharedGovernor = rlm.NewGovernor(rlm.Limits{})
		childLimits := execution.Subagent
		// Child agents get their own ledger rather than sharing the RLM one:
		// execution.subagent.* has to mean child agent spend, not child agent
		// spend plus every rlm sub-query the parent happened to run.
		childGovernor := rlm.NewGovernor(rlm.Limits{
			MaxTokens: childLimits.MaxTokens, MaxCostUSD: childLimits.MaxCostUSD,
			MaxDepth: childLimits.MaxDepth, MaxConcurrency: childLimits.MaxParallel,
		})
		if err := os.MkdirAll(agentRoot, 0o700); err != nil {
			return nil, fmt.Errorf("agent root: %w", err)
		}
		childTrees, err := newChildWorktrees(
			execution.Workspace, agentRoot, childLimits.Workspace, backend,
		)
		if err != nil {
			return nil, fmt.Errorf("child worktrees: %w", err)
		}
		childToolsets = newChildToolsets(
			helperPath, content, webOpts, execution.Verify, execution.Journal,
		)
		session.childTools = childToolsets
		chatRoot := filepath.Join(execution.Workspace, ".codehelper", "chats")
		if err := os.MkdirAll(chatRoot, 0o700); err != nil {
			return nil, fmt.Errorf("Chat worktree root: %w", err)
		}
		chatTrees, err = newChildWorktrees(
			execution.Workspace, chatRoot, config.SubagentWorkspaceAuto, backend,
		)
		if err != nil {
			return nil, fmt.Errorf("Chat worktrees: %w", err)
		}
		childRT = newChildRuntime(
			childLimits, execution.Workspace, childGovernor, childToolsets,
		)
		subagentManager, err = subagent.Open(subagent.Options{
			Root: agentRoot, Gate: guard, Runtime: childRT, Worktrees: childTrees,
			Budget: subagent.Budget{
				MaxTokens: childLimits.MaxTokens, MaxCostUSD: childLimits.MaxCostUSD,
				MaxDepth: childLimits.MaxDepth, MaxParallel: childLimits.MaxParallel,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("subagent manager: %w", err)
		}
		parentFiles, err = filetool.NewWithBackend(execution.Workspace, backend)
		if err != nil {
			return nil, fmt.Errorf("parent file tools for agent_merge: %w", err)
		}
		if err := agenttool.Register(registry, agenttool.Options{
			Manager: subagentManager,
			Handles: handleStore, Governor: childGovernor,
			SessionID: hookSessionID, Root: agentRoot, Gate: guard,
			Graph: agentGraphFor(
				options.PersistentStore, execution.Workspace, hookSessionID,
			),
			Files:     parentFiles,
			Workspace: execution.Workspace,
			OnRelease: func(agentID string) {
				childRT.release(agentID)
			},
		}); err != nil {
			return nil, fmt.Errorf("agent tool: %w", err)
		}
		rlmRoot := filepath.Join(execution.Workspace, ".codehelper", "rlm")
		rlmWorkspace, err := sandbox.NewWorkspace(execution.Workspace)
		if err != nil {
			return nil, fmt.Errorf("rlm workspace: %w", err)
		}
		var subQuery rlm.SubQueryClient
		// A locked route set without a subquery slot leaves sub_query without a
		// route. That is a refusal worth carrying to the tool rather than a
		// missing feature: the operator asked for no fallback, and the model
		// should be told that instead of that sub_query does not exist.
		subQueryRoute, subQueryErr := routes.For(model.PurposeSubquery)
		if subQueryErr != nil {
			subQuery = rlm.RouteSubQuery{Provider: toolSampler, Unavailable: subQueryErr}
		} else if err := subQueryRoute.Validate(); err == nil {
			subQuery = rlm.RouteSubQuery{Provider: toolSampler, Route: subQueryRoute}
		}
		rlmStore, err := rlm.NewStore(rlm.StoreOptions{
			Root: rlmRoot, Backend: backend, Workspace: rlmWorkspace,
			SubQuery: subQuery, Governor: sharedGovernor,
		})
		if err != nil {
			return nil, fmt.Errorf("rlm store: %w", err)
		}
		if err := rlmtool.Register(registry, rlmtool.Options{
			Store: rlmStore, Handles: handleStore, Governor: sharedGovernor,
			SubQuery: subQuery, SessionID: hookSessionID, Root: rlmRoot,
			Workspace: execution.Workspace, Backend: backend,
		}); err != nil {
			return nil, fmt.Errorf("rlm tools: %w", err)
		}
		session.rlmStore = rlmStore
		inputHost := interacttool.NewHost(0)
		// Vision is registered when a route was configured for it, whether through
		// [route.vision] or through the [vision] section it aliases. Resolution
		// failures were silently swallowed here before, which turned a typo in a
		// provider name into a session that quietly could not see images.
		var visionClient interacttool.VisionClient
		if _, configured := snapshot.Config.Route.Slots[string(model.PurposeVision)]; configured {
			visionRoute, visionErr := routes.For(model.PurposeVision)
			if visionErr != nil {
				return nil, fmt.Errorf("vision route: %w", visionErr)
			}
			visionClient = interacttool.RouteVision{Provider: toolSampler, Route: visionRoute}
		}
		if err := interacttool.Register(registry, interacttool.Options{
			Host: inputHost, Workspace: execution.Workspace, Backend: backend,
			RLM: rlmStore, Governor: sharedGovernor, Vision: visionClient,
			OnPlan: func(plan interacttool.Plan) error {
				if session.applyPlan != nil {
					session.applyPlan(plan)
				}
				return nil
			},
		}); err != nil {
			return nil, fmt.Errorf("interact tools: %w", err)
		}
		session.inputHost = inputHost
	} else if options.TrustedDynamicTools {
		return nil, errors.New("trusted-host dynamic tools require execution.tools")
	}
	toolPrefix := ""
	if execution.Tools {
		toolPrefix = "Use only the supplied tools and honor their schemas and policy decisions."
	}
	budgets := options.PromptBudgets
	if budgets == nil {
		budgets = defaultPromptBudgets()
	}
	prompt, err := promptcontext.Assemble(promptcontext.Options{
		BaseSystem: "You are a software engineering agent.",
		Mode:       execution.Mode, Workspace: execution.Workspace, ToolPrefix: toolPrefix,
		Budgets: budgets, WorkingSet: promptWorkingSet(options.WorkingSet),
		Skills:        skillCatalog.Summaries(ctx),
		MemoryEnabled: snapshot.Config.Memory.Enabled,
		Memory:        session.memory,
		Constitution:  session.constitutionPrompt,
		Sections:      promptSections(securityRuntime, registry, snapshot.Config.Context, execution.Tools),
	})
	if err != nil {
		return nil, fmt.Errorf("assemble prompt context: %w", err)
	}
	catalog := skillCatalog
	repoContext := newRepoContext(repositoryIndex, snapshot.Config.Context, budgets)
	// Spans need a turn row to hang off, so they are only persisted when the
	// session has a database. A session without one still measures its turns; it
	// just has nowhere to keep the trace.
	var traceSink trace.Sink
	if options.PersistentStore != nil {
		traceSink = trace.NewSQLiteRepository(options.PersistentStore.SQLite())
	}
	var workspaceTurnGate *agentengine.WorkspaceTurnGate
	if journal != nil {
		// Every ordinary thread shares this workspace journal. Its transaction is
		// whole-turn atomic, so another turn must not write the same workspace until
		// commit or rollback. Isolated child worktrees clear both journal and gate.
		workspaceTurnGate = agentengine.NewWorkspaceTurnGate()
	}
	approvalPosture := policy.PermissionBypass
	if securityRuntime != nil {
		approvalPosture = securityRuntime.Permission
	} else if options.Permission != "" {
		approvalPosture = policy.Permission(options.Permission)
	}
	seedOptions := agentengine.Options{
		Provider: client, Route: route, Routes: routes,
		Tools: registry, PromptContext: prompt.Messages,
		MaxOutputTokens: execution.MaxOutputTokens, Security: securityRuntime,
		ProfilePermissionCeiling: approvalPosture,
		Workspace:                execution.Workspace, Guard: nil,
		OnNetworkAllow: egressGate.Allow,
		Journal:        journal, WorkspaceTurnGate: workspaceTurnGate,
		Diagnostics: diagnosticRunner,
		Verify: agentengine.VerifyOptions{
			Mode:           execution.Verify.Mode,
			Scope:          verify.Scope(execution.Verify.Scope),
			OnFailure:      execution.Verify.OnFailure,
			MaxRepairSteps: execution.Verify.MaxRepairSteps,
			Timeout:        execution.Verify.Timeout,
			Runner:         verifyRunner,
		},
		Metrics: session.metrics, Trace: traceSink,
		ReasoningEffort: execution.ReasoningEffort,
		NativeSearch:    execution.NativeSearch,
		Budget: agentengine.Budget{
			MaxTokens: execution.BudgetTokens, MaxCostUSD: execution.BudgetUSD,
		},
		MaxSteps: execution.MaxSteps, WorkingSet: prompt.WorkingSet,
		CriticalPaths: prompt.CriticalPaths, ContextReceipts: prompt.Receipts,
		RepoContext:     repoContext,
		WorkingSetLimit: snapshot.Config.Context.WorkingSet.MaxEntries,
		EvidenceLimit:   snapshot.Config.Context.Evidence.MaxEntries,
		// Compaction thresholds come from config rather than the engine's own
		// defaults, so an operator whose window is smaller than the default can
		// say so instead of discovering it as a provider error.
		MaxContextBytes:  snapshot.Config.Context.Compact.MaxHistoryBytes,
		SummaryMaxBytes:  snapshot.Config.Context.Compact.SummaryMaxBytes,
		MaxDigestEntries: snapshot.Config.Context.Compact.MaxDigestEntries,
		Hooks:            session.hooks, SessionID: hookSessionID, InputHost: session.inputHost,
		PromptCacheKey: stickyPromptCacheKey(hookSessionID, execution.Workspace),
		ToolCatalogSync: func() error {
			if session.mcpPrewarm == nil {
				return nil
			}
			return session.mcpPrewarm.SyncCatalog()
		},
		MCPHealthSnapshot: func() []agentengine.MCPHealthSnapshot {
			if session.mcpPool == nil {
				return nil
			}
			snapshots := session.mcpPool.HealthSnapshots()
			result := make([]agentengine.MCPHealthSnapshot, 0, len(snapshots))
			for _, snapshot := range snapshots {
				result = append(result, agentengine.MCPHealthSnapshot{
					Server: snapshot.Server, State: string(snapshot.State),
					ConsecutiveFailures: snapshot.ConsecutiveFailures,
					LastError:           snapshot.LastError, ChangedAt: snapshot.ChangedAt,
					RetryAt: snapshot.RetryAt,
				})
			}
			return result
		},
		ExtensionSnapshot: func() ([]agentengine.ExtensionSnapshot, error) {
			if session.pluginRegistry == nil {
				return nil, nil
			}
			if session.pluginTools != nil {
				if syncErr := session.pluginTools.Sync(); syncErr != nil {
					return nil, syncErr
				}
			}
			snapshots, snapshotErr := session.pluginRegistry.LifecycleSnapshots()
			if snapshotErr != nil {
				return nil, snapshotErr
			}
			result := make([]agentengine.ExtensionSnapshot, 0, len(snapshots))
			for _, snapshot := range snapshots {
				result = append(result, agentengine.ExtensionSnapshot{
					Kind: "plugin", Name: snapshot.Name, Version: snapshot.Version,
					Source: snapshot.Source, Publisher: snapshot.Publisher,
					Trust: snapshot.Trust, Digest: snapshot.Digest,
					Generation: snapshot.Generation, Enabled: snapshot.Enabled,
					LastAction: snapshot.LastAction, ChangedAt: snapshot.ChangedAt,
				})
			}
			return result, nil
		},
		SkillSnapshot: func() []agentengine.SkillSummary {
			if catalog == nil {
				return nil
			}
			summaries := catalog.Summaries(context.Background())
			out := make([]agentengine.SkillSummary, 0, len(summaries))
			for _, summary := range summaries {
				out = append(out, agentengine.SkillSummary{
					Name: summary.Name, Description: summary.Description,
					Source: string(summary.Source),
				})
			}
			return out
		},
	}
	defaultProfile := protocol.SessionProfile{
		Version:             protocol.SessionProfileVersion,
		Revision:            1,
		Mode:                execution.Mode,
		Provider:            route.ProviderID(),
		Model:               route.Model().ID,
		ReasoningEffort:     execution.ReasoningEffort,
		ApprovalPosture:     string(approvalPosture),
		ExecutionTarget:     "local",
		MaxSteps:            execution.MaxSteps,
		PromptCacheRevision: 1,
	}
	modelCapabilities := route.Model().Capabilities
	mutableProfileFields := []string{"mode", "max_steps"}
	if modelCapabilities.ToolCalls {
		mutableProfileFields = append(mutableProfileFields, "enabled_tool_ids")
	}
	if approvalPosture != policy.PermissionNever {
		mutableProfileFields = append(
			mutableProfileFields,
			"approval_posture",
		)
	}
	var reasoningEfforts []string
	if modelCapabilities.Reasoning {
		mutableProfileFields = append(mutableProfileFields, "reasoning_effort")
		reasoningEfforts = []string{"minimal", "low", "medium", "high", "xhigh"}
	}
	profileCapabilities := protocol.SessionProfileCapabilities{
		Provider: defaultProfile.Provider,
		Model:    defaultProfile.Model,
		ModelCapabilities: protocol.ModelCapabilities{
			Streaming:        modelCapabilities.Streaming,
			Reasoning:        modelCapabilities.Reasoning,
			ToolCalls:        modelCapabilities.ToolCalls,
			NativeSearch:     modelCapabilities.NativeSearch,
			Vision:           modelCapabilities.Vision,
			ImageInput:       modelCapabilities.ImageInput,
			PromptCache:      modelCapabilities.PromptCache,
			ReasoningEfforts: reasoningEfforts,
		},
		MutableFields: mutableProfileFields,
	}
	// Shared Guard is intentionally not passed into seedOptions: each thread
	// Engine allocates its own Guard so approval handlers stay isolated.
	// OnNetworkAllow still points at the session egress Gate so a mid-flight
	// host approval actually Grants Dial, not just the approval cache.
	workspaceIdentity := options.WorkspaceIdentity
	threadManager := app.NewThreadManager(func() (*app.EngineAdapter, error) {
		threadOptions := seedOptions
		threadOptions.Security = cloneThreadSecurity(seedOptions.Security)
		worker, err := agentengine.New(threadOptions)
		if err != nil {
			return nil, err
		}
		return adaptEngine(worker, workspaceIdentity), nil
	})
	session.threads = threadManager
	threadManager.SetChildFactory(func(spec app.ChildSpec) (*app.EngineAdapter, error) {
		options := childEngineOptions(seedOptions, spec, securityRuntime)
		if !spec.ReadOnly && !spec.Serialized {
			// A writing child must not borrow the parent's registry: every tool in
			// it resolves paths against the parent workspace, so the child's
			// isolated root would be a claim rather than a fact.
			toolset, err := childToolsets.open(spec.Workspace)
			if err != nil {
				return nil, err
			}
			options.Tools = toolset.registry
			options.Journal = toolset.journal
			options.ReadTracker = workspacejournal.NewReadTracker()
			options.Diagnostics = toolset.diagnostics
			options.Verify.Runner = toolset.verify
		}
		worker, err := agentengine.New(options)
		if err != nil {
			return nil, err
		}
		return adaptEngine(worker, workspaceIdentity), nil
	})
	session.chatWorkspaces = newChatWorkspaces(
		execution.Workspace,
		chatTrees,
		childToolsets,
		threadManager,
		parentFiles,
		journal,
		workspaceTurnGate,
		securityRuntime != nil &&
			securityRuntime.Permission != policy.PermissionNever,
	)
	if options.PersistentStore != nil {
		store := options.PersistentStore
		threadManager.SetWindowRestorer(func(ctx context.Context, threadID protocol.ThreadID) (*protocol.ThreadCompactedData, error) {
			return app.LatestThreadHistorySeed(ctx, store, threadID)
		})
		threadManager.SetSequenceReader(func(ctx context.Context) (protocol.Cursor, error) {
			return store.LastSequence(ctx)
		})
	}
	if journal != nil {
		if err := reverttool.Register(registry, reverttool.Options{
			Reverter: reverttool.EngineReverter{
				RevertFn: func(ctx context.Context, targetTurnID string) ([]string, []string, error) {
					receipt, revertErr := threadManager.RevertWorkspace(ctx, targetTurnID)
					conflicts := make([]string, len(receipt.Conflicts))
					for i, conflict := range receipt.Conflicts {
						conflicts[i] = conflict.Path + ": " + conflict.Reason
					}
					return receipt.Restored, conflicts, revertErr
				},
				DefaultTurnFn: threadManager.LastTurnID,
			},
		}); err != nil {
			return nil, fmt.Errorf("revert_turn tool: %w", err)
		}
	}
	session.applyPlan = threadManager.ApplyPlan
	if session.hooks != nil {
		session.hooks.SessionStart(ctx, hooks.SessionStartInput{
			SessionID: hookSessionID, Workspace: execution.Workspace,
		})
	}
	if options.PersistentStore != nil {
		if session.automations == nil {
			session.automations = automation.NewSQLiteRepository(options.PersistentStore.SQLite())
			if _, err := session.automations.Tick(ctx, time.Time{}); err != nil {
				return nil, fmt.Errorf("automation reconcile: %w", err)
			}
		}
		session.Runtime, err = NewPersistentRuntime(ctx, PersistentRuntimeOptions{
			Store: options.PersistentStore, Engine: threadManager,
			OperationBuffer:  snapshot.Config.Runtime.OperationBuffer,
			SubscriberBuffer: snapshot.Config.Runtime.SubscriberBuffer,
			Metrics:          session.metrics, Logger: session.logger,
			DefaultProfile:      defaultProfile,
			ToolCatalog:         registry,
			ProfileCapabilities: profileCapabilities,
			SessionWorkspaces:   session.chatWorkspaces,
		})
		if err != nil {
			return nil, fmt.Errorf("create persistent runtime: %w", err)
		}
	} else {
		session.Runtime = app.NewRuntime(app.Options{
			Engine: threadManager, ContentStore: content,
			Metrics: session.metrics, Logger: session.logger,
		})
	}
	if childRT != nil {
		childRT.bind(session.Runtime, threadManager, subagentManager)
		session.children = childRT
		session.subagents = subagentManager
	}
	// The scheduler starts last, because the first thing it does is look for work
	// to run, and running it needs everything above to exist.
	if err := session.startScheduler(
		ctx, execution.Worker, hookSessionID, registry, guard, journal,
		workspaceTurnGate, workflowRuns, execution.Workspace,
	); err != nil {
		return nil, err
	}
	return session, nil
}

func cloneThreadSecurity(source *policy.Runtime) *policy.Runtime {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Grants = append([]policy.Rule(nil), source.Grants...)
	cloned.Repository = append([]policy.Rule(nil), source.Repository...)
	cloned.Approvals = policy.NewApprovalCache()
	return &cloned
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

// startScheduler brings up the durable task scheduler when the host asked for
// one. It is off unless configured: a process that claims background work has to
// stay alive to finish it, which is the opposite of what `exec` does.
func (s *Session) startScheduler(
	ctx context.Context,
	settings config.Worker,
	owner string,
	registry *tool.Registry,
	guard *toolguard.Guard,
	journal *workspacejournal.Manager,
	workspaceTurnGate *agentengine.WorkspaceTurnGate,
	workflowRuns workflowRunStore,
	workspace string,
) error {
	if !settings.Enabled || s.tasks == nil {
		return nil
	}
	var executors []worker.Executor
	if s.subagents != nil && s.children != nil {
		agentTurns, err := newAgentTurnExecutor(
			s.subagents,
			s.children.release,
			guard,
			journal,
			workspaceTurnGate,
		)
		if err != nil {
			return fmt.Errorf("agent_turn executor: %w", err)
		}
		executors = append(executors, agentTurns)
	}
	workflowRunsExecutor, err := newWorkflowRunExecutor(s.Runtime, workflowRuns)
	if err != nil {
		return fmt.Errorf("workflow_run executor: %w", err)
	}
	executors = append(executors, workflowRunsExecutor)
	shellCommands, err := newShellCommandExecutor(registry, s.security, workspace, s.hooks)
	if err != nil {
		return fmt.Errorf("shell_command executor: %w", err)
	}
	executors = append(executors, shellCommands)
	if len(executors) == 0 {
		// Nothing this build can run means nothing should be claimed: a scheduler
		// with no executors would take leases it cannot honor.
		return errors.New(
			"execution.worker.enabled requires tools, which is what supplies the executors",
		)
	}
	scheduler, err := worker.New(worker.Options{
		Tasks: s.tasks, Automations: s.automations, Owner: owner,
		Executors: executors, WorkspaceRoot: workspace, Lease: settings.Lease,
		ClaimInterval:      settings.ClaimInterval,
		AutomationInterval: settings.AutomationInterval,
		MaxParallel:        settings.MaxParallel,
		Backoff: taskstate.Backoff{
			Base: settings.RetryBackoff, Max: settings.RetryBackoffMax,
		},
		Logger: s.logger,
	})
	if err != nil {
		return fmt.Errorf("worker scheduler: %w", err)
	}
	if err := scheduler.Start(ctx); err != nil {
		return fmt.Errorf("start worker scheduler: %w", err)
	}
	s.scheduler = scheduler
	return nil
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
	}
	if spec.ReadOnly && security != nil {
		// Plan mode is the existing, tested read-only enforcement: everything
		// that is not a read capability is denied with mode_denied. A read-only
		// stance that only shaped the prompt would not be a stance at all.
		readOnly := security.CloneSampling()
		readOnly.Mode = policy.ModePlan
		options.Security = readOnly
	}
	return options
}

func agentGraphFor(
	store *state.Store, workspaceRoot, sessionID string,
) subagent.Graph {
	if store == nil {
		return nil
	}
	return state.NewAgentGraph(store, workspaceRoot, sessionID)
}

func stickyPromptCacheKey(sessionID, workspace string) string {
	if sessionID != "" {
		return "session:" + sessionID
	}
	if workspace != "" {
		return "workspace:" + filepath.Base(workspace)
	}
	return "codehelper-default"
}
