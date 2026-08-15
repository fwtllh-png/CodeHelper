package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
)

// operation dispatches one model-visible lifecycle tool onto AgentControl.
type operation struct {
	tools *Tool
	kind  string
}

func (o *operation) Descriptor() tool.Descriptor {
	switch o.kind {
	case "wait_agent":
		return tool.Descriptor{
			Name: "wait_agent",
			Description: "Wait until listed child agents reach a terminal status " +
				"(completed|failed|interrupted|integrated|integration_failed|closed). " +
				"Empty agent_ids waits for any. " +
				"timeout_ms>0 returns timed_out without error when the deadline elapses. " +
				"A queued same-workspace child returns deferred immediately because it can start only " +
				"after the calling turn releases the workspace.",
			Visibility: o.visibility(), Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_ids": map[string]any{
						"type": "array", "items": map[string]any{"type": "string"},
					},
					"timeout_ms": map[string]any{"type": "integer"},
				},
				"additionalProperties": false,
			},
		}
	case "list_agents":
		return tool.Descriptor{
			Name: "list_agents",
			Description: "List child agents managed by this session. " +
				"Optionally filter by parent_id; include_closed lists closed agents.",
			Visibility: o.visibility(), Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"parent_id":      map[string]any{"type": "string"},
					"include_closed": map[string]any{"type": "boolean"},
				},
				"additionalProperties": false,
			},
		}
	case "send_message":
		return tool.Descriptor{
			Name: "send_message",
			Description: "Queue a bounded message for an open child agent without starting a new turn. " +
				"Use followup_task when the message should begin more work.",
			Visibility: o.visibility(), Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "agent", Field: "agent_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{"type": "string", "minLength": float64(1)},
					"message": map[string]any{
						"type": "string", "minLength": float64(1), "maxLength": float64(8192),
					},
				},
				"required":             []string{"agent_id", "message"},
				"additionalProperties": false,
			},
		}
	case "followup_task":
		return tool.Descriptor{
			Name: "followup_task",
			Description: "Send a follow-up turn to a resident child agent. " +
				"Fails if the agent is running, closed, or missing. " +
				"Interrupt or wait for completion before following up.",
			Visibility: o.visibility(), Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "agent", Field: "agent_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{"type": "string", "minLength": float64(1)},
					"prompt": map[string]any{
						"type": "string", "minLength": float64(1), "maxLength": float64(16384),
					},
				},
				"required":             []string{"agent_id", "prompt"},
				"additionalProperties": false,
			},
		}
	case "interrupt_agent":
		return tool.Descriptor{
			Name: "interrupt_agent",
			Description: "Interrupt a running child agent turn. The agent stays open " +
				"(worktree retained) so followup_task can resume.",
			Visibility: o.visibility(), Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "agent", Field: "agent_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"agent_id"},
				"additionalProperties": false,
			},
		}
	case "close_agent":
		return tool.Descriptor{
			Name: "close_agent",
			Description: "Close a child agent, free its concurrency slot, and cleanup its worktree. " +
				"Integrate with integrate_agent first if you need the child's writes in the parent workspace; " +
				"Close discards the worktree. Prefer wait_agent until terminal status before closing.",
			Visibility: o.visibility(), Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "agent", Field: "agent_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"agent_id"},
				"additionalProperties": false,
			},
		}
	case "integrate_agent":
		return tool.Descriptor{
			Name: "integrate_agent",
			Description: "Integrate a settled writing child through a durable preview-bound workflow. " +
				"Use op=preview first and inspect its digest, paths, conflicts, and unified diff. " +
				"Use op=apply with that preview_digest; apply revalidates child bytes, owned paths, " +
				"write claims, and parent baseline before the journaled write, then verifies the parent workspace. " +
				"Use discard to reject and clean up, or retry with a failed candidate digest to create a new preview. " +
				"Requires an open isolated child; do not close_agent first.",
			Visibility: o.visibility(), Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{
				Templates: []tool.ResourceTemplate{
					{Kind: "agent", Field: "agent_id", Access: tool.AccessWrite},
					{Kind: "process", ID: "git", Access: tool.AccessRead},
				},
				ChangesField: "changes",
			},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{"type": "string", "minLength": float64(1)},
					"op": map[string]any{
						"type": "string", "default": mergePreview,
						"enum": []any{mergePreview, mergeApply, mergeDiscard, mergeRetry},
					},
					"preview_digest": map[string]any{
						"type": "string", "minLength": float64(64), "maxLength": float64(64),
					},
					"paths": map[string]any{
						"type": "array", "items": map[string]any{"type": "string"},
					},
				},
				"required":             []string{"agent_id"},
				"additionalProperties": false,
			},
		}
	default:
		return o.tools.spawnDescriptor()
	}
}

