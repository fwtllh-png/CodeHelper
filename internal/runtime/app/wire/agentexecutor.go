package wire

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// AgentTurnPayloadVersion is the only payload shape the agent_turn executor
// runs. A task carrying anything else fails closed rather than being guessed at:
// the payload decides what an agent is told to do, so a misread field is a
// misread instruction.
const AgentTurnPayloadVersion = 1

// AgentTurnPayload is what a queued agent turn says about the work.
type AgentTurnPayload struct {
	Version int    `json:"version"`
	Prompt  string `json:"prompt"`
	// Role picks the child's stance and toolset. Empty means the general role,
	// which writes, so a task that only needs to read should say "explore".
	Role string `json:"role,omitempty"`
}

// agentTurnExecutor runs a queued task as a real child agent turn. It reuses the
// child runtime rather than spawning a subprocess, so a background turn
// gets the same isolation, budget and fail-closed approvals as a foreground one,
// and shows up in the same event stream.
type agentTurnExecutor struct {
	control *subagent.AgentControl
	// release drops the child's thread engine and isolated tool plane. Without it
	// a worker that ran for a week would retain one of each per finished task.
	release func(agentID string)
	guard   *toolguard.Guard
	journal *workspacejournal.Manager
	gate    *agentengine.WorkspaceTurnGate
}

func newAgentTurnExecutor(
	control *subagent.AgentControl,
	release func(agentID string),
	guard *toolguard.Guard,
	journal *workspacejournal.Manager,
	gate *agentengine.WorkspaceTurnGate,
) (*agentTurnExecutor, error) {
	if control == nil {
		return nil, errors.New("agent_turn executor requires agent control")
	}
	if guard == nil {
		return nil, errors.New("agent_turn executor requires a parent tool guard")
	}
	return &agentTurnExecutor{
		control: control, release: release, guard: guard, journal: journal, gate: gate,
	}, nil
}

func (e *agentTurnExecutor) Name() string { return taskstate.ExecutorAgentTurn }

func (e *agentTurnExecutor) Execute(
	ctx context.Context, value taskstate.Task,
) (worker.Outcome, error) {
	payload, err := parseAgentTurnPayload(value.Payload)
	if err != nil {
		// A payload this worker cannot read will not become readable on the next
		// attempt, so this is a failure to report rather than one to retry.
		return worker.Outcome{State: taskstate.StateFailed, Reason: err.Error()}, nil
	}
	role, err := subagent.ParseRole(payload.Role)
	if err != nil {
		return worker.Outcome{State: taskstate.StateFailed, Reason: err.Error()}, nil
	}

	agent, err := e.control.SpawnBackgroundForSession(
		value.SessionID, role, payload.Prompt,
	)
	if err != nil {
		// Spawn fails when the budget is spent or when a writing child has nowhere
		// isolated to write. Neither is fixed by trying again immediately, but both
		// are fixed by an operator, so the reason has to survive into the task.
		return worker.Outcome{State: taskstate.StateFailed, Reason: err.Error()}, nil
	}
	defer e.closeAgent(agent.ID)

	turnID, err := e.control.TakeoverBackground(ctx, *agent, payload.Prompt)
	if err != nil {
		return worker.Outcome{State: taskstate.StateFailed, Reason: err.Error()}, nil
	}
	if _, err := e.control.Wait(ctx, []string{agent.ID}, 0); err != nil {
		// Cancellation is the scheduler stopping or the lease being lost. Either
		// way the child turn must stop too, or it would keep spending against a
		// task another worker now owns.
		_, _ = e.control.Interrupt(context.WithoutCancel(ctx), agent.ID)
		return worker.Outcome{}, err
	}
	result, ok := e.control.Result(agent.ID)
	if !ok {
		return worker.Outcome{
			State:    taskstate.StateFailed,
			Reason:   "child agent finished without a result",
			ThreadID: subagent.ThreadIDFor(agent.ID), TurnID: turnID,
		}, nil
	}
	merged := false
	if result.Status == subagent.StatusCompleted && len(result.WritePaths()) != 0 {
		if agent.Serialized {
			// The child already ran under the host workspace's whole-turn gate.
			merged = true
		} else {
			if err := e.merge(ctx, value, agent.ID); err != nil {
				outcome := agentTurnOutcome(result, turnID, false)
				outcome.State = taskstate.StateFailed
				outcome.Reason = fmt.Sprintf("merge child result: %v", err)
				outcome.Retryable = false
				return outcome, nil
			}
			merged = true
		}
	}
	return agentTurnOutcome(result, turnID, merged), nil
}

