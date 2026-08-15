// Package automation exposes the durable automation ledger as model-visible tools.
package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	automationstore "github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
)

type Options struct {
	Repository *automationstore.Repository
	SessionID  string
	Workspace  string
}

type Tools struct {
	repo      *automationstore.Repository
	sessionID string
	workspace string
}

type executor struct {
	tools *Tools
	name  string
}

type input struct {
	Name            string          `json:"name"`
	RRULE           string          `json:"rrule"`
	AutomationID    string          `json:"automation_id"`
	TaskKind        string          `json:"task_kind"`
	TaskPayload     json.RawMessage `json:"task_payload"`
	Status          string          `json:"status"`
	ExpectedVersion uint64          `json:"expected_version"`
}

func Register(registry *tool.Registry, options Options) error {
	if registry == nil {
		return errors.New("automation tool registry is required")
	}
	if options.Repository == nil {
		return errors.New("automation repository is required")
	}
	sessionID := strings.TrimSpace(options.SessionID)
	if sessionID == "" {
		sessionID = "session-local"
	}
	workspace := strings.TrimSpace(options.Workspace)
	if workspace == "" {
		workspace = "."
	}
	tools := &Tools{
		repo: options.Repository, sessionID: sessionID, workspace: workspace,
	}
	for _, name := range []string{
		"automation_create", "automation_list", "automation_read",
		"automation_update", "automation_pause", "automation_resume",
		"automation_delete", "automation_run",
	} {
		instance := &executor{tools: tools, name: name}
		typedExecutor, err := instance.typedExecutor()
		if err != nil {
			return err
		}
		if err := registry.Register(typedExecutor, nil); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) Descriptor() tool.Descriptor {
	switch e.name {
	case "automation_create":
		return tool.Descriptor{
			Name: "automation_create",
			Description: "Create a recurring automation that enqueues durable tasks on schedule. " +
				"RRULE supports FREQ=HOURLY|WEEKLY with optional INTERVAL/BYDAY (UTC).",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			RepeatPolicy:       tool.RepeatExecute,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "automation", Field: "automation_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":          map[string]any{"type": "string", "minLength": float64(1)},
					"rrule":         map[string]any{"type": "string", "minLength": float64(1)},
					"automation_id": map[string]any{"type": "string", "minLength": float64(1)},
					"task_kind":     map[string]any{"type": "string"},
					"task_payload":  map[string]any{"type": "object"},
				},
				"required":             []string{"name", "rrule"},
				"additionalProperties": false,
			},
		}
	case "automation_list":
		return tool.Descriptor{
			Name:        "automation_list",
			Description: "List non-deleted automations for the current session.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			RepeatPolicy:       tool.RepeatExecute,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "session", ID: "automations", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"status": map[string]any{
						"type": "string", "enum": []any{"active", "paused"},
					},
				},
				"additionalProperties": false,
			},
		}
	case "automation_read":
		return tool.Descriptor{
			Name:        "automation_read",
			Description: "Read an automation template and its recent run ledger.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			RepeatPolicy:       tool.RepeatExecute,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "automation", Field: "automation_id", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"automation_id": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"automation_id"},
				"additionalProperties": false,
			},
		}
	case "automation_update":
		return tool.Descriptor{
			Name:        "automation_update",
			Description: "Update mutable automation fields. Pass expected_version for optimistic concurrency.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			RepeatPolicy:       tool.RepeatExecute,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "automation", Field: "automation_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"automation_id":    map[string]any{"type": "string", "minLength": float64(1)},
					"expected_version": map[string]any{"type": "integer", "minimum": float64(1)},
					"name":             map[string]any{"type": "string", "minLength": float64(1)},
					"rrule":            map[string]any{"type": "string", "minLength": float64(1)},
					"task_kind":        map[string]any{"type": "string"},
					"task_payload":     map[string]any{"type": "object"},
				},
				"required":             []string{"automation_id"},
				"additionalProperties": false,
			},
		}
	case "automation_pause":
		return mutatorDescriptor("automation_pause", "Pause an active automation (clears next_run_at).")
	case "automation_resume":
		return mutatorDescriptor("automation_resume", "Resume a paused automation from its creation RRULE anchor.")
	case "automation_delete":
		return mutatorDescriptor("automation_delete", "Soft-delete an automation.")
	case "automation_run":
		return tool.Descriptor{
			Name: "automation_run",
			Description: "Manually enqueue a durable task for an automation without advancing the schedule slot. " +
				"Returns task_id usable with task_read.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			RepeatPolicy:       tool.RepeatExecute,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "automation", Field: "automation_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"automation_id":    map[string]any{"type": "string", "minLength": float64(1)},
					"expected_version": map[string]any{"type": "integer", "minimum": float64(1)},
				},
				"required":             []string{"automation_id"},
				"additionalProperties": false,
			},
		}
	default:
		return tool.Descriptor{Name: e.name, Availability: tool.AvailabilityUnavailable}
	}
}

