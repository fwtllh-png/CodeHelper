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

// operation dispatches model-visible agent control-plane tools onto Manager.
type operation struct {
	tools *Tool
	kind  string
}

func (o *operation) Descriptor() tool.Descriptor {
	switch o.kind {
	case "agent_wait":
		return tool.Descriptor{
			Name: "agent_wait",
			Description: "Wait until listed child agents reach a terminal status " +
				"(completed|errored|interrupted|shutdown). Empty agent_ids waits for any. " +
				"timeout_ms>0 returns timed_out without error when the deadline elapses. " +
				"A queued same-workspace child returns deferred immediately because it can start only " +
				"after the calling turn releases the workspace.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelSerial,
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
	case "agent_list":
		return tool.Descriptor{
			Name: "agent_list",
			Description: "List child agents managed by this session. " +
				"Optionally filter by parent_id; include_closed lists shutdown agents.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
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
	case "agent_followup":
		return tool.Descriptor{
			Name: "agent_followup",
			Description: "Send a follow-up turn to a resident child agent. " +
				"Fails if the agent is running, closed, or missing. " +
				"Interrupt or wait for completion before following up.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "agent", Field: "agent_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{"type": "string", "minLength": float64(1)},
					"prompt":   map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"agent_id", "prompt"},
				"additionalProperties": false,
			},
		}
	case "agent_interrupt":
		return tool.Descriptor{
			Name: "agent_interrupt",
			Description: "Interrupt a running child agent turn. The agent stays open " +
				"(worktree retained) so agent_followup can resume.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
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
	case "agent_close":
		return tool.Descriptor{
			Name: "agent_close",
			Description: "Close a child agent, free its concurrency slot, and cleanup its worktree. " +
				"Merge with agent_merge first if you need the child's writes in the parent workspace; " +
				"Close discards the worktree. Prefer agent_wait until terminal status before closing.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
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
	case "agent_merge":
		return tool.Descriptor{
			Name: "agent_merge",
			Description: "Apply a writing child's worktree changes into the parent workspace via " +
				"the same validate-then-apply path as file_apply (journal + turnDiff + Verify Gate). " +
				"dry_run defaults to true — preview the unified diff before applying. " +
				"Fails on write-claim conflicts or when the parent drifted from the child's base revision. " +
				"Requires an open isolated child with a settled result; do not agent_close first.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{
				Templates: []tool.ResourceTemplate{{
					Kind: "agent", Field: "agent_id", Access: tool.AccessWrite,
				}},
				ChangesField: "changes",
			},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"agent_id": map[string]any{"type": "string", "minLength": float64(1)},
					"dry_run":  map[string]any{"type": "boolean", "default": true},
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
	case "agent_wait":
		return o.tools.wait(ctx, raw)
	case "agent_list":
		return o.tools.list(raw)
	case "agent_followup":
		return o.tools.followUp(ctx, raw)
	case "agent_interrupt":
		return o.tools.interrupt(ctx, raw)
	case "agent_close":
		return o.tools.closeAgent(raw)
	case "agent_merge":
		return o.tools.merge(ctx, raw)
	default:
		var input struct {
			Op string `json:"op"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return tool.Result{}, err
		}
		switch strings.TrimSpace(input.Op) {
		case "", "spawn":
			return o.tools.spawn(ctx, raw)
		case "wait":
			return o.tools.wait(ctx, raw)
		case "list":
			return o.tools.list(raw)
		case "followup":
			return o.tools.followUp(ctx, raw)
		case "interrupt":
			return o.tools.interrupt(ctx, raw)
		case "close":
			return o.tools.closeAgent(raw)
		case "merge":
			return o.tools.merge(ctx, raw)
		default:
			return tool.Result{}, fmt.Errorf("unsupported agent operation %q", input.Op)
		}
	}
}

func (t *Tool) spawnDescriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "agent",
		Description: "Spawn or control child agents. Omit op (or use op=spawn) to create an " +
			"isolated child with its own worktree and mailbox. Compatibility operations: " +
			"list, wait, followup, interrupt, close, merge; the dedicated agent_* tools remain available. " +
			"Roles: general|explore|plan|review|implementer|verifier|custom " +
			"(aliases: worker→general, reviewer→review, planner→plan). " +
			"fork_context:true inherits the parent prompt prefix when available. " +
			"Returns agent_id, structured receipt, and a transcript var_handle for handle_read. " +
			"Use agent_wait / agent_followup / agent_interrupt / agent_list / agent_merge / agent_close for control.",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		ResourceResolver: tool.ResourceResolver{
			Templates: []tool.ResourceTemplate{
				{Kind: "agent", Field: "parent_id", Access: tool.AccessWrite},
				{Kind: "agent", Field: "agent_id", Access: tool.AccessWrite},
			},
			ChangesField: "changes",
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"op": map[string]any{
					"type": "string",
					"enum": []any{"spawn", "list", "wait", "followup", "interrupt", "close", "merge"},
				},
				"prompt": map[string]any{"type": "string", "minLength": float64(1)},
				"role": map[string]any{
					"type": "string",
					"enum": []any{
						"general", "explore", "plan", "review", "implementer", "verifier", "custom",
						"worker", "reviewer", "planner",
					},
				},
				"parent_id":      map[string]any{"type": "string"},
				"fork_context":   map[string]any{"type": "boolean"},
				"parent_context": map[string]any{"type": "string"},
				"agent_id":       map[string]any{"type": "string", "minLength": float64(1)},
				"agent_ids": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
				},
				"timeout_ms":     map[string]any{"type": "integer"},
				"include_closed": map[string]any{"type": "boolean"},
				"dry_run":        map[string]any{"type": "boolean"},
				"paths": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
				},
			},
			"oneOf": []any{
				map[string]any{
					"properties": map[string]any{"op": map[string]any{"const": "spawn"}},
					"required":   []string{"prompt"},
				},
				map[string]any{
					"properties": map[string]any{"op": map[string]any{"const": "list"}},
					"required":   []string{"op"},
				},
				map[string]any{
					"properties": map[string]any{"op": map[string]any{"const": "wait"}},
					"required":   []string{"op"},
				},
				map[string]any{
					"properties": map[string]any{"op": map[string]any{"const": "followup"}},
					"required":   []string{"op", "agent_id", "prompt"},
				},
				map[string]any{
					"properties": map[string]any{"op": map[string]any{"const": "interrupt"}},
					"required":   []string{"op", "agent_id"},
				},
				map[string]any{
					"properties": map[string]any{"op": map[string]any{"const": "close"}},
					"required":   []string{"op", "agent_id"},
				},
				map[string]any{
					"properties": map[string]any{"op": map[string]any{"const": "merge"}},
					"required":   []string{"op", "agent_id"},
				},
			},
			"additionalProperties": false,
		},
	}
}

func agentSnapshot(agent subagent.Agent) map[string]any {
	snapshot := map[string]any{
		"agent_id": agent.ID, "role": string(agent.Role), "profile": agent.Profile,
		"stance": string(agent.Stance), "depth": agent.Depth, "worktree": agent.Worktree,
		"isolated": agent.Isolated, "serialized": agent.Serialized, "base_rev": agent.BaseRev,
		"parent_id": agent.Parent, "status": string(agent.Status),
		"turn_id": agent.TurnID, "last_message": agent.LastMessage, "closed": agent.Closed,
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
	timeout := time.Duration(0)
	if input.TimeoutMS > 0 {
		timeout = time.Duration(input.TimeoutMS) * time.Millisecond
	}
	if queued := t.serializedWaitTargets(input.AgentIDs); len(queued) > 0 {
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
	result, err := t.manager.Wait(ctx, input.AgentIDs, timeout)
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

func (t *Tool) serializedWaitTargets(agentIDs []string) []subagent.Agent {
	if len(agentIDs) == 0 {
		listed := t.manager.List(subagent.ListFilter{})
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
		agent, ok := t.manager.Agent(agentID)
		if ok && agent.Serialized && !subagent.IsTerminal(agent.Status) {
			queued = append(queued, agent)
		}
	}
	return queued
}

func (t *Tool) list(raw json.RawMessage) (tool.Result, error) {
	var input struct {
		ParentID      string `json:"parent_id"`
		IncludeClosed bool   `json:"include_closed"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	listed := t.manager.List(subagent.ListFilter{
		ParentID: strings.TrimSpace(input.ParentID), IncludeClosed: input.IncludeClosed,
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
	turn, err := t.manager.FollowUp(ctx, agentID, prompt)
	if err != nil {
		return tool.Result{}, err
	}
	snap, _ := t.manager.Agent(agentID)
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
	prev, err := t.manager.Interrupt(ctx, agentID)
	if err != nil {
		return tool.Result{}, err
	}
	snap, ok := t.manager.Agent(agentID)
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

func (t *Tool) closeAgent(raw json.RawMessage) (tool.Result, error) {
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
	snap, _ := t.manager.Agent(agentID)
	worktree := snap.Worktree
	if err := t.manager.Close(agentID); err != nil {
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
