package shell

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type sessionTool struct {
	workspace *sandbox.Workspace
	backend   sandbox.Backend
	manager   *process.SessionManager
	kind      string
}

func registerSessions(
	registry *tool.Registry,
	workspace *sandbox.Workspace,
	backend sandbox.Backend,
	manager *process.SessionManager,
) error {
	if manager == nil {
		return errors.New("terminal session manager is required")
	}
	for _, kind := range []string{
		"terminal_create", "terminal_write", "terminal_read",
		"terminal_resize", "terminal_signal", "terminal_close",
	} {
		executor := &sessionTool{
			workspace: workspace, backend: backend, manager: manager, kind: kind,
		}
		if err := registry.Register(executor, nil); err != nil {
			return err
		}
	}
	return registerBackground(registry, workspace, backend, manager)
}

func (t *sessionTool) Descriptor() tool.Descriptor {
	properties := map[string]any{
		"session_id": map[string]any{"type": "string", "minLength": float64(1)},
	}
	required := []string{"session_id"}
	description := ""
	switch t.kind {
	case "terminal_create":
		description = "Create a persistent pseudo-terminal session"
		properties = map[string]any{
			"command": map[string]any{"type": "string"},
			"cwd":     map[string]any{"type": "string"},
			"rows":    map[string]any{"type": "integer"},
			"cols":    map[string]any{"type": "integer"},
		}
		required = []string{}
	case "terminal_write":
		description = "Write input to a persistent terminal session"
		properties["data"] = map[string]any{"type": "string"}
		required = append(required, "data")
	case "terminal_read":
		description = "Read terminal output incrementally from a cursor"
		properties["cursor"] = map[string]any{"type": "integer"}
	case "terminal_resize":
		description = "Resize a persistent terminal session"
		properties["rows"] = map[string]any{"type": "integer"}
		properties["cols"] = map[string]any{"type": "integer"}
		required = append(required, "rows", "cols")
	case "terminal_signal":
		description = "Send a signal to a persistent terminal session"
		properties["signal"] = map[string]any{
			"type": "string", "enum": []any{"INT", "TERM", "KILL", "HUP", "WINCH"},
		}
		required = append(required, "signal")
	case "terminal_close":
		description = "Close a persistent terminal session and its process group"
	}
	access := tool.AccessWrite
	resolver := tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
		Kind: "session", Field: "session_id", Access: tool.AccessWrite,
	}}}
	if t.kind == "terminal_create" {
		resolver = tool.ResourceResolver{Templates: []tool.ResourceTemplate{
			{Kind: "repo", ID: ".", Access: tool.AccessWrite, Tree: true},
			{Kind: "process", ID: "workspace", Access: tool.AccessWrite, Tree: true},
		}}
		access = tool.AccessTree
	} else if t.kind == "terminal_read" {
		resolver.Templates[0].Access = tool.AccessRead
		access = tool.AccessRead
	}
	return tool.Descriptor{
		Name: t.kind, Description: description, Visibility: tool.VisibleModel,
		Capability: tool.CapabilityProcess, AccessMode: access,
		ResourceResolver: resolver, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": properties, "required": required, "additionalProperties": false,
		},
	}
}

func (t *sessionTool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		SessionID string `json:"session_id"`
		Command   string `json:"command"`
		CWD       string `json:"cwd"`
		Data      string `json:"data"`
		Cursor    uint64 `json:"cursor"`
		Rows      uint16 `json:"rows"`
		Cols      uint16 `json:"cols"`
		Signal    string `json:"signal"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	switch t.kind {
	case "terminal_create":
		directory, err := t.workspace.ResolveDirectory(input.CWD)
		if err != nil {
			return tool.Result{}, err
		}
		directoryFile, err := t.workspace.OpenDirectory(input.CWD)
		if err != nil {
			return tool.Result{}, err
		}
		defer directoryFile.Close()
		sandboxBackend, requireStrong := processSandbox(ctx, t.backend)
		identity := tool.InvocationIdentityFrom(ctx)
		id, err := t.manager.Create(context.WithoutCancel(ctx), process.SessionOptions{
			Command: input.Command, Dir: directory, Rows: input.Rows, Cols: input.Cols,
			DirFile:  directoryFile,
			ThreadID: identity.ThreadID, TurnID: identity.TurnID, CallID: identity.CallID,
			Sandbox: sandboxBackend, RequireStrongSandbox: requireStrong,
			DetachFromCaller: true,
		})
		if err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: id, Metadata: map[string]any{"session_id": id, "cursor": uint64(0)}}, nil
	case "terminal_write":
		if err := t.manager.Write(input.SessionID, []byte(input.Data)); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: "written"}, nil
	case "terminal_read":
		read, err := t.manager.Read(input.SessionID, input.Cursor)
		if err != nil {
			return tool.Result{}, err
		}
		return tool.Result{
			Content: read.Data,
			Metadata: map[string]any{
				"cursor": read.Cursor, "running": read.Running, "exit_code": read.ExitCode,
			},
		}, nil
	case "terminal_resize":
		if err := t.manager.Resize(input.SessionID, input.Rows, input.Cols); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: "resized"}, nil
	case "terminal_signal":
		signal, err := parseSignal(input.Signal)
		if err != nil {
			return tool.Result{}, err
		}
		if err := t.manager.Signal(input.SessionID, signal); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: "signaled"}, nil
	case "terminal_close":
		if err := t.manager.Close(input.SessionID); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: "closed"}, nil
	default:
		return tool.Result{}, errors.New("unknown terminal session operation")
	}
}
