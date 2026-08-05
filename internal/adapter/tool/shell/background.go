package shell

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type backgroundTool struct {
	workspace *sandbox.Workspace
	backend   sandbox.Backend
	manager   *process.SessionManager
	kind      string
}

func registerBackground(
	registry *tool.Registry,
	workspace *sandbox.Workspace,
	backend sandbox.Backend,
	manager *process.SessionManager,
) error {
	for _, kind := range []string{
		"background_shell_start", "background_shell_wait",
		"background_shell_interact", "background_shell_cancel",
		"task_shell_start", "task_shell_wait",
	} {
		if err := registry.Register(&backgroundTool{
			workspace: workspace, backend: backend, manager: manager, kind: kind,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (t *backgroundTool) operation() string {
	switch t.kind {
	case "task_shell_start":
		return "background_shell_start"
	case "task_shell_wait":
		return "background_shell_wait"
	default:
		return t.kind
	}
}

func (t *backgroundTool) Descriptor() tool.Descriptor {
	properties := map[string]any{
		"session_id": map[string]any{"type": "string", "minLength": float64(1)},
	}
	required := []string{"session_id"}
	description := ""
	aliases := []tool.Alias{}
	access := tool.AccessWrite
	resolver := tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
		Kind: "session", Field: "session_id", Access: tool.AccessWrite,
	}}}
	switch t.operation() {
	case "background_shell_start":
		description = "Start a cancellable background shell with incremental output"
		if t.kind == "task_shell_start" {
			description = "Start a long-running background shell for durable task work; " +
				"poll with task_shell_wait"
		}
		properties = map[string]any{
			"command":    map[string]any{"type": "string", "minLength": float64(1)},
			"cwd":        map[string]any{"type": "string"},
			"session_id": map[string]any{"type": "string", "minLength": float64(1)},
			"rows":       map[string]any{"type": "integer"},
			"cols":       map[string]any{"type": "integer"},
			"timeout_ms": map[string]any{"type": "integer"},
		}
		required = []string{"command"}
		if t.kind == "background_shell_start" {
			aliases = []tool.Alias{{Name: "shell_start", Hidden: true}}
		}
		access = tool.AccessTree
		resolver = tool.ResourceResolver{Templates: []tool.ResourceTemplate{
			{Kind: "repo", ID: ".", Access: tool.AccessWrite, Tree: true},
			{Kind: "process", ID: "workspace", Access: tool.AccessWrite, Tree: true},
		}}
	case "background_shell_wait":
		description = "Wait for incremental background shell output or process exit; " +
			"a cursor the live buffer has passed is served from the job log, " +
			"marked archived with pending_bytes for what is still unread"
		if t.kind == "task_shell_wait" {
			description = "Wait for incremental output or exit from a task_shell_start session"
		}
		properties["cursor"] = map[string]any{"type": "integer"}
		properties["timeout_ms"] = map[string]any{"type": "integer"}
		if t.kind == "background_shell_wait" {
			aliases = []tool.Alias{{Name: "shell_wait", Hidden: true}}
		}
		access = tool.AccessRead
		resolver.Templates[0].Access = tool.AccessRead
	case "background_shell_interact":
		description = "Send stdin and optionally resize a background pseudo-terminal"
		properties["stdin"] = map[string]any{"type": "string"}
		properties["rows"] = map[string]any{"type": "integer"}
		properties["cols"] = map[string]any{"type": "integer"}
		aliases = []tool.Alias{{Name: "shell_interact", Hidden: true}}
	case "background_shell_cancel":
		description = "Cancel a background shell and reclaim its process group"
		aliases = []tool.Alias{{Name: "shell_cancel", Hidden: true}}
	}
	availability := tool.AvailabilityAvailable
	unavailableReason := ""
	if runtime.GOOS == "windows" {
		availability = tool.AvailabilityUnavailable
		unavailableReason = "background pseudo-terminal capability is unavailable on this platform"
	}
	return tool.Descriptor{
		Name: t.kind, Description: description, Visibility: tool.VisibleModel,
		Capability: tool.CapabilityProcess, AccessMode: access,
		ResourceResolver: resolver, Aliases: aliases,
		ParallelPolicy: tool.ParallelSerial, SandboxRequirement: tool.SandboxStrong,
		Availability: availability, UnavailableReason: unavailableReason,
		InputSchema: map[string]any{
			"type": "object", "properties": properties, "required": required,
			"additionalProperties": false,
		},
	}
}

func (t *backgroundTool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		SessionID string `json:"session_id"`
		Command   string `json:"command"`
		CWD       string `json:"cwd"`
		Stdin     string `json:"stdin"`
		Cursor    uint64 `json:"cursor"`
		Rows      uint16 `json:"rows"`
		Cols      uint16 `json:"cols"`
		TimeoutMS int64  `json:"timeout_ms"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	switch t.operation() {
	case "background_shell_start":
		directory, err := t.workspace.ResolveDirectory(input.CWD)
		if err != nil {
			return tool.Result{}, err
		}
		directoryFile, err := t.workspace.OpenDirectory(input.CWD)
		if err != nil {
			return tool.Result{}, err
		}
		defer directoryFile.Close()
		jobContext := context.WithoutCancel(ctx)
		if input.TimeoutMS > 0 {
			var cancel context.CancelFunc
			jobContext, cancel = context.WithTimeout(jobContext, time.Duration(input.TimeoutMS)*time.Millisecond)
			go func() {
				<-jobContext.Done()
				cancel()
			}()
		}
		sandboxBackend, requireStrong := processSandbox(jobContext, t.backend)
		identity := tool.InvocationIdentityFrom(ctx)
		id, err := t.manager.Create(jobContext, process.SessionOptions{
			Command: input.Command, Dir: directory, DirFile: directoryFile,
			SessionID: input.SessionID,
			ThreadID:  identity.ThreadID, TurnID: identity.TurnID, CallID: identity.CallID,
			Rows: input.Rows, Cols: input.Cols, Sandbox: sandboxBackend,
			RequireStrongSandbox: requireStrong,
			DetachFromCaller:     true,
		})
		if err != nil {
			return tool.Result{}, err
		}
		return tool.Result{
			Content: id,
			Metadata: map[string]any{
				"session_id": id, "cursor": uint64(0), "running": true,
				"pty": true, "pty_available": true, "stdin": true, "resize": true,
			},
		}, nil
	case "background_shell_wait":
		wait, err := t.manager.Wait(
			ctx, input.SessionID, input.Cursor, time.Duration(input.TimeoutMS)*time.Millisecond,
		)
		if err != nil {
			return tool.Result{}, err
		}
		content := wait.Data
		metadata := map[string]any{
			"session_id": input.SessionID, "cursor": wait.Cursor,
			"running": wait.Running, "exit_code": wait.ExitCode, "timed_out": wait.TimedOut,
		}
		if wait.Archived {
			// The caller's cursor had fallen behind the live buffer; say where the
			// bytes came from and whether there are more waiting, so a poller that is
			// far behind knows to call again instead of assuming it caught up.
			metadata["archived"] = true
			metadata["pending_bytes"] = wait.Pending
		}
		if wait.TimedOut {
			if content != "" && !strings.HasSuffix(content, "\n") {
				content += "\n"
			}
			content += ForegroundTimeoutHint
			metadata["timeout_hint"] = ForegroundTimeoutHint
		}
		return tool.Result{Content: content, Metadata: metadata}, nil
	case "background_shell_interact":
		if input.Rows != 0 || input.Cols != 0 {
			if input.Rows == 0 || input.Cols == 0 {
				return tool.Result{}, errors.New("rows and cols must be supplied together")
			}
			if err := t.manager.Resize(input.SessionID, input.Rows, input.Cols); err != nil {
				return tool.Result{}, err
			}
		}
		if input.Stdin != "" {
			if err := t.manager.Write(input.SessionID, []byte(input.Stdin)); err != nil {
				return tool.Result{}, err
			}
		}
		return tool.Result{
			Content:  "interacted",
			Metadata: map[string]any{"session_id": input.SessionID, "stdin_bytes": len(input.Stdin)},
		}, nil
	case "background_shell_cancel":
		if err := t.manager.Close(input.SessionID); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{
			Content:  "canceled",
			Metadata: map[string]any{"session_id": input.SessionID, "running": false, "canceled": true},
		}, nil
	default:
		return tool.Result{}, errors.New("unknown background shell operation")
	}
}