func (o *operation) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if o == nil || o.tools == nil {
		return tool.Result{}, errors.New("agent tool is not configured")
	}
	switch o.kind {
	case "wait_agent":
		return o.tools.wait(ctx, raw)
	case "list_agents":
		return o.tools.list(ctx, raw)
	case "send_message":
		return o.tools.sendMessage(ctx, raw)
	case "followup_task":
		return o.tools.followUp(ctx, raw)
	case "interrupt_agent":
		return o.tools.interrupt(ctx, raw)
	case "close_agent":
		return o.tools.closeAgent(ctx, raw)
	case "integrate_agent":
		return o.tools.merge(ctx, raw)
	default:
		return o.tools.spawn(ctx, raw)
	}
}

func (t *Tool) spawnDescriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "spawn_agent",
		Description: "Create an asynchronous child agent after delegation admission. " +
			"Provide a short task_name, one objective, the expected output, and the authority trigger. " +
			"Roles: general|explore|plan|review|implementer|verifier|awaiter|custom. " +
			"Declare owned_paths for writing work; omit them for read-only roles. " +
			"context_mode defaults to task_capsule; use last_n_turns only when recent history is material. " +
			"full requires explicit authority or role policy. Parent context is captured by the runtime. " +
			"Returns agent_id, structured receipt, and a transcript var_handle for handle_read. " +
			"Use wait_agent, followup_task, interrupt_agent, list_agents, integrate_agent, and close_agent for control.",
		Visibility: t.visibility(), Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		ResourceResolver: tool.ResourceResolver{
			Templates: []tool.ResourceTemplate{
				{Kind: "agent", Field: "parent_id", Access: tool.AccessWrite},
			},
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_name":       map[string]any{"type": "string", "minLength": float64(1)},
				"objective":       map[string]any{"type": "string", "minLength": float64(1)},
				"expected_output": map[string]any{"type": "string", "minLength": float64(1)},
				"role": map[string]any{
					"type": "string",
					"enum": []any{
						"general", "explore", "plan", "review", "implementer", "verifier", "awaiter", "custom",
						"worker", "reviewer", "planner",
					},
				},
				"trigger": map[string]any{
					"type": "string",
					"enum": []any{"user", "developer", "skill", "system", "adaptive"},
				},
				"owned_paths": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
				},
				"parent_id": map[string]any{"type": "string"},
				"context_mode": map[string]any{
					"type":    "string",
					"enum":    []any{"fresh", "task_capsule", "last_n_turns", "full"},
					"default": "task_capsule",
				},
				"context_turns": map[string]any{
					"type": "integer", "minimum": float64(1), "maximum": float64(8),
					"default": float64(2),
				},
				"max_steps": map[string]any{
					"type": "integer", "minimum": float64(1),
				},
				"max_tokens": map[string]any{
					"type": "integer", "minimum": float64(1),
				},
				"max_cost_usd": map[string]any{
					"type": "number", "exclusiveMinimum": float64(0),
				},
			},
			"required": []string{
				"task_name", "role", "objective", "expected_output", "trigger",
			},
			"additionalProperties": false,
		},
	}
}

func (o *operation) visibility() tool.Visibility {
	if o == nil || o.tools == nil {
		return tool.VisibleInternal
	}
	return o.tools.visibility()
}

func (t *Tool) visibility() tool.Visibility {
	if t != nil && t.control != nil && t.control.Policy().ModelVisible() {
		return tool.VisibleModel
	}
	return tool.VisibleInternal
}

