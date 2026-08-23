package wire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	orchestrationextension "github.com/fwtllh-png/CodeHelper/internal/adapter/extension/orchestration"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	agenttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/agent"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	interacttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	rlmtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	workbudget "github.com/fwtllh-png/CodeHelper/internal/orchestration/budget"
	orchestrationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/store"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	persiststate "github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func buildWorkGraphStore(
	ctx context.Context,
	state *buildState,
	output *orchestrationBuildState,
) error {
	if state.persistence.taskStore == nil {
		return errors.New("work graph SQLite store is required")
	}
	value, err := orchestrationstore.Open(ctx, state.persistence.taskStore)
	if err != nil {
		return fmt.Errorf("open work graph store: %w", err)
	}
	output.workGraph = value
	output.workBudget = workbudget.NewLedger()
	return nil
}

func buildOrchestrationRepositories(
	_ context.Context,
	state *buildState,
	output *orchestrationBuildState,
) error {
	if state.persistence.taskStore == nil {
		return errors.New("orchestration store is required")
	}
	tasks := taskstate.NewSQLiteRepository(state.persistence.taskStore)
	automations := automation.NewSQLiteRepository(state.persistence.taskStore)
	if err := orchestrationextension.Contribute(
		state.tools.registry,
		orchestrationextension.Options{
			Tasks: tasks, Automations: automations,

			Backend: state.platform.backend, Workspace: state.config.execution.Workspace, SessionID: state.config.hookSessionID,
		},
	); err != nil {
		return err
	}
	state.session.tasks, state.session.automations = tasks, automations
	output.tasks, output.automations = tasks, automations
	return nil
}

func buildChildOrchestration(
	ctx context.Context,
	state *buildState,
	output *orchestrationBuildState,
) error {
	session, execution := state.session, state.config.execution
	limits := effectiveSubagentLimits(execution.Subagent, effectiveTurnTokenBudget(execution.TurnBudgetTokens, state.provider.route.Model().Limits.ContextTokens))
	output.sharedGovernor = rlm.NewGovernor(rlm.Limits{})
	output.childGovernor = newChildGovernor(limits)
	orchestrationRoot := childOrchestrationRoot(state)
	agentRoot := filepath.Join(orchestrationRoot, "agents")
	if err := os.MkdirAll(agentRoot, 0o700); err != nil {
		return fmt.Errorf("agent root: %w", err)
	}
	childTrees, err := newChildWorktrees(
		execution.Workspace, agentRoot, limits.Workspace, state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("child worktrees: %w", err)
	}
	gitCommonDir, err := childTrees.commonGitDir(ctx)
	if err != nil {
		return fmt.Errorf("resolve repository Git metadata: %w", err)
	}
	output.childToolsets = newChildToolsets(
		state.platform.helperPath, session.content, state.platform.web,
		execution.Verify, execution.Journal, state.config.diagnosticCommands,
		state.config.diagnosticReadRoots, state.config.diagnosticReadFiles,
		gitCommonDir, sandbox.BackendManagedProxyPort(state.platform.backend),
	)
	session.childTools = output.childToolsets
	chatRoot := filepath.Join(orchestrationRoot, "chats")
	if err := os.MkdirAll(chatRoot, 0o700); err != nil {
		return fmt.Errorf("Chat worktree root: %w", err)
	}
	output.chatTrees, err = newChildWorktrees(
		execution.Workspace, chatRoot, config.SubagentWorkspaceAuto,
		state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("Chat worktrees: %w", err)
	}
	output.children = newChildRuntime(
		limits, execution.Workspace, output.childGovernor, output.childToolsets,
		output.workGraph,
	)
	output.children.useBudget(output.workBudget)
	workspaceIdentity, err := taskstate.NormalizeWorkspaceRoot(execution.Workspace)
	if err != nil {
		return fmt.Errorf("normalize agent workspace: %w", err)
	}
	output.subagents, err = subagent.OpenControl(subagent.Options{
		Root: agentRoot, Gate: state.security.guard,

		Runtime: output.children, Worktrees: childTrees, Budget: subagent.Budget{
			MaxTokens: limits.MaxTokens, MaxCostUSD: limits.MaxCostUSD, MaxDepth: limits.MaxDepth, MaxParallel: limits.MaxParallel, MaxResident: limits.MaxResident, MaxTotal: limits.MaxTotal,
		}, Workspace: workspaceIdentity, SessionID: state.config.hookSessionID,
	}, subagent.DelegationMode(limits.Delegation))
	if err != nil {
		return fmt.Errorf("agent control: %w", err)
	}
	output.childToolsets.bindAgents(output.subagents, state.config.hookSessionID, output.children.release)
	output.parentFiles, err = filetool.NewWithBackend(
		execution.Workspace,
		state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("parent file tools for integrate_agent: %w", err)
	}
	if err := agenttool.Register(state.tools.registry, agenttool.Options{
		Control: output.subagents, Handles: state.tools.handleStore,

		Root: agentRoot, Gate: state.security.guard,
		Graph: persiststate.NewAgentGraph(
			state.options.PersistentStore, execution.Workspace, state.config.hookSessionID,
		),
		Files:   output.parentFiles,
		Sandbox: state.platform.backend, OnRelease: output.children.release, Verify: state.security.verify, Workspace: execution.Workspace, SessionID: state.config.hookSessionID,
	}); err != nil {
		return fmt.Errorf("agent tool: %w", err)
	}
	return nil
}

