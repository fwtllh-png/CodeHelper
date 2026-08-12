package wire

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// schedulerFactory is an OrchestrationModule output. It freezes every
// dependency except the Runtime and Turn gate, which do not exist until later
// modules. BackgroundModule materializes and starts the scheduler.
type schedulerFactory struct {
	settings     config.Worker
	owner        string
	workspace    string
	registry     *tool.Registry
	guard        *toolguard.Guard
	journal      *workspacejournal.Manager
	workflowRuns workflowRunStore
	tasks        *taskstate.Repository
	automations  *automation.Repository
	subagents    *subagent.AgentControl
	children     *childRuntime
	security     *policy.Runtime
	hooks        *hooks.Manager
	logger       *slog.Logger
}

func (f schedulerFactory) Build(
	runtime *app.Runtime,
	workspaceTurnGate *agentengine.WorkspaceTurnGate,
) (*worker.Scheduler, error) {
	if !f.settings.Enabled || f.tasks == nil {
		return nil, nil
	}
	var executors []worker.Executor
	if f.subagents != nil && f.children != nil {
		agentTurns, err := newAgentTurnExecutor(
			f.subagents,
			f.children.release,
			f.guard,
			f.journal,
			workspaceTurnGate,
		)
		if err != nil {
			return nil, fmt.Errorf("agent_turn executor: %w", err)
		}
		executors = append(executors, agentTurns)
	}
	workflowRuns, err := newWorkflowRunExecutor(runtime, f.workflowRuns)
	if err != nil {
		return nil, fmt.Errorf("workflow_run executor: %w", err)
	}
	executors = append(executors, workflowRuns)
	shellCommands, err := newShellCommandExecutor(
		f.registry,
		f.security,
		f.workspace,
		f.hooks,
	)
	if err != nil {
		return nil, fmt.Errorf("shell_command executor: %w", err)
	}
	executors = append(executors, shellCommands)
	if len(executors) == 0 {
		return nil, errors.New(
			"execution.worker.enabled requires tools, which is what supplies the executors",
		)
	}
	return worker.New(worker.Options{
		Tasks: f.tasks, Automations: f.automations, Owner: f.owner,
		Executors: executors, WorkspaceRoot: f.workspace,
		Lease:              f.settings.Lease,
		ClaimInterval:      f.settings.ClaimInterval,
		AutomationInterval: f.settings.AutomationInterval,
		MaxParallel:        f.settings.MaxParallel,
		Backoff: taskstate.Backoff{
			Base: f.settings.RetryBackoff,
			Max:  f.settings.RetryBackoffMax,
		},
		Logger: f.logger,
	})
}
