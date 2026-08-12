package wire

import (
	"context"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	reverttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/revert"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type agentModule struct{}

func (agentModule) Name() string { return "agent" }

func (agentModule) Build(ctx context.Context, state *buildState) error {
	session := state.session
	execution := state.config.execution
	snapshot := state.config.snapshot
	toolPrefix := ""
	if execution.Tools {
		toolPrefix = "Use only the supplied tools and honor their schemas and policy decisions. " +
			"A turn that mutates the workspace is not complete until turn_complete is called " +
			"after the last mutation and every required quality check."
	}
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
	reasoningEffort := maximumReasoningEffort(
		route.ProviderID(),
		route.Model().ID,
		modelCapabilities.Reasoning,
	)
	maxOutputTokens, err := reasoningAwareMaxOutputTokens(
		execution.MaxOutputTokens,
		snapshot.MaxOutputTokensSource(),
		route,
		reasoningEffort,
	)
	if err != nil {
		return err
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
		Provider:                 state.provider.client,
		Route:                    route,
		Routes:                   state.provider.routes,
		Tools:                    state.tools.registry,
		PromptContext:            prompt.Messages,
		ModePromptBudget:         budgets[promptcontext.PartitionMode],
		MaxOutputTokens:          maxOutputTokens,
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
		FixedReasoningEffort:         reasoningEffort,
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
		MaxContextBytes:  snapshot.Config.Context.Compact.MaxHistoryBytes,
		SummaryMaxBytes:  snapshot.Config.Context.Compact.SummaryMaxBytes,
		MaxDigestEntries: snapshot.Config.Context.Compact.MaxDigestEntries,
		Hooks:            session.hooks,
		SessionID:        state.config.hookSessionID,
		InputHost:        session.inputHost,
		PromptCacheKey: stickyPromptCacheKey(
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
	threadManager.SetChildFactory(
		func(spec app.ChildSpec) (*app.EngineAdapter, error) {
			options := childEngineOptions(
				seedOptions,
				spec,
				securityRuntime,
			)
			if !spec.ReadOnly && !spec.Serialized {
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
			worker, workerErr := agentengine.New(options)
			if workerErr != nil {
				return nil, workerErr
			}
			return adaptEngine(worker, workspaceIdentity), nil
		},
	)
	session.chatWorkspaces = newChatWorkspaces(
		execution.Workspace,
		state.orchestration.chatTrees,
		state.orchestration.childToolsets,
		threadManager,
		state.orchestration.parentFiles,
		state.security.journal,
		workspaceTurnGate,
		state.security.runtime != nil &&
			state.security.runtime.Permission != policy.PermissionNever,
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
		runtime, err := PreparePersistentRuntime(ctx, PersistentRuntimeOptions{
			Store:               state.options.PersistentStore,
			Engine:              state.agent.threads,
			OperationBuffer:     state.config.snapshot.Config.Runtime.OperationBuffer,
			SubscriberBuffer:    state.config.snapshot.Config.Runtime.SubscriberBuffer,
			Metrics:             session.metrics,
			Logger:              session.logger,
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
	if state.orchestration.children != nil {
		state.orchestration.children.bind(
			session.Runtime,
			state.agent.threads,
			state.orchestration.subagents,
		)
		session.children = state.orchestration.children
		session.subagents = state.orchestration.subagents
	}
	state.runtime.application = session.Runtime
	return nil
}

type backgroundModule struct{}

func (backgroundModule) Name() string { return "background" }

func (backgroundModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	if prewarm := state.extensions.mcpPrewarm; prewarm != nil {
		if err := prewarm.RefreshNow(ctx); err != nil {
			return fmt.Errorf("initial MCP refresh: %w", err)
		}
	}
	if err := state.runtime.application.Start(ctx); err != nil {
		return fmt.Errorf("start runtime recovery: %w", err)
	}
	if prewarm := state.extensions.mcpPrewarm; prewarm != nil {
		prewarm.Start(ctx)
	}
	if automations := state.orchestration.automations; automations != nil {
		if _, err := automations.Tick(ctx, time.Time{}); err != nil {
			return fmt.Errorf("automation reconcile: %w", err)
		}
	}
	scheduler, err := state.orchestration.scheduler.Build(
		state.runtime.application,
		state.agent.workspaceTurnGate,
	)
	if err != nil {
		return fmt.Errorf("worker scheduler: %w", err)
	}
	if scheduler == nil {
		return nil
	}
	if err := scheduler.Start(ctx); err != nil {
		return fmt.Errorf("start worker scheduler: %w", err)
	}
	state.session.scheduler = scheduler
	return nil
}
