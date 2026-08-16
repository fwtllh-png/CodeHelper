// Package task exposes durable Task repository operations as model-visible tools.
package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	defaultKind   = "agent"
	workBoardKind = "work_board"
	defaultLimit  = 50
	gateLogLimit  = 8 << 10
)

type Options struct {
	Repository *taskstate.Repository
	SessionID  string
	Workspace  string
	Backend    sandbox.Backend
}

type Tools struct {
	repo      *taskstate.Repository
	sessionID string
	workspace *sandbox.Workspace
	backend   sandbox.Backend

	mu           sync.Mutex
	activeTaskID string
}

type executor struct {
	tools *Tools
	name  string
}

func Register(registry *tool.Registry, options Options) error {
	if registry == nil {
		return errors.New("task tool registry is required")
	}
	if options.Repository == nil {
		return errors.New("task repository is required")
	}
	sessionID := strings.TrimSpace(options.SessionID)
	if sessionID == "" {
		sessionID = "session-local"
	}
	root := strings.TrimSpace(options.Workspace)
	if root == "" {
		root = "."
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	backend := options.Backend
	if backend != nil {
		backend, err = sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
		if err != nil {
			return err
		}
	}
	tools := &Tools{
		repo: options.Repository, sessionID: sessionID,
		workspace: workspace, backend: backend,
	}
	for _, name := range []string{
		"task_create", "task_list", "task_read", "task_cancel",
		"work_update", "note", "task_gate_run",
	} {
		if err := registry.Register(&executor{tools: tools, name: name}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) Descriptor() tool.Descriptor {
	switch e.name {
	case "task_create":
		return tool.Descriptor{
			Name: "task_create",
			Description: "Enqueue a durable background task. Returns task_id; use task_gate_run " +
				"for verification evidence and task_read for lifecycle status.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "task", Field: "task_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"kind":    map[string]any{"type": "string", "minLength": float64(1)},
					"title":   map[string]any{"type": "string"},
					"task_id": map[string]any{"type": "string", "minLength": float64(1)},
					"payload": map[string]any{
						"type": "object",
						"description": "Executor-specific payload. For kind=workflow_run use " +
							"{version, idempotent, spec:{goal, budget, permissions, nodes}}; " +
							"keep the spec wrapper and do not flatten its fields.",
					},
				},
				"additionalProperties": false,
			},
		}
	case "task_list":
		return tool.Descriptor{
			Name:        "task_list",
			Description: "List durable tasks for the current session, newest first.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "session", ID: "tasks", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"state": map[string]any{
						"type": "string",
						"enum": []any{"queued", "running", "waiting", "failed", "canceled", "completed"},
					},
					"limit": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(1000)},
				},
				"additionalProperties": false,
			},
		}
	case "task_read":
		return tool.Descriptor{
			Name:        "task_read",
			Description: "Read a durable task, including payload gates/artifacts and lifecycle timeline.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "task", Field: "task_id", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"task_id"},
				"additionalProperties": false,
			},
		}
	case "task_cancel":
		return tool.Descriptor{
			Name:        "task_cancel",
			Description: "Cancel a durable task. Requires approval when ToolGuard is enabled.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "task", Field: "task_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id": map[string]any{"type": "string", "minLength": float64(1)},
					"reason":  map[string]any{"type": "string"},
				},
				"required":             []string{"task_id"},
				"additionalProperties": false,
			},
		}
	case "work_update":
		return tool.Descriptor{
			Name:        "work_update",
			Description: "Replace the thread work / todo board for the current session.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "work", ID: "board", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"items": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":      map[string]any{"type": "string"},
								"content": map[string]any{"type": "string", "minLength": float64(1)},
								"status": map[string]any{
									"type": "string",
									"enum": []any{"pending", "in_progress", "completed", "canceled"},
								},
							},
							"required":             []string{"content"},
							"additionalProperties": false,
						},
					},
				},
				"required":             []string{"items"},
				"additionalProperties": false,
			},
		}
	case "note":
		return tool.Descriptor{
			Name:        "note",
			Description: "Append a durable one-off note to the session work board for later recall.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "work", ID: "notes", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"text"},
				"additionalProperties": false,
			},
		}
	case "task_gate_run":
		return tool.Descriptor{
			Name: "task_gate_run",
			Description: "Run a verification command and attach structured gate evidence to the " +
				"active durable task (command, exit code, summary, truncated log).",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
			AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
				{Kind: "task", Field: "task_id", Access: tool.AccessWrite},
				{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
				{Kind: "process", ID: "workspace", Access: tool.AccessWrite, Tree: true},
			}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"task_id":    map[string]any{"type": "string", "minLength": float64(1)},
					"command":    map[string]any{"type": "string", "minLength": float64(1)},
					"cwd":        map[string]any{"type": "string"},
					"timeout_ms": map[string]any{"type": "integer"},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		}
	default:
		return tool.Descriptor{Name: e.name, Availability: tool.AvailabilityUnavailable}
	}
}