func agentSnapshot(agent subagent.Agent) map[string]any {
	snapshot := map[string]any{
		"agent_id": agent.ID, "agent_path": agent.Path, "revision": agent.Revision,
		"role": string(agent.Role), "profile": agent.Profile,
		"stance": string(agent.Stance), "depth": agent.Depth, "worktree": agent.Worktree,
		"isolated": agent.Isolated, "serialized": agent.Serialized, "base_rev": agent.BaseRev,
		"parent_id": agent.Parent, "parent_path": agent.ParentPath,
		"status":  string(agent.Status),
		"turn_id": agent.TurnID, "last_message": agent.LastMessage, "closed": agent.Closed,
		"task_name": agent.TaskName, "expected_output": agent.ExpectedOutput,
		"owned_paths": agent.OwnedPaths, "delegation_trigger": agent.DelegationTrigger,
	}
	// The structured result is the point of waiting on a child: without it the
	// parent would only learn that the child stopped, not what it established.
	if agent.Result != nil {
		snapshot["result"] = agent.Result
	}
	return snapshot
}

func (t *Tool) wait(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		AgentIDs  []string `json:"agent_ids"`
		TimeoutMS int64    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if caller, nested := t.callerAgent(ctx); nested {
		if len(input.AgentIDs) == 0 {
			for _, child := range t.control.List(subagent.ListFilter{
				SessionID: caller.SessionID, ParentID: caller.ID,
			}) {
				input.AgentIDs = append(input.AgentIDs, child.ID)
			}
			if len(input.AgentIDs) == 0 {
				return emptyWaitResult()
			}
		} else {
			for _, agentID := range input.AgentIDs {
				if !t.control.IsDescendant(caller.ID, agentID) {
					return tool.Result{}, fmt.Errorf(
						"agent %s may wait only on its descendant subtree",
						caller.ID,
					)
				}
			}
		}
	}
	timeout := time.Duration(0)
	if input.TimeoutMS > 0 {
		timeout = time.Duration(input.TimeoutMS) * time.Millisecond
	}
	if queued := t.serializedWaitTargets(
		t.invocationSession(ctx), input.AgentIDs,
	); len(queued) > 0 {
		agents := make([]map[string]any, 0, len(queued))
		for _, agent := range queued {
			agents = append(agents, agentSnapshot(agent))
		}
		body := map[string]any{
			"timed_out": false, "deferred": true, "agents": agents,
			"reason": "same-workspace children start after the current turn releases the workspace",
		}
		content, err := json.Marshal(body)
		if err != nil {
			return tool.Result{}, err
		}
		return tool.Result{
			Content: string(content),
			Metadata: map[string]any{
				"timed_out": false, "deferred": true, "count": len(agents),
			},
		}, nil
	}
	result, err := t.control.WaitSession(
		ctx, t.invocationSession(ctx), input.AgentIDs, timeout,
	)
	if err != nil {
		return tool.Result{}, err
	}
	agents := make([]map[string]any, 0, len(result.Agents))
	for _, agent := range result.Agents {
		agents = append(agents, agentSnapshot(agent))
	}
	body := map[string]any{"timed_out": result.TimedOut, "agents": agents}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"timed_out": result.TimedOut, "count": len(agents),
		},
	}, nil
}

func emptyWaitResult() (tool.Result, error) {
	content, err := json.Marshal(map[string]any{
		"timed_out": false, "agents": []any{},
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"timed_out": false, "count": 0},
	}, nil
}

func (t *Tool) serializedWaitTargets(
	sessionID string,
	agentIDs []string,
) []subagent.Agent {
	if len(agentIDs) == 0 {
		listed := t.control.List(subagent.ListFilter{
			SessionID: sessionID,
		})
		queued := make([]subagent.Agent, 0, len(listed))
		for _, agent := range listed {
			if agent.Serialized && !subagent.IsTerminal(agent.Status) {
				queued = append(queued, agent)
			}
		}
		return queued
	}
	queued := make([]subagent.Agent, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		agent, ok := t.control.Agent(agentID)
		if ok && agent.Serialized && !subagent.IsTerminal(agent.Status) {
			queued = append(queued, agent)
		}
	}
	return queued
}

