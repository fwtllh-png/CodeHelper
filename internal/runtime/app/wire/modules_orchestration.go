package wire

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	agenttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/agent"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	interacttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	rlmtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type orchestrationModule struct{}

func (orchestrationModule) Name() string { return "orchestration" }

func (orchestrationModule) Build(
	ctx context.Context,
	state *buildState,
) error {
	if !state.config.execution.Tools {
		return nil
	}
	session := state.session
	execution := state.config.execution
	childLimits := execution.Subagent
	sharedGovernor := rlm.NewGovernor(rlm.Limits{})
	childGovernor := rlm.NewGovernor(rlm.Limits{
		MaxTokens:      childLimits.MaxTokens,
		MaxCostUSD:     childLimits.MaxCostUSD,
		MaxDepth:       childLimits.MaxDepth,
		MaxConcurrency: childLimits.MaxParallel,
	})
	agentRoot := filepath.Join(execution.Workspace, ".codehelper", "agents")
	if err := os.MkdirAll(agentRoot, 0o700); err != nil {
		return fmt.Errorf("agent root: %w", err)
	}
	childTrees, err := newChildWorktrees(
		execution.Workspace,
		agentRoot,
		childLimits.Workspace,
		state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("child worktrees: %w", err)
	}
	gitCommonDir, err := childTrees.commonGitDir(ctx)
	if err != nil {
		return fmt.Errorf("resolve repository Git metadata: %w", err)
	}
	childToolsets := newChildToolsets(
		state.platform.helperPath,
		session.content,
		state.platform.web,
		execution.Verify,
		execution.Journal,
		state.config.diagnosticCommands,
		state.config.diagnosticReadRoots,
		state.config.diagnosticReadFiles,
		gitCommonDir,
	)
	session.childTools = childToolsets
	chatRoot := filepath.Join(execution.Workspace, ".codehelper", "chats")
	if err := os.MkdirAll(chatRoot, 0o700); err != nil {
		return fmt.Errorf("Chat worktree root: %w", err)
	}
	chatTrees, err := newChildWorktrees(
		execution.Workspace,
		chatRoot,
		config.SubagentWorkspaceAuto,
		state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("Chat worktrees: %w", err)
	}
	childRuntime := newChildRuntime(
		childLimits,
		execution.Workspace,
		childGovernor,
		childToolsets,
	)
	subagents, err := subagent.Open(subagent.Options{
		Root:      agentRoot,
		Gate:      state.security.guard,
		Runtime:   childRuntime,
		Worktrees: childTrees,
		Budget: subagent.Budget{
			MaxTokens:   childLimits.MaxTokens,
			MaxCostUSD:  childLimits.MaxCostUSD,
			MaxDepth:    childLimits.MaxDepth,
			MaxParallel: childLimits.MaxParallel,
		},
	})
	if err != nil {
		return fmt.Errorf("subagent manager: %w", err)
	}
	parentFiles, err := filetool.NewWithBackend(
		execution.Workspace,
		state.platform.backend,
	)
	if err != nil {
		return fmt.Errorf("parent file tools for agent_merge: %w", err)
	}
	if err := agenttool.Register(state.tools.registry, agenttool.Options{
		Manager:   subagents,
		Handles:   state.tools.handleStore,
		Governor:  childGovernor,
		SessionID: state.config.hookSessionID,
		Root:      agentRoot,
		Gate:      state.security.guard,
		Graph: agentGraphFor(
			state.options.PersistentStore,
			execution.Workspace,
			state.config.hookSessionID,
		),
		Files:     parentFiles,
		Workspace: execution.Workspace,
		OnRelease: func(agentID string) {
			childRuntime.release(agentID)
		},
	}); err != nil {
		return fmt.Errorf("agent tool: %w", err)
	}

	rlmRoot := filepath.Join(execution.Workspace, ".codehelper", "rlm")
	rlmWorkspace, err := sandbox.NewWorkspace(execution.Workspace)
	if err != nil {
		return fmt.Errorf("rlm workspace: %w", err)
	}
	var subQuery rlm.SubQueryClient
	subQueryRoute, subQueryErr := state.provider.routes.For(
		model.PurposeSubquery,
	)
	if subQueryErr != nil {
		subQuery = rlm.RouteSubQuery{
			Provider:    state.provider.toolSampler,
			Unavailable: subQueryErr,
		}
	} else if err := subQueryRoute.Validate(); err == nil {
		subQuery = rlm.RouteSubQuery{
			Provider: state.provider.toolSampler,
			Route:    subQueryRoute,
		}
	}
	rlmStore, err := rlm.NewStore(rlm.StoreOptions{
		Root:      rlmRoot,
		Backend:   state.platform.backend,
		Workspace: rlmWorkspace,
		SubQuery:  subQuery,
		Governor:  sharedGovernor,
	})
	if err != nil {
		return fmt.Errorf("rlm store: %w", err)
	}
	if err := rlmtool.Register(state.tools.registry, rlmtool.Options{
		Store:     rlmStore,
		Handles:   state.tools.handleStore,
		Governor:  sharedGovernor,
		SubQuery:  subQuery,
		SessionID: state.config.hookSessionID,
		Root:      rlmRoot,
		Workspace: execution.Workspace,
		Backend:   state.platform.backend,
	}); err != nil {
		return fmt.Errorf("rlm tools: %w", err)
	}
	session.rlmStore = rlmStore

	inputHost := interacttool.NewHost(0)
	var visionClient interacttool.VisionClient
	if _, configured := state.config.snapshot.Config.Route.Slots[string(model.PurposeVision)]; configured {
		visionRoute, visionErr := state.provider.routes.For(
			model.PurposeVision,
		)
		if visionErr != nil {
			return fmt.Errorf("vision route: %w", visionErr)
		}
		visionClient = interacttool.RouteVision{
			Provider: state.provider.toolSampler,
			Route:    visionRoute,
		}
	}
	if err := interacttool.Register(
		state.tools.registry,
		interacttool.Options{
			Host:      inputHost,
			Workspace: execution.Workspace,
			Backend:   state.platform.backend,
			RLM:       rlmStore,
			Governor:  sharedGovernor,
			Vision:    visionClient,
			OnPlan: func(plan interacttool.Plan) error {
				if session.applyPlan != nil {
					session.applyPlan(plan)
				}
				return nil
			},
		},
	); err != nil {
		return fmt.Errorf("interact tools: %w", err)
	}
	session.inputHost = inputHost
	state.orchestration = orchestrationBuildState{
		sharedGovernor: sharedGovernor,
		childGovernor:  childGovernor,
		children:       childRuntime,
		childToolsets:  childToolsets,
		chatTrees:      chatTrees,
		parentFiles:    parentFiles,
		subagents:      subagents,
	}
	return nil
}