func (e *executor) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	switch e.name {
	case "task_create":
		return e.tools.create(ctx, raw)
	case "task_list":
		return e.tools.list(ctx, raw)
	case "task_read":
		return e.tools.read(ctx, raw)
	case "task_cancel":
		return e.tools.cancel(ctx, raw)
	case "work_update":
		return e.tools.workUpdate(ctx, raw)
	case "note":
		return e.tools.note(ctx, raw)
	case "task_gate_run":
		return e.tools.gateRun(ctx, raw)
	default:
		return tool.Result{}, fmt.Errorf("unknown task tool %q", e.name)
	}
}

func (t *Tools) create(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Kind    string          `json:"kind"`
		Title   string          `json:"title"`
		TaskID  string          `json:"task_id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if err := t.repo.EnsureSession(ctx, t.sessionID, t.workspace.Root()); err != nil {
		return tool.Result{}, err
	}
	kind := strings.TrimSpace(input.Kind)
	if kind == "" {
		kind = defaultKind
	}
	id := strings.TrimSpace(input.TaskID)
	if id == "" {
		id = fmt.Sprintf("task_%d", time.Now().UTC().UnixNano())
	}
	payload := map[string]any{}
	if len(input.Payload) > 0 {
		if err := json.Unmarshal(input.Payload, &payload); err != nil {
			return tool.Result{}, fmt.Errorf("payload: %w", err)
		}
	}
	executor := executableKind(kind)
	payload = normalizeExecutablePayload(executor, payload)
	if title := strings.TrimSpace(input.Title); title != "" && executor == "" {
		payload["title"] = title
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	created, err := t.repo.Create(ctx, taskstate.Task{
		ID: id, SessionID: t.sessionID, Kind: kind, State: taskstate.StateQueued,
		Payload: encoded, Executor: executor,
	})
	if err != nil {
		return tool.Result{}, err
	}
	t.setActive(created.ID)
	return tool.Result{
		Content: created.ID,
		Metadata: map[string]any{
			"task_id": created.ID, "session_id": created.SessionID,
			"kind": created.Kind, "state": string(created.State),
		},
	}, nil
}

func executableKind(kind string) string {
	switch kind {
	case taskstate.ExecutorAgentTurn,
		taskstate.ExecutorWorkflowRun,
		taskstate.ExecutorShellCommand:
		return kind
	default:
		return ""
	}
}

func normalizeExecutablePayload(
	executor string,
	payload map[string]any,
) map[string]any {
	if executor != taskstate.ExecutorWorkflowRun || payload["spec"] != nil {
		return payload
	}
	if _, hasGoal := payload["goal"]; !hasGoal {
		return payload
	}
	spec := make(map[string]any, len(payload))
	maps.Copy(spec, payload)
	version := any(float64(1))
	if value, exists := spec["version"]; exists {
		version = value
		delete(spec, "version")
	}
	normalized := map[string]any{"version": version, "spec": spec}
	if value, exists := spec["idempotent"]; exists {
		normalized["idempotent"] = value
		delete(spec, "idempotent")
	}
	return normalized
}

func (t *Tools) list(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		State string `json:"state"`
		Limit int    `json:"limit"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return tool.Result{}, err
		}
	}
	limit := input.Limit
	if limit == 0 {
		limit = defaultLimit
	}
	filter := taskstate.Filter{SessionID: t.sessionID}
	if input.State != "" {
		filter.State = taskstate.State(input.State)
	}
	values, err := t.repo.List(ctx, filter, limit)
	if err != nil {
		return tool.Result{}, err
	}
	summaries := make([]map[string]any, 0, len(values))
	for _, value := range values {
		summaries = append(summaries, map[string]any{
			"task_id": value.ID, "kind": value.Kind, "state": string(value.State),
			"updated_at": value.UpdatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	content, err := json.Marshal(summaries)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"count": len(summaries), "session_id": t.sessionID,
		},
	}, nil
}