func mutatorDescriptor(name, description string) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "automation", Field: "automation_id", Access: tool.AccessWrite,
		}}},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"automation_id":    map[string]any{"type": "string", "minLength": float64(1)},
				"expected_version": map[string]any{"type": "integer", "minimum": float64(1)},
			},
			"required":             []string{"automation_id"},
			"additionalProperties": false,
		},
	}
}

func (e *executor) typedExecutor() (tool.Executor, error) {
	return typed.Define(typed.Spec[input, tool.Result]{
		Descriptor:  e.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Run:         e.run,
		Encode: func(value tool.Result) (tool.Result, error) {
			return value, nil
		},
	})
}

func (e *executor) run(ctx context.Context, value input) (tool.Result, error) {
	switch e.name {
	case "automation_create":
		return e.tools.create(ctx, value)
	case "automation_list":
		return e.tools.list(ctx, value)
	case "automation_read":
		return e.tools.read(ctx, value)
	case "automation_update":
		return e.tools.update(ctx, value)
	case "automation_pause":
		return e.tools.pause(ctx, value)
	case "automation_resume":
		return e.tools.resume(ctx, value)
	case "automation_delete":
		return e.tools.delete(ctx, value)
	case "automation_run":
		return e.tools.run(ctx, value)
	default:
		return tool.Result{}, fmt.Errorf("unknown automation tool %q", e.name)
	}
}

func (t *Tools) create(ctx context.Context, input input) (tool.Result, error) {
	if err := t.repo.EnsureSession(ctx, t.sessionID, t.workspace); err != nil {
		return tool.Result{}, err
	}
	id := strings.TrimSpace(input.AutomationID)
	if id == "" {
		id = fmt.Sprintf("auto_%d", time.Now().UTC().UnixNano())
	}
	created, err := t.repo.Create(ctx, automationstore.CreateRequest{
		ID: id, SessionID: t.sessionID, Name: input.Name, RRULE: input.RRULE,
		TaskKind: input.TaskKind, TaskPayload: input.TaskPayload,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return summarizeAutomation(created)
}

func (t *Tools) list(ctx context.Context, input input) (tool.Result, error) {
	filter := automationstore.Filter{SessionID: t.sessionID}
	if input.Status != "" {
		filter.Status = automationstore.Status(input.Status)
	}
	values, err := t.repo.List(ctx, filter)
	if err != nil {
		return tool.Result{}, err
	}
	rows := make([]map[string]any, 0, len(values))
	for _, value := range values {
		rows = append(rows, automationSummary(value))
	}
	content, err := json.Marshal(rows)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"count": len(rows), "session_id": t.sessionID},
	}, nil
}

func (t *Tools) read(ctx context.Context, input input) (tool.Result, error) {
	value, err := t.repo.Get(ctx, strings.TrimSpace(input.AutomationID))
	if err != nil {
		return tool.Result{}, err
	}
	runs, err := t.repo.ListRuns(ctx, value.ID)
	if err != nil {
		return tool.Result{}, err
	}
	runRows := make([]map[string]any, 0, len(runs))
	for _, run := range runs {
		runRows = append(runRows, map[string]any{
			"run_id": run.ID, "task_id": run.TaskID, "trigger": string(run.Trigger),
			"status":        string(run.Status),
			"scheduled_for": run.ScheduledFor.UTC().Format(time.RFC3339Nano),
		})
	}
	body := automationSummary(value)
	body["task_payload"] = json.RawMessage(value.TaskPayload)
	body["runs"] = runRows
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"automation_id": value.ID, "version": value.Version, "status": string(value.Status),
		},
	}, nil
}

