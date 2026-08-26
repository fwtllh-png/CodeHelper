package wire

import (
	"context"
	"fmt"

	sessionhistory "github.com/fwtllh-png/CodeHelper/internal/persist/history"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	reverttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/revert"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type agentModule struct{}

func (agentModule) Name() string { return "agent" }

func (agentModule) Build(ctx context.Context, state *buildState) error {
	session := state.session
	execution := state.config.execution
	snapshot := state.config.snapshot
	delegation := ""
	if state.orchestration.subagents != nil {
		delegation = state.orchestration.subagents.Policy().Instructions()
	}
	toolPrefix := promptcontext.ToolInstructions(execution.Tools, delegation)
	budgets := state.options.PromptBudgets
	if budgets == nil {
		budgets = defaultPromptBudgets()
	}
	prompt, err := promptcontext.Assemble(promptcontext.Options{
		BaseSystem: "You are a software engineering agent.",

		ToolPrefix: toolPrefix,
		Budgets:    budgets,

		MemoryEnabled: false,
		Constitution:  session.constitutionPrompt, WorkingSet: promptWorkingSet(state.options.WorkingSet), Workspace: execution.Workspace,
	})
	if err != nil {
		return fmt.Errorf("assemble prompt context: %w", err)
	}
	workspaceID, err := bindMemoryScopes(
		session.memory,
		execution.Workspace,
		state.options.WorkspaceIdentity.RootID,
	)
	if err != nil {
		return err
	}
	repoContext := newRepoContext(
		state.platform.repositoryIndex,
		snapshot.Config.Context,
		budgets,
	)
	workspaceTurnGate, approvalPosture := engineSecurityPolicy(state)
	route := state.provider.route
	reasoningEffort := effectiveReasoningEffort(route, execution.ReasoningEffort)
	if err := validateRouteReasoning(route, reasoningEffort); err != nil {
		return err
	}
	modelCapabilities := route.Model().Capabilities
	contextRuntime, err := buildContextRuntime(
		state.options.PersistentStore,
		state.config.hookSessionID,
	)
	if err != nil {
		return fmt.Errorf("create context runtime: %w", err)
	}
	session.turnCoordinators = contextRuntime.durable
	catalog := state.tools.skillCatalog
	seedOptions := agentengine.Options{ProviderConfig: agentengine.ProviderConfig{Provider: state.provider.provider,
		Route:            route,
		Routes:           state.provider.routes,
		SelectableRoutes: state.provider.selectableRoutes,

		MaxOutputTokens: execution.MaxOutputTokens,

		ReasoningEffort: reasoningEffort,
		NativeSearch:    execution.NativeSearch,

		MaxSteps: execution.MaxSteps}, ContextConfig: agentengine.ContextConfig{StaticContext: prompt.Messages,
		ContextBudgets: budgets,
		CodingPolicy:   execution.Tools && snapshot.Config.Context.CodingPolicy.Enabled,

		Budget: agentengine.Budget{
			MaxTokens:     execution.BudgetTokens,
			MaxTurnTokens: effectiveTurnTokenBudget(execution.TurnBudgetTokens, route.Model().Limits.ContextTokens),
			MaxCostUSD:    execution.BudgetUSD,
		},

		WorkingSet:            prompt.WorkingSet,
		CriticalPaths:         prompt.CriticalPaths,
		StaticContextReceipts: prompt.Receipts,
		RepoContext:           repoContext,
		WorkingSetLimit:       snapshot.Config.Context.WorkingSet.MaxEntries,
		EvidenceLimit:         snapshot.Config.Context.Evidence.MaxEntries,
		SummaryMaxBytes:       snapshot.Config.Context.Compact.SummaryMaxBytes,
		MaxDigestEntries:      snapshot.Config.Context.Compact.MaxDigestEntries,
		Context: engineContextPolicy(
			snapshot.Config.Context.Compact,
			contextRuntime.commit,
			contextRuntime.commitWithFacts,
		),

		PromptCacheKey: promptcontext.StickyCacheKey(
			state.config.hookSessionID,
			execution.Workspace,
		),

		TurnSnapshots: agentengine.TurnSnapshotSources{
			Memory: memorySnapshotSource(
				session.memory,
				snapshot.Config.Memory,
				budgets[promptcontext.PartitionUserMemory],
			),
			MCP: func() []agentengine.MCPHealthSnapshot {
				if session.mcpPool == nil {
					return nil
				}
				snapshots := session.mcpPool.HealthSnapshots()
				result := make([]agentengine.MCPHealthSnapshot, 0, len(snapshots))
				for _, snapshot := range snapshots {
					result = append(result, agentengine.MCPHealthSnapshot{
						Server:              snapshot.Server,
						State:               snapshot.State,
						ConsecutiveFailures: snapshot.ConsecutiveFailures,
						LastError:           snapshot.LastError,
						ChangedAt:           snapshot.ChangedAt,
						RetryAt:             snapshot.RetryAt,
					})
				}
				return result
			},
			ExtensionPlan: func() (runtimeextension.Plan, error) {
				if session.extensions == nil {
					return runtimeextension.Plan{}, nil
				}
				return session.extensions.SnapshotPlan(context.Background())
			},
			SkillSelection: func(
				query string,
			) ([]agentengine.SkillSummary, agentengine.SkillSelectionMetrics, error) {
				return selectTurnSkills(catalog, query)
			},
		}}, ToolConfig: agentengine.ToolConfig{Tools: state.tools.registry,

		OnNetworkAllow: state.security.guardFactory.onNetworkAllow,

		Diagnostics: state.security.diagnostics,
		Verify: agentengine.VerifyOptions{
			Mode:           execution.Verify.Mode,
			Scope:          verify.Scope(execution.Verify.Scope),
			OnFailure:      execution.Verify.OnFailure,
			MaxRepairSteps: execution.Verify.MaxRepairSteps,
			Timeout:        execution.Verify.Timeout,
			Runner:         state.security.verify,
		},
		RequireCompletionDeclaration: execution.Tools,

		ToolCatalogSync: func() error {
			if session.mcpPrewarm == nil {
				return nil
			}
			return session.mcpPrewarm.SyncCatalog()
		}}, SecurityConfig: agentengine.SecurityConfig{Security: state.security.runtime,
		ProfilePermissionCeiling: approvalPosture,
		Workspace:                execution.Workspace,
		WorkspaceIdentity:        workspaceID,
		WorkspaceIsolation:       "shared",

		Journal:           state.security.journal,
		WorkspaceTurnGate: workspaceTurnGate}, TelemetryConfig: agentengine.TelemetryConfig{Metrics: session.metrics,
		Observability: engineObservability(state),

		Hooks: session.hooks}, LifecycleConfig: agentengine.LifecycleConfig{TurnCoordinatorRuntime: contextRuntime.coordinator,
		ReleaseTurnResources: session.turnProcessReleaser(
			session.processes,
			"main",
		),

		SessionID: state.config.hookSessionID,
		InputHost: session.inputHost},
	}
	engineGuardFactory := state.security.guardFactory
	approvalObserver := session.metrics.Approval
	bindEngineGuardFactory(
		&seedOptions,
		engineGuardFactory,
		approvalObserver,
	)
	defaultProfile := protocol.SessionProfile{
		Version: protocol.SessionProfileVersion, Revision: 1,
		Mode:                execution.Mode,
		PlanningPolicy:      "adaptive",
		PlanApproval:        "manual",
		Provider:            route.ProviderID(),
		Model:               route.Model().ID,
		ReasoningEffort:     reasoningEffort,
		ApprovalPosture:     string(approvalPosture),
		ExecutionTarget:     "local",
		MaxSteps:            execution.MaxSteps,
		PromptCacheRevision: 1,
	}
	session.configuration.profile = defaultProfile
	profileModels, modelFields := runtimeProfileModels(
		state.provider.modelCatalog,
		defaultProfile.Provider,
		state.provider.modelCapabilities,
	)
	mutableFields := mutableSessionProfileFields(
		modelFields, modelCapabilities.ToolCalls,
		approvalPosture != policy.PermissionNever,
	)
	profileCapabilities := protocol.SessionProfileCapabilities{
		Provider:          defaultProfile.Provider,
		Model:             defaultProfile.Model,
		ModelCapabilities: state.provider.modelCapabilities,
		MutableFields:     mutableFields,
	}
	workspaceIdentity := state.options.WorkspaceIdentity
	childToolsets := state.orchestration.childToolsets
	securityRuntime := state.security.runtime
	threadManager := app.NewThreadManager(func() (*app.EngineAdapter, error) {
		threadOptions := seedOptions
		threadOptions.Security = cloneThreadSecurity(seedOptions.Security)
		bindEngineGuardFactory(
			&threadOptions,
			engineGuardFactory,
			approvalObserver,
		)
		worker, workerErr := agentengine.New(threadOptions)
		if workerErr != nil {
			return nil, workerErr
		}
		return adaptEngine(worker, workspaceIdentity), nil
	})
	session.threads = threadManager
	subagent.BindRuntimeContext(state.orchestration.subagents, threadManager)
	threadManager.SetChildFactory(
		func(spec app.ChildSpec) (*app.EngineAdapter, error) {
			options := childEngineOptions(
				seedOptions,
				spec,
				securityRuntime,
			)
			if !spec.Serialized && (!spec.ReadOnly || spec.Workspace != spec.HostWorkspace) {
				toolset, openErr := childToolsets.open(spec.Workspace, spec.HostSeeded)
				if openErr != nil {
					return nil, openErr
				}
				options.Tools = toolset.registry
				options.Journal, options.InputHost = toolset.journal, toolset.inputHost
				options.ReadTracker = workspacejournal.NewReadTracker()
				options.Diagnostics = toolset.diagnostics
				options.Verify.Runner = toolset.verify
				options.ReleaseTurnResources = session.turnProcessReleaser(
					toolset.processes,
					"child",
				)
			}
			restrictChildTools(
				options.Security, spec, seedOptions.Tools, options.Tools,
			)
			bindEngineGuardFactory(
				&options,
				engineGuardFactory,
				approvalObserver,
			)
			worker, workerErr := agentengine.New(options)
			if workerErr != nil {
				return nil, workerErr
			}
			adapter := adaptEngine(worker, workspaceIdentity)
			if spec.AgentID != "" && spec.AgentPath != "" {
				adapter.SetApprovalSource(protocol.ApprovalSource{
					Kind: "agent", AgentID: spec.AgentID,
					AgentPath: spec.AgentPath, ParentPath: spec.ParentPath,
					Role: spec.Role, SessionID: spec.SessionID,
					WorkspaceRoot: spec.HostWorkspace,
				})
			}
			return adapter, nil
		},
	)
	session.chatWorkspaces = buildChatWorkspaces(
		state, threadManager, workspaceTurnGate,
	)
	if state.options.PersistentStore != nil {
		store := state.options.PersistentStore
		threadManager.SetWindowRestorer(
			func(
				ctx context.Context,
				threadID protocol.ThreadID,
			) (*protocol.ThreadCompactedData, error) {
				return sessionhistory.LatestThreadHistorySeed(ctx, store, threadID)
			},
		)
		threadManager.SetSequenceReader(
			func(ctx context.Context) (protocol.Cursor, error) {
				return store.LastSequence(ctx)
			},
		)
	}
	if state.security.journal != nil {
		if err := reverttool.Register(
			state.tools.registry,
			reverttool.Options{Reverter: reverttool.EngineReverter{
				RevertFn: func(
					ctx context.Context,
					targetTurnID string,
				) ([]string, []string, error) {
					receipt, revertErr := threadManager.RevertWorkspace(
						ctx,
						targetTurnID,
					)
					conflicts := make([]string, len(receipt.Conflicts))
					for index, conflict := range receipt.Conflicts {
						conflicts[index] = conflict.Path + ": " + conflict.Reason
					}
					return receipt.Restored, conflicts, revertErr
				},
				DefaultTurnFn: threadManager.LastTurnID,
			}},
		); err != nil {
			return fmt.Errorf("revert_turn tool: %w", err)
		}
	}
	session.applyPlan = threadManager.ApplyPlan
	if session.hooks != nil {
		session.hooks.SessionStart(ctx, hooks.SessionStartInput{
			SessionID: state.config.hookSessionID,
			Workspace: execution.Workspace,
		})
	}
	state.agent = agentBuildState{
		workspaceTurnGate:   workspaceTurnGate,
		coordinatorRuntime:  contextRuntime.coordinator,
		seedOptions:         seedOptions,
		defaultProfile:      defaultProfile,
		profileCapabilities: profileCapabilities,
		profileModels:       profileModels,
		threads:             threadManager,
	}
	return nil
}

type runtimeModule struct{}

func (runtimeModule) Name() string { return "runtime" }

func (runtimeModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	session := state.session
	if state.options.PersistentStore != nil {
		runtime, err := PreparePersistentRuntime(ctx, PersistentRuntimeOptions{
			Store:               state.options.PersistentStore,
			WorkspaceRoot:       state.config.execution.Workspace,
			Engine:              state.agent.threads,
			OperationBuffer:     state.config.snapshot.Config.Runtime.OperationBuffer,
			SubscriberBuffer:    state.config.snapshot.Config.Runtime.SubscriberBuffer,
			Observability:       runtimeObservability(state),
			DefaultProfile:      state.agent.defaultProfile,
			ToolCatalog:         state.tools.registry,
			ProfileCapabilities: state.agent.profileCapabilities,
			ProfileModels:       state.agent.profileModels,
			SessionWorkspaces:   session.chatWorkspaces,
			SkipRuntimeRecovery: state.options.RuntimeRole == RuntimeRoleWorker,
		})
		if err != nil {
			return fmt.Errorf("create persistent runtime: %w", err)
		}
		session.Runtime = runtime
	} else {
		runtime, err := app.PrepareRuntime(ctx, app.Options{
			Engine:        state.agent.threads,
			WorkspaceRoot: state.config.execution.Workspace,
			ContentStore:  session.content,
			Orchestration: state.orchestration.workGraph, Observability: runtimeObservability(state),
		})
		if err != nil {
			return fmt.Errorf("prepare runtime: %w", err)
		}
		session.Runtime = runtime
	}
	if store := state.options.PersistentStore; store != nil &&
		state.orchestration.subagents != nil {
		control := state.orchestration.subagents
		if err := ConfigurePersistentSubagents(
			state.agent.threads, store,
			state.config.execution.Workspace,
			state.config.hookSessionID,
			session.Runtime,
			func(graph any) error {
				return control.AttachGraph(graph.(subagent.Graph))
			},
		); err != nil {
			return fmt.Errorf("attach live agent graph: %w", err)
		}
	}
	if state.orchestration.children != nil {
		if err := state.orchestration.children.bind(
			session.Runtime,
			state.agent.threads,
			state.orchestration.subagents,
		); err != nil {
			return fmt.Errorf("bind child runtime: %w", err)
		}
		session.children = state.orchestration.children
		session.subagents = state.orchestration.subagents
	}
	state.runtime.application = session.Runtime
	return nil
}
