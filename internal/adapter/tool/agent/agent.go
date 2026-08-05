// Package agent exposes model-visible agent tools for spawning and controlling
// isolated sub-agents through subagent.Manager, rlm.Governor, and a shared handle store.
// Hosts must only render receipts/events; control plane is these tools + Manager.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/handle"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
)

type Options struct {
	Manager       *subagent.Manager
	Handles       *handle.Store
	Governor      *rlm.Governor
	SessionID     string
	Root          string
	Gate          subagent.ToolGate
	Budget        subagent.Budget
	Runtime       subagent.RuntimeHost
	ParentContext func() string
	Graph         subagent.Graph
	// OnRelease lets the host drop per-agent runtime state (thread engine, wall
	// clock watchdog) when the model closes an agent.
	OnRelease func(agentID string)
	// Files applies merge writes into the parent workspace (RFC-006 D9).
	Files *filetool.Tools
	// Workspace is the parent workspace root used for baseline fingerprinting.
	Workspace string
}

type Tool struct {
	manager       *subagent.Manager
	handles       *handle.Store
	governor      *rlm.Governor
	sessionID     string
	parentContext func() string
	onRelease     func(agentID string)
	files         *filetool.Tools
	workspace     string
}

// ArtifactRef is a compact pointer to a child-produced artifact.
type ArtifactRef struct {
	Kind string `json:"kind"`
	Ref  any    `json:"ref"`
}

// Usage accounts for child token spend. At spawn time the child has not run yet,
// so Status is "pending" and the real numbers arrive with the child's Result.
type Usage struct {
	Status string `json:"status"`
}

// Verification distinguishes self-reported child claims from gate-proven evidence.
type Verification struct {
	Status   string `json:"status"`
	Evidence string `json:"evidence,omitempty"`
}

// Receipt is the structured agent run receipt returned to the parent.
type Receipt struct {
	RunID        string         `json:"run_id"`
	AgentID      string         `json:"agent_id"`
	ThreadID     string         `json:"thread_id"`
	Turn         string         `json:"turn"`
	Role         string         `json:"role"`
	Profile      string         `json:"profile"`
	Stance       string         `json:"stance"`
	FollowUp     bool           `json:"follow_up"`
	Takeover     bool           `json:"takeover"`
	Artifacts    []ArtifactRef  `json:"artifacts"`
	Usage        Usage          `json:"usage"`
	Verification Verification   `json:"verification"`
	WorkerRecord map[string]any `json:"worker_record"`
}