func (t *Tools) update(ctx context.Context, input input) (tool.Result, error) {
	version, err := t.resolveVersion(ctx, input.AutomationID, input.ExpectedVersion)
	if err != nil {
		return tool.Result{}, err
	}
	updated, err := t.repo.Update(ctx, strings.TrimSpace(input.AutomationID), automationstore.Update{
		ExpectedVersion: version, Name: input.Name, RRULE: input.RRULE,
		TaskKind: input.TaskKind, TaskPayload: input.TaskPayload,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return summarizeAutomation(updated)
}

func (t *Tools) pause(ctx context.Context, input input) (tool.Result, error) {
	id, version, err := t.parseMutator(ctx, input)
	if err != nil {
		return tool.Result{}, err
	}
	updated, err := t.repo.Pause(ctx, id, version, time.Time{})
	if err != nil {
		return tool.Result{}, err
	}
	return summarizeAutomation(updated)
}

func (t *Tools) resume(ctx context.Context, input input) (tool.Result, error) {
	id, version, err := t.parseMutator(ctx, input)
	if err != nil {
		return tool.Result{}, err
	}
	updated, err := t.repo.Resume(ctx, id, version, time.Time{})
	if err != nil {
		return tool.Result{}, err
	}
	return summarizeAutomation(updated)
}

func (t *Tools) delete(ctx context.Context, input input) (tool.Result, error) {
	id, version, err := t.parseMutator(ctx, input)
	if err != nil {
		return tool.Result{}, err
	}
	updated, err := t.repo.Delete(ctx, id, version, time.Time{})
	if err != nil {
		return tool.Result{}, err
	}
	return summarizeAutomation(updated)
}

func (t *Tools) run(ctx context.Context, input input) (tool.Result, error) {
	id, version, err := t.parseMutator(ctx, input)
	if err != nil {
		return tool.Result{}, err
	}
	run, err := t.repo.RunNow(ctx, id, version, time.Time{})
	if err != nil {
		return tool.Result{}, err
	}
	content, err := json.Marshal(map[string]any{
		"run_id": run.ID, "task_id": run.TaskID, "automation_id": run.AutomationID,
		"trigger": string(run.Trigger), "status": string(run.Status),
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"run_id": run.ID, "task_id": run.TaskID, "automation_id": run.AutomationID,
		},
	}, nil
}

func (t *Tools) parseMutator(ctx context.Context, input input) (string, uint64, error) {
	version, err := t.resolveVersion(ctx, input.AutomationID, input.ExpectedVersion)
	if err != nil {
		return "", 0, err
	}
	return strings.TrimSpace(input.AutomationID), version, nil
}

func (t *Tools) resolveVersion(ctx context.Context, id string, expected uint64) (uint64, error) {
	current, err := t.repo.Get(ctx, strings.TrimSpace(id))
	if err != nil {
		return 0, err
	}
	if expected == 0 {
		return current.Version, nil
	}
	return expected, nil
}

func summarizeAutomation(value automationstore.Automation) (tool.Result, error) {
	content, err := json.Marshal(automationSummary(value))
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"automation_id": value.ID, "version": value.Version, "status": string(value.Status),
		},
	}, nil
}

func automationSummary(value automationstore.Automation) map[string]any {
	body := map[string]any{
		"automation_id": value.ID, "version": value.Version, "session_id": value.SessionID,
		"name": value.Name, "status": string(value.Status), "rrule": value.RRULE,
		"task_kind":  value.TaskKind,
		"created_at": value.CreatedAt.UTC().Format(time.RFC3339Nano),
		"updated_at": value.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if value.NextRunAt != nil {
		body["next_run_at"] = value.NextRunAt.UTC().Format(time.RFC3339Nano)
	}
	if value.LastRunAt != nil {
		body["last_run_at"] = value.LastRunAt.UTC().Format(time.RFC3339Nano)
	}
	return body
}