func (t *Tools) read(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	value, err := t.repo.Get(ctx, strings.TrimSpace(input.TaskID))
	if err != nil {
		return tool.Result{}, err
	}
	lifecycle, err := t.repo.Lifecycle(ctx, value.ID)
	if err != nil {
		return tool.Result{}, err
	}
	timeline := make([]map[string]any, 0, len(lifecycle))
	for _, entry := range lifecycle {
		timeline = append(timeline, map[string]any{
			"sequence": entry.Sequence, "state": string(entry.State),
			"reason": entry.Reason, "created_at": entry.CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	body := map[string]any{
		"task_id": value.ID, "session_id": value.SessionID, "thread_id": value.ThreadID,
		"turn_id": value.TurnID, "kind": value.Kind, "state": string(value.State),
		"lifecycle_sequence": value.LifecycleSequence,
		"payload":            json.RawMessage(value.Payload),
		"result":             json.RawMessage(value.Result),
		"failure_reason":     value.FailureReason, "lifecycle": timeline,
		"created_at": value.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at": value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"task_id": value.ID, "state": string(value.State),
		},
	}, nil
}

func (t *Tools) cancel(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		TaskID string `json:"task_id"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	updated, err := t.repo.Cancel(ctx, strings.TrimSpace(input.TaskID), strings.TrimSpace(input.Reason), time.Time{})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: updated.ID,
		Metadata: map[string]any{
			"task_id": updated.ID, "state": string(updated.State),
			"reason": updated.FailureReason,
		},
	}, nil
}

func (t *Tools) workUpdate(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	board, err := t.ensureWorkBoard(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	items := make([]map[string]any, 0, len(input.Items))
	for index, item := range input.Items {
		content, _ := item["content"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			return tool.Result{}, fmt.Errorf("items[%d].content is required", index)
		}
		status, _ := item["status"].(string)
		if status == "" {
			status = "pending"
		}
		id, _ := item["id"].(string)
		if strings.TrimSpace(id) == "" {
			id = fmt.Sprintf("item_%d", index+1)
		}
		items = append(items, map[string]any{
			"id": id, "content": content, "status": status,
		})
	}
	payload := boardPayload(board)
	payload["items"] = items
	updated, err := t.repo.PatchPayload(ctx, board.ID, payload)
	if err != nil {
		return tool.Result{}, err
	}
	content, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"task_id": updated.ID, "count": len(items),
		},
	}, nil
}

func (t *Tools) note(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	text := strings.TrimSpace(input.Text)
	if text == "" {
		return tool.Result{}, errors.New("text is required")
	}
	board, err := t.ensureWorkBoard(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	payload := boardPayload(board)
	notes, _ := payload["notes"].([]any)
	entry := map[string]any{
		"text": text, "created_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	notes = append(notes, entry)
	payload["notes"] = notes
	updated, err := t.repo.PatchPayload(ctx, board.ID, payload)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: text,
		Metadata: map[string]any{
			"task_id": updated.ID, "notes": len(notes),
		},
	}, nil
}

func (t *Tools) gateRun(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		TaskID    string `json:"task_id"`
		Command   string `json:"command"`
		CWD       string `json:"cwd"`
		TimeoutMS int64  `json:"timeout_ms"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		taskID = t.active()
	}
	if taskID == "" {
		return tool.Result{}, errors.New("task_id is required (or create a task first)")
	}
	current, err := t.repo.Get(ctx, taskID)
	if err != nil {
		return tool.Result{}, err
	}
	if current.State == taskstate.StateQueued {
		current, err = t.repo.Update(ctx, taskID, taskstate.Transition{State: taskstate.StateRunning})
		if err != nil {
			return tool.Result{}, err
		}
	}
	if current.State != taskstate.StateRunning && current.State != taskstate.StateWaiting {
		return tool.Result{}, fmt.Errorf("task %s is %s; gate_run requires running or waiting", taskID, current.State)
	}
	if current.State == taskstate.StateWaiting {
		current, err = t.repo.Update(ctx, taskID, taskstate.Transition{State: taskstate.StateRunning})
		if err != nil {
			return tool.Result{}, err
		}
	}
	directory, err := t.workspace.ResolveDirectory(input.CWD)
	if err != nil {
		return tool.Result{}, err
	}
	directoryFile, err := t.workspace.OpenDirectory(input.CWD)
	if err != nil {
		return tool.Result{}, err
	}
	defer directoryFile.Close()
	runCtx := ctx
	if input.TimeoutMS > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(input.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	started := time.Now().UTC()
	result, err := process.Run(runCtx, process.Options{
		Command: input.Command, Dir: directory, DirFile: directoryFile,
		Sandbox: t.backend, RequireStrongSandbox: true,
		WorkspaceReadOnly: true,
	})
	if err != nil {
		return tool.Result{}, err
	}
	durationMS := time.Since(started).Milliseconds()
	logBody := result.Stdout
	if result.Stderr != "" {
		if logBody != "" {
			logBody += "\n"
		}
		logBody += "[stderr]\n" + result.Stderr
	}
	summary := truncate(logBody, 240)
	classification := "passed"
	if result.ExitCode != 0 {
		classification = "failed"
	}
	gate := map[string]any{
		"command": input.Command, "cwd": directory, "exit_code": result.ExitCode,
		"duration_ms": durationMS, "classification": classification,
		"summary": summary, "log": truncate(logBody, gateLogLimit),
		"at": started.Format(time.RFC3339Nano),
	}
	payload := boardPayload(current)
	gates, _ := payload["gates"].([]any)
	gates = append(gates, gate)
	payload["gates"] = gates
	if _, err := t.repo.PatchPayload(ctx, taskID, payload); err != nil {
		return tool.Result{}, err
	}
	if result.ExitCode != 0 {
		if _, err := t.repo.Update(ctx, taskID, taskstate.Transition{
			State: taskstate.StateWaiting, Reason: "gate_failed",
		}); err != nil {
			return tool.Result{}, err
		}
	}
	t.setActive(taskID)
	content, err := json.Marshal(gate)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		IsError: result.ExitCode != 0,
		Metadata: map[string]any{
			"task_id": taskID, "exit_code": result.ExitCode,
			"classification": classification, "duration_ms": durationMS,
		},
	}, nil
}

func (t *Tools) ensureWorkBoard(ctx context.Context) (taskstate.Task, error) {
	if err := t.repo.EnsureSession(ctx, t.sessionID, t.workspace.Root()); err != nil {
		return taskstate.Task{}, err
	}
	id := "work_" + t.sessionID
	current, err := t.repo.Get(ctx, id)
	if err == nil {
		return current, nil
	}
	if !errors.Is(err, taskstate.ErrNotFound) {
		return taskstate.Task{}, err
	}
	payload, _ := json.Marshal(map[string]any{
		"items": []any{}, "notes": []any{},
	})
	return t.repo.Create(ctx, taskstate.Task{
		ID: id, SessionID: t.sessionID, Kind: workBoardKind,
		State: taskstate.StateQueued, Payload: payload,
	})
}

func boardPayload(value taskstate.Task) map[string]any {
	payload := map[string]any{}
	if len(value.Payload) > 0 {
		_ = json.Unmarshal(value.Payload, &payload)
	}
	return payload
}

func (t *Tools) setActive(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.activeTaskID = id
}

func (t *Tools) active() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activeTaskID
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