func (e *agentTurnExecutor) merge(
	ctx context.Context,
	value taskstate.Task,
	agentID string,
) (resultErr error) {
	release, err := e.gate.Acquire(ctx)
	if err != nil {
		return err
	}
	defer release()

	transactionID := fmt.Sprintf("task-%s-attempt-%d-merge", value.ID, value.Attempt)
	if e.journal != nil {
		if err := e.journal.Begin(transactionID); err != nil {
			return err
		}
	}
	committed := false
	defer func() {
		if e.journal != nil && !committed {
			receipt, rollbackErr := e.journal.Rollback(
				context.WithoutCancel(ctx),
				transactionID,
			)
			if len(receipt.Conflicts) != 0 {
				rollbackErr = errors.Join(
					rollbackErr,
					fmt.Errorf("merge rollback left %d conflict(s)", len(receipt.Conflicts)),
				)
			}
			resultErr = errors.Join(resultErr, rollbackErr)
		}
	}()
	previewArguments, err := json.Marshal(map[string]any{
		"agent_id": agentID,
		"op":       "preview",
	})
	if err != nil {
		return err
	}
	mergeContext := tool.WithInvocationIdentity(ctx, tool.InvocationIdentity{
		CallID:    "task-agent-preview-" + value.ID,
		SessionID: value.SessionID, ThreadID: value.ThreadID,
		TurnID: value.TurnID,
	})
	preview, err := e.guard.Execute(
		mergeContext,
		"task-agent-preview-"+value.ID,
		"integrate_agent",
		previewArguments,
	)
	if err != nil {
		return err
	}
	previewDigest, _ := preview.Metadata["preview_digest"].(string)
	if previewDigest == "" {
		return errors.New("agent integration preview returned no digest")
	}
	applyArguments, err := json.Marshal(map[string]any{
		"agent_id":       agentID,
		"op":             "apply",
		"preview_digest": previewDigest,
	})
	if err != nil {
		return err
	}
	result, err := e.guard.Execute(
		mergeContext,
		"task-agent-apply-"+value.ID,
		"integrate_agent",
		applyArguments,
	)
	if err != nil {
		return err
	}
	if result.IsError {
		message := strings.TrimSpace(result.Content)
		if message == "" {
			message = "agent merge failed"
		}
		return errors.New(message)
	}
	if e.journal != nil {
		if err := e.journal.Commit(transactionID); err != nil {
			// Commit removes the active journal before appending its durable marker;
			// it can no longer be rolled back through the active transaction path.
			committed = true
			return err
		}
	}
	committed = true
	return nil
}

// closeAgent discards the child's worktree and forgets its thread. A background
// task keeps no agent resident: the next attempt spawns a fresh one, which is
// what makes a retry cheap.
func (e *agentTurnExecutor) closeAgent(agentID string) {
	_ = e.control.Close(agentID)
	if e.release != nil {
		e.release(agentID)
	}
}

func agentTurnOutcome(result subagent.Result, turnID string, merged bool) worker.Outcome {
	outcome := worker.Outcome{
		ThreadID: result.ThreadID, TurnID: result.TurnID,
		PermissionDigests: append(
			[]string(nil),
			result.PermissionDigests...,
		),
	}
	if outcome.TurnID == "" {
		outcome.TurnID = turnID
	}
	switch result.Status {
	case subagent.StatusCompleted:
		outcome.State = taskstate.StateCompleted
	case subagent.StatusInterrupted:
		// Interrupted means someone stopped this turn, not that the work is
		// impossible, and the worktree that held its writes is gone. Another
		// attempt starts from the same place the first one did.
		outcome.State, outcome.Retryable = taskstate.StateFailed, true
		outcome.Reason = "child agent was interrupted"
	default:
		outcome.State, outcome.Retryable = taskstate.StateFailed, true
		outcome.Reason = strings.Join(result.Unresolved, "; ")
		if outcome.Reason == "" {
			outcome.Reason = "child agent turn failed"
		}
	}
	// The child's own account of the turn is the task result. It records what the
	// child left unresolved and what it changed. A completed writing child is
	// merged before this result is encoded, so Merged is an execution fact.
	if encoded, err := json.Marshal(agentTurnResult{
		Version: AgentTurnPayloadVersion, AgentID: result.AgentID,
		ThreadID: result.ThreadID, TurnID: outcome.TurnID,
		Status: string(result.Status), Summary: result.Summary,
		Unresolved: result.Unresolved, Verification: result.Verification,
		ChangedPaths: result.WritePaths(), Merged: merged, Usage: result.Usage, Context: result.Context,
	}); err == nil {
		outcome.Result = encoded
	}
	return outcome
}

// agentTurnResult is the task result an operator reads. Merged distinguishes a
// child worktree diff from changes that actually reached the host workspace.
type agentTurnResult struct {
	Version      int                          `json:"version"`
	AgentID      string                       `json:"agent_id"`
	ThreadID     string                       `json:"thread_id,omitempty"`
	TurnID       string                       `json:"turn_id,omitempty"`
	Status       string                       `json:"status"`
	Summary      string                       `json:"summary,omitempty"`
	Unresolved   []string                     `json:"unresolved,omitempty"`
	Verification protocol.ReceiptVerification `json:"verification"`
	ChangedPaths []string                     `json:"changed_paths,omitempty"`
	Merged       bool                         `json:"merged"`
	Usage        subagent.ResultUsage         `json:"usage"`
	Context      subagent.ContextReceipt      `json:"context"`
}

func parseAgentTurnPayload(raw json.RawMessage) (AgentTurnPayload, error) {
	if len(raw) == 0 {
		return AgentTurnPayload{}, errors.New("agent_turn task has no payload")
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var payload AgentTurnPayload
	if err := decoder.Decode(&payload); err != nil {
		return AgentTurnPayload{}, fmt.Errorf("agent_turn payload: %w", err)
	}
	if payload.Version != AgentTurnPayloadVersion {
		return AgentTurnPayload{}, fmt.Errorf(
			"agent_turn payload version %d is not supported (this build runs version %d)",
			payload.Version, AgentTurnPayloadVersion,
		)
	}
	if strings.TrimSpace(payload.Prompt) == "" {
		return AgentTurnPayload{}, errors.New("agent_turn payload needs a prompt")
	}
	return payload, nil
}