func Register(registry *tool.Registry, options Options) error {
	if registry == nil {
		return errors.New("agent tool registry is required")
	}
	if options.Handles == nil {
		return errors.New("agent handle store is required")
	}
	sessionID := strings.TrimSpace(options.SessionID)
	if sessionID == "" {
		sessionID = "session-local"
	}
	manager := options.Manager
	if manager == nil {
		if options.Gate == nil {
			return errors.New("agent tool gate is required when manager is nil")
		}
		root := strings.TrimSpace(options.Root)
		if root == "" {
			return errors.New("agent root is required when manager is nil")
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		opened, err := subagent.Open(subagent.Options{
			Root: root, Budget: options.Budget, Gate: options.Gate, Runtime: options.Runtime,
		})
		if err != nil {
			return err
		}
		manager = opened
	}
	if options.Graph != nil {
		if err := manager.AttachGraph(options.Graph); err != nil {
			return fmt.Errorf("attach agent graph: %w", err)
		}
	}
	governor := options.Governor
	if governor == nil {
		governor = rlm.NewGovernor(rlm.Limits{})
	}
	shared := &Tool{
		manager: manager, handles: options.Handles,
		governor: governor, sessionID: sessionID,
		parentContext: options.ParentContext,
		onRelease:     options.OnRelease,
		files:         options.Files,
		workspace:     strings.TrimSpace(options.Workspace),
	}
	for _, kind := range []string{
		"agent", "agent_wait", "agent_list", "agent_followup", "agent_interrupt", "agent_close",
		"agent_merge",
	} {
		if err := registry.Register(&operation{tools: shared, kind: kind}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tool) spawn(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if t == nil || t.manager == nil || t.handles == nil || t.governor == nil {
		return tool.Result{}, errors.New("agent tool is not configured")
	}
	var input struct {
		Prompt        string `json:"prompt"`
		Role          string `json:"role"`
		ParentID      string `json:"parent_id"`
		ForkContext   bool   `json:"fork_context"`
		ParentContext string `json:"parent_context"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		return tool.Result{}, errors.New("prompt is required")
	}
	role, err := subagent.ParseRole(input.Role)
	if err != nil {
		return tool.Result{}, err
	}
	parentID := strings.TrimSpace(input.ParentID)
	depth := 0
	if parentID != "" {
		parent, ok := t.manager.Agent(parentID)
		if !ok {
			return tool.Result{}, errors.New("parent agent unavailable")
		}
		depth = parent.Depth + 1
	}
	// Admission here only guards the spawn itself, and the lease is released
	// before the turn starts: the child turn takes a lease of its own for its
	// whole lifetime, and counting the same child twice would make max_parallel
	// bind one child too early. Real token spend is charged from the receipt the
	// child produces, so nothing is charged for work that has not run.
	lease, err := t.governor.Admit(depth, 0, 0)
	if err != nil {
		return tool.Result{}, err
	}
	child, err := t.manager.Spawn(parentID, role, prompt)
	t.governor.Release(lease)
	if err != nil {
		return tool.Result{}, err
	}
	childPrompt, forkEmpty := t.buildChildPrompt(input.ForkContext, input.ParentContext, child, prompt)
	turn, err := t.manager.Takeover(ctx, child.ID, childPrompt)
	if err != nil {
		_ = t.manager.Close(child.ID)
		return tool.Result{}, err
	}
	threadID := subagent.ThreadIDFor(child.ID)
	transcript := fmt.Sprintf(
		"agent_id=%s\nthread_id=%s\nrole=%s\nprofile=%s\nstance=%s\ndepth=%d\nworktree=%s\nparent=%s\nfork_context=%v\nfork_context_empty=%v\nprompt=%s\nturn=%s\n",
		child.ID, threadID, child.Role, child.Profile, child.Stance, child.Depth, child.Worktree, child.Parent,
		input.ForkContext, forkEmpty, childPrompt, turn,
	)
	handleName := filepath.ToSlash(filepath.Join("agent-"+child.ID, "transcript"))
	varHandle, err := t.handles.PutText(t.sessionID, handleName, transcript)
	if err != nil {
		_ = t.manager.Close(child.ID)
		return tool.Result{}, err
	}
	receipt := Receipt{
		RunID: child.ID, AgentID: child.ID, ThreadID: threadID, Turn: turn,
		Role: string(child.Role), Profile: child.Profile, Stance: string(child.Stance),
		FollowUp: false, Takeover: true,
		Artifacts: []ArtifactRef{{Kind: "transcript_handle", Ref: varHandle}},
		// The child turn has only just been submitted here: claiming a
		// verification status now would be a claim about work that has not run.
		Usage:        Usage{Status: "pending"},
		Verification: Verification{Status: "pending"},
		WorkerRecord: map[string]any{
			"worktree": child.Worktree, "serialized": child.Serialized,
			"parent_id": child.Parent, "depth": child.Depth,
		},
	}
	receiptBody, err := json.Marshal(receipt)
	if err != nil {
		_ = t.manager.Close(child.ID)
		return tool.Result{}, err
	}
	mailboxTo := parentID
	if mailboxTo == "" {
		mailboxTo = "parent"
	}
	message, err := t.manager.Mailbox().Deliver(child.ID, mailboxTo, receiptBody)
	if err != nil {
		_ = t.manager.Close(child.ID)
		return tool.Result{}, err
	}
	body := map[string]any{
		"agent_id": child.ID, "thread_id": threadID, "role": string(child.Role),
		"profile": child.Profile, "stance": string(child.Stance),
		"depth": child.Depth, "worktree": child.Worktree,
		"serialized": child.Serialized,
		"parent_id":  child.Parent, "turn": turn,
		"status":       string(subagent.StatusRunning),
		"fork_context": input.ForkContext, "fork_context_empty": forkEmpty,
		"receipt": map[string]any{
			"sequence": message.Sequence, "from": message.From, "to": message.To,
			"body": json.RawMessage(message.Body),
		},
		"run_receipt":       receipt,
		"transcript_handle": varHandle,
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"agent_id": child.ID, "thread_id": threadID,
			"receipt_sequence":  message.Sequence,
			"transcript_handle": varHandle,
			"verification":      receipt.Verification.Status,
			"usage":             receipt.Usage.Status,
			"status":            string(subagent.StatusRunning),
			"serialized":        child.Serialized,
		},
	}, nil
}

func (t *Tool) buildChildPrompt(fork bool, explicitParent string, child *subagent.Agent, task string) (string, bool) {
	roleLine := fmt.Sprintf("role=%s profile=%s stance=%s", child.Role, child.Profile, child.Stance)
	if !fork {
		return roleLine + "\n" + task, false
	}
	prefix := strings.TrimSpace(explicitParent)
	if t.parentContext != nil {
		if fromHost := strings.TrimSpace(t.parentContext()); fromHost != "" {
			prefix = fromHost
		}
	}
	if prefix == "" {
		return roleLine + "\n" + task, true
	}
	return prefix + "\n---\n" + roleLine + "\n" + task, false
}

// Manager exposes the underlying manager for tests and cleanup.
func (t *Tool) Manager() *subagent.Manager { return t.manager }
