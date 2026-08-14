package wire

import (
	"context"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	reverttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/revert"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
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
		BaseSystem:    "You are a software engineering agent.",
		Mode:          execution.Mode,
		Workspace:     execution.Workspace,
		ToolPrefix:    toolPrefix,
		Budgets:       budgets,
		WorkingSet:    promptWorkingSet(state.options.WorkingSet),
		Skills:        state.tools.skillCatalog.Summaries(ctx),
		MemoryEnabled: snapshot.Config.Memory.Enabled,
		Memory:        session.memory,
		Constitution:  session.constitutionPrompt,
		Sections: promptSections(
			state.security.runtime,
			state.tools.registry,
			snapshot.Config.Context,
			execution.Tools,
		),
	})
	if err != nil {
		return fmt.Errorf("assemble prompt context: %w", err)
	}
	repoContext := newRepoContext(
		state.platform.repositoryIndex,
		snapshot.Config.Context,
		budgets,
	)
	var traceSink trace.Sink
	if state.options.PersistentStore != nil {
		traceSink = trace.NewSQLiteRepository(
			state.options.PersistentStore.SQLite(),
		)
	}
	var workspaceTurnGate *agentengine.WorkspaceTurnGate
	if state.security.journal != nil {
		workspaceTurnGate = agentengine.NewWorkspaceTurnGate()
	}
	approvalPosture := policy.PermissionBypass
	if state.security.runtime != nil {
		approvalPosture = state.security.runtime.Permission
	} else if state.options.Permission != "" {
		approvalPosture = policy.Permission(state.options.Permission)
	}
	route := state.provider.route
	modelCapabilities := route.Model().Capabilities
	reasoningEffort := execution.ReasoningEffort
	if reasoningEffort != "" && !modelCapabilities.Reasoning {
		return fmt.Errorf("execution.reasoning_effort requires a reasoning model")
	}
	var coordinatorRuntime turnkernel.CoordinatorRuntime
	if state.options.PersistentStore != nil {
		session.turnCoordinators, err = newDurableCoordinatorRuntime(
			turnstate.NewSQLiteRepository(
				state.options.PersistentStore.SQLite(),
			),
			state.config.hookSessionID,
			defaultTurnCoordinatorLease,
		)
		if err != nil {
			return fmt.Errorf(
				"create durable turn coordinator runtime: %w",
				err,
			)
		}
		coordinatorRuntime = session.turnCoordinators
	} else {
		coordinatorRuntime = turnkernel.NewEphemeralCoordinatorRuntime()
	}
	catalog := state.tools.skillCatalog
	seedOptions := agentengine.Options{
		Provider:                 state.provider.provider,
		Route:                    route,
		Routes:                   state.provider.routes,
		Tools:                    state.tools.registry,
		PromptContext:            prompt.Messages,
		ModePromptBudget:         budgets[promptcontext.PartitionMode],
		MaxOutputTokens:          execution.MaxOutputTokens,
		Security:                 state.security.runtime,
		ProfilePermissionCeiling: approvalPosture,
		Workspace:                execution.Workspace,
		WorkspaceIsolation:       "shared",
		OnNetworkAllow:           state.provider.egress.Allow,
		Journal:                  state.security.journal,
		WorkspaceTurnGate:        workspaceTurnGate,
		Diagnostics:              state.security.diagnostics,
		Verify: agentengine.VerifyOptions{
			Mode:           execution.Verify.Mode,
			Scope:          verify.Scope(execution.Verify.Scope),
			OnFailure:      execution.Verify.OnFailure,
			MaxRepairSteps: execution.Verify.MaxRepairSteps,
			Timeout:        execution.Verify.Timeout,
			Runner:         state.security.verify,
		},
		RequireCompletionDeclaration: execution.Tools,
		Metrics:                      session.metrics,
		Trace:                        traceSink,
		TurnCoordinatorRuntime:       coordinatorRuntime,
		ReasoningEffort:              reasoningEffort,
		NativeSearch:                 execution.NativeSearch,
		Budget: agentengine.Budget{
			MaxTokens:  execution.BudgetTokens,
			MaxCostUSD: execution.BudgetUSD,
		},
		MaxSteps:         execution.MaxSteps,
		WorkingSet:       prompt.WorkingSet,
		CriticalPaths:    prompt.CriticalPaths,
		ContextReceipts:  prompt.Receipts,
		RepoContext:      repoContext,
		WorkingSetLimit:  snapshot.Config.Context.WorkingSet.MaxEntries,
		EvidenceLimit:    snapshot.Config.Context.Evidence.MaxEntries,
		CompactWindow:    agentengine.CompactWindowPolicy{AutoTokens: uint64(snapshot.Config.Context.Compact.AutoCompactTokens), Scope: snapshot.Config.Context.Compact.Scope},
		SummaryMaxBytes:  snapshot.Config.Context.Compact.SummaryMaxBytes,
		MaxDigestEntries: snapshot.Config.Context.Compact.MaxDigestEntries,
		Hooks:            session.hooks,
		SessionID:        state.config.hookSessionID,
		InputHost:        session.inputHost,
		PromptCacheKey: promptcontext.StickyCacheKey(
			state.config.hookSessionID,
			execution.Workspace,
		),
		ToolCatalogSync: func() error {
			if session.mcpPrewarm == nil {
				return nil
			}
			return session.mcpPrewarm.SyncCatalog()
		},
		TurnSnapshots: agentengine.TurnSnapshotSources{
			MCP: func() []agentengine.MCPHealthSnapshot {
				if session.mcpPool == nil {
					return nil
				}
				snapshots := session.mcpPool.HealthSnapshots()
				result := make([]agentengine.MCPHealthSnapshot, 0, len(snapshots))
				for _, snapshot := range snapshots {
					result = append(result, agentengine.MCPHealthSnapshot{
						Server:              snapshot.Server,
						State:               string(snapshot.State),
						ConsecutiveFailures: snapshot.ConsecutiveFailures,
						LastError:           snapshot.LastError,
						ChangedAt:           snapshot.ChangedAt,
						RetryAt:             snapshot.RetryAt,
					})
				}
				return result
			},
			Extensions: func() ([]agentengine.ExtensionSnapshot, error) {
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
						Kind:       "plugin",
						Name:       snapshot.Name,
						Version:    snapshot.Version,
						Source:     snapshot.Source,
						Publisher:  snapshot.Publisher,
						Trust:      snapshot.Trust,
						Digest:     snapshot.Digest,
						Generation: snapshot.Generation,
						Enabled:    snapshot.Enabled,
						LastAction: snapshot.LastAction,
						ChangedAt:  snapshot.ChangedAt,
					})
				}
				return result, nil
			},
			Skills: func() []agentengine.SkillSummary {
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
		},
	}
	defaultProfile := protocol.SessionProfile{
		Version:             protocol.SessionProfileVersion,
		Revision:            1,
		Mode:                execution.Mode,
		Provider:            route.ProviderID(),
		Model:               route.Model().ID,
		ReasoningEffort:     reasoningEffort,
		ApprovalPosture:     string(approvalPosture),
		ExecutionTarget:     "local",
		MaxSteps:            execution.MaxSteps,
		PromptCacheRevision: 1,
	}
	mutableFields := []string{"mode", "max_steps"}
	if modelCapabilities.ToolCalls {
		mutableFields = append(mutableFields, "enabled_tool_ids")
	}
	if approvalPosture != policy.PermissionNever {
		mutableFields = append(mutableFields, "approval_posture")
	}
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
				toolset, openErr := childToolsets.open(spec.Workspace)
				if openErr != nil {
					return nil, openErr
				}
				options.Tools = toolset.registry
				options.Journal = toolset.journal
				options.ReadTracker = workspacejournal.NewReadTracker()
				options.Diagnostics = toolset.diagnostics
				options.Verify.Runner = toolset.verify
			}
			restrictChildTools(
				options.Security, spec, seedOptions.Tools, options.Tools,
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
				return app.LatestThreadHistorySeed(ctx, store, threadID)
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
		coordinatorRuntime:  coordinatorRuntime,
		seedOptions:         seedOptions,
		defaultProfile:      defaultProfile,
		profileCapabilities: profileCapabilities,
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
		runtime, err := apppersistence.PreparePersistentRuntime(ctx, apppersistence.PersistentRuntimeOptions{
			Store:            state.options.PersistentStore,
			Engine:           state.agent.threads,
			OperationBuffer:  state.config.snapshot.Config.Runtime.OperationBuffer,
			SubscriberBuffer: state.config.snapshot.Config.Runtime.SubscriberBuffer,
			Metrics:          session.metrics, Logger: session.logger,
			DefaultProfile:      state.agent.defaultProfile,
			ToolCatalog:         state.tools.registry,
			ProfileCapabilities: state.agent.profileCapabilities,
			SessionWorkspaces:   session.chatWorkspaces,
		})
		if err != nil {
			return fmt.Errorf("create persistent runtime: %w", err)
		}
		session.Runtime = runtime
	} else {
		runtime, err := app.PrepareRuntime(ctx, app.Options{
			Engine:       state.agent.threads,
			ContentStore: session.content,
			Metrics:      session.metrics,
			Logger:       session.logger,
		})
		if err != nil {
			return fmt.Errorf("prepare runtime: %w", err)
		}
		session.Runtime = runtime
	}
	if store := state.options.PersistentStore; store != nil &&
		state.orchestration.subagents != nil {
		control := state.orchestration.subagents
		if err := apppersistence.ConfigurePersistentSubagents(
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