func buildRLMOrchestration(
	_ context.Context,
	state *buildState,
	output *orchestrationBuildState,
) error {
	execution := state.config.execution
	root := filepath.Join(execution.Workspace, ".codehelper", "rlm")
	workspace, err := sandbox.NewWorkspace(execution.Workspace)
	if err != nil {
		return fmt.Errorf("rlm workspace: %w", err)
	}
	var subQuery rlm.SubQueryClient
	route, routeErr := state.provider.routes.For(model.PurposeSubquery)
	if routeErr != nil {
		subQuery = rlm.RouteSubQuery{
			Provider: state.provider.toolSampler, Unavailable: routeErr,
		}
	} else if err := route.Validate(); err == nil {
		subQuery = rlm.RouteSubQuery{
			Provider: state.provider.toolSampler, Route: route,
		}
	}
	store, err := rlm.NewStore(rlm.StoreOptions{
		Root: root, Backend: state.platform.backend, Workspace: workspace,
		SubQuery: subQuery, Governor: output.sharedGovernor,
	})
	if err != nil {
		return fmt.Errorf("rlm store: %w", err)
	}
	if err := rlmtool.Register(state.tools.registry, rlmtool.Options{
		Store: store, Handles: state.tools.handleStore,
		Governor: output.sharedGovernor, SubQuery: subQuery,
		Root:    root,
		Backend: state.platform.backend, Workspace: execution.Workspace, SessionID: state.config.hookSessionID,
	}); err != nil {
		return fmt.Errorf("rlm tools: %w", err)
	}
	state.session.rlmStore = store
	return nil
}

func buildInteractionOrchestration(
	_ context.Context,
	state *buildState,
	output *orchestrationBuildState,
) error {
	execution := state.config.execution
	host := interacttool.NewHost(0)
	var vision interacttool.VisionClient
	if _, configured := state.config.snapshot.Config.Route.Slots[string(model.PurposeVision)]; configured {
		route, err := state.provider.routes.For(model.PurposeVision)
		if err != nil {
			return fmt.Errorf("vision route: %w", err)
		}
		vision = interacttool.RouteVision{
			Provider: state.provider.toolSampler, Route: route,
		}
	}
	session := state.session
	applyPlan := func(plan interacttool.Plan) error {
		if session.applyPlan != nil {
			return session.applyPlan(plan)
		}
		return nil
	}
	if err := interacttool.Register(state.tools.registry, interacttool.Options{
		Host: host, Backend: state.platform.backend,
		RLM: session.rlmStore, Governor: output.sharedGovernor, Vision: vision,
		OnPlan: applyPlan, Workspace: execution.Workspace,
	}); err != nil {
		return fmt.Errorf("interact tools: %w", err)
	}
	session.inputHost = host
	if tools := session.childTools; tools != nil {
		tools.bindInteractions(session.rlmStore, output.sharedGovernor, vision, applyPlan)
	}
	return nil
}

func newSchedulerFactory(
	state *buildState,
	output orchestrationBuildState,
) schedulerFactory {
	return schedulerFactory{
		settings:  state.config.execution.Worker,
		owner:     state.config.hookSessionID,
		workspace: state.config.execution.Workspace,
		registry:  state.tools.registry, guard: state.security.guard,
		journal:    state.security.journal,
		workGraphs: output.workGraph,
		workBudget: output.workBudget,
		persistent: state.options.PersistentStore,
		tasks:      output.tasks, automations: output.automations,
		subagents: output.subagents, children: output.children,
		security: state.security.runtime, hooks: state.session.hooks,
		logger: state.session.logger,
	}
}