func (t *Tool) list(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		ParentID      string `json:"parent_id"`
		IncludeClosed bool   `json:"include_closed"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	parentID := strings.TrimSpace(input.ParentID)
	if caller, nested := t.callerAgent(ctx); nested {
		if parentID != "" && parentID != caller.ID &&
			!t.control.IsDescendant(caller.ID, parentID) {
			return tool.Result{}, fmt.Errorf(
				"agent %s may list only its descendant subtree", caller.ID,
			)
		}
		if parentID == "" {
			parentID = caller.ID
		}
	}
	listed := t.control.List(subagent.ListFilter{
		SessionID: t.invocationSession(ctx),
		ParentID:  parentID, IncludeClosed: input.IncludeClosed,
	})
	agents := make([]map[string]any, 0, len(listed))
	for _, agent := range listed {
		agents = append(agents, agentSnapshot(agent))
	}
	body := map[string]any{"agents": agents, "count": len(agents)}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"count": len(agents)},
	}, nil
}

func (t *Tool) sendMessage(
	ctx context.Context,
	raw json.RawMessage,
) (tool.Result, error) {
	var input struct {
		AgentID string `json:"agent_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	agentID := strings.TrimSpace(input.AgentID)
	message := strings.TrimSpace(input.Message)
	if agentID == "" || message == "" {
		return tool.Result{}, errors.New("agent_id and message are required")
	}
	if err := t.authorizeTarget(ctx, agentID); err != nil {
		return tool.Result{}, err
	}
	if _, ok := t.control.Agent(agentID); !ok {
		return tool.Result{}, errors.New("agent not found")
	}
	body, err := json.Marshal(map[string]any{
		"kind": "message", "message": message,
	})
	if err != nil {
		return tool.Result{}, err
	}
	from := "parent"
	if caller, nested := t.callerAgent(ctx); nested {
		from = caller.ID
	}
	delivered, err := t.control.Mailbox().Enqueue(subagent.Message{
		SessionID: t.invocationSession(ctx), From: from, To: agentID,
		Kind: subagent.MessageContext, Body: body,
	})
	if err != nil {
		return tool.Result{}, err
	}
	content, err := json.Marshal(map[string]any{
		"agent_id": agentID, "sequence": delivered.Sequence, "queued": true,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"agent_id": agentID, "sequence": delivered.Sequence, "queued": true,
		},
	}, nil
}

func (t *Tool) followUp(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		AgentID string `json:"agent_id"`
		Prompt  string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	agentID := strings.TrimSpace(input.AgentID)
	prompt := strings.TrimSpace(input.Prompt)
	if agentID == "" || prompt == "" {
		return tool.Result{}, errors.New("agent_id and prompt are required")
	}
	if err := t.authorizeTarget(ctx, agentID); err != nil {
		return tool.Result{}, err
	}
	turn, err := t.control.FollowUp(ctx, agentID, prompt)
	if err != nil {
		return tool.Result{}, err
	}
	snap, _ := t.control.Agent(agentID)
	body := map[string]any{
		"agent_id": agentID, "turn": turn, "status": string(snap.Status),
		"follow_up": true,
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"agent_id": agentID, "turn": turn, "status": string(snap.Status),
		},
	}, nil
}

func (t *Tool) interrupt(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return tool.Result{}, errors.New("agent_id is required")
	}
	if err := t.authorizeTarget(ctx, agentID); err != nil {
		return tool.Result{}, err
	}
	prev, err := t.control.Interrupt(ctx, agentID)
	if err != nil {
		return tool.Result{}, err
	}
	snap, ok := t.control.Agent(agentID)
	status := subagent.StatusInterrupted
	if ok {
		status = snap.Status
	}
	body := map[string]any{
		"agent_id": agentID, "previous_status": string(prev), "status": string(status),
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"agent_id": agentID, "previous_status": string(prev), "status": string(status),
		},
	}, nil
}

func (t *Tool) closeAgent(
	ctx context.Context,
	raw json.RawMessage,
) (tool.Result, error) {
	var input struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return tool.Result{}, errors.New("agent_id is required")
	}
	if err := t.authorizeTarget(ctx, agentID); err != nil {
		return tool.Result{}, err
	}
	snap, _ := t.control.Agent(agentID)
	worktree := snap.Worktree
	if err := t.control.Close(agentID); err != nil {
		return tool.Result{}, err
	}
	if t.onRelease != nil {
		t.onRelease(agentID)
	}
	body := map[string]any{
		"agent_id": agentID, "status": string(subagent.StatusShutdown), "closed": true,
		"worktree": worktree,
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"agent_id": agentID, "status": string(subagent.StatusShutdown), "closed": true,
		},
	}, nil
}
