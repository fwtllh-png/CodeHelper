package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// ForegroundTimeoutHint steers agents from blocking shell_run toward background jobs.
const ForegroundTimeoutHint = "timed out; rerun with task_shell_start or background_shell_start, then poll via task_shell_wait or TUI /jobs"

type Tool struct {
	workspace *sandbox.Workspace
	backend   sandbox.Backend
	pty       bool
	readOnly  bool
}

func RegisterWithManagerAndBackend(
	registry *tool.Registry,
	root string,
	manager *process.SessionManager,
	backend sandbox.Backend,
) error {
	if backend == nil {
		return errors.New("shell tools require an injected sandbox backend")
	}
	if manager == nil {
		return errors.New("shell tools require an injected session manager")
	}
	backend, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		return err
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	return registerWithBackend(registry, workspace, manager, backend)
}

func registerWithBackend(
	registry *tool.Registry,
	workspace *sandbox.Workspace,
	manager *process.SessionManager,
	backend sandbox.Backend,
) error {
	registry.SetSandboxBackend(backend)
	for _, executor := range []tool.Executor{
		&Tool{workspace: workspace, backend: backend},
		&Tool{workspace: workspace, backend: backend, readOnly: true},
		&Tool{workspace: workspace, backend: backend, pty: true},
	} {
		if err := registry.Register(executor, nil); err != nil {
			return err
		}
	}
	return registerSessions(registry, workspace, backend, manager)
}

func (t *Tool) Descriptor() tool.Descriptor {
	name := "shell_run"
	description := "Run a shell command in the workspace sandbox. " +
		"cwd must be workspace-relative (or omitted). Host /tmp is blocked — use $TMPDIR. " +
		"Do not cd outside the workspace. Use shell_read instead when the command only inspects data."
	aliases := []tool.Alias{{Name: "bash", Hidden: true}}
	capability := tool.CapabilityProcess
	access := tool.AccessWrite
	accessMode := tool.AccessTree
	if t.readOnly {
		name = "shell_read"
		description = "Run a read-only, network-isolated shell command. " +
			"The OS sandbox permits workspace reads and private temporary files, " +
			"but rejects workspace writes and all network access. " +
			"Use this for grep, sed, sort, inventory, and inspection pipelines. " +
			"Prefer search_text for source or Markdown text. Quote literal backticks " +
			"and other shell metacharacters with single quotes."
		aliases = nil
		capability = tool.CapabilityRead
		access = tool.AccessRead
		accessMode = tool.AccessRead
	} else if t.pty {
		name = "terminal_run"
		description = "Run a command in a pseudo-terminal inside the workspace sandbox"
		aliases = []tool.Alias{{Name: "run_terminal", Hidden: true}}
	}
	return tool.Descriptor{
		Name: name, Description: description, Visibility: tool.VisibleModel, Aliases: aliases,
		Capability: capability, AccessMode: accessMode,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
			{Kind: "repo", ID: ".", Access: access, Tree: true},
			{Kind: "process", ID: "workspace", Access: access, Tree: true},
		}},
		ParallelPolicy:     tool.ParallelSerial,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command":     map[string]any{"type": "string", "minLength": float64(1)},
				"cwd":         map[string]any{"type": "string"},
				"timeout_ms":  map[string]any{"type": "integer"},
				"description": map[string]any{"type": "string"},
			},
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Command     string `json:"command"`
		CWD         string `json:"cwd"`
		TimeoutMS   int64  `json:"timeout_ms"`
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	directory, err := t.workspace.ResolveDirectory(input.CWD)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("invalid cwd %q: %v", input.CWD, err),
			IsError: true,
			Metadata: map[string]any{
				"exit_code": -1, "cwd_error": err.Error(),
			},
		}, nil
	}
	directoryFile, err := t.workspace.OpenDirectory(input.CWD)
	if err != nil {
		return tool.Result{
			Content: fmt.Sprintf("open cwd %q: %v", input.CWD, err),
			IsError: true,
			Metadata: map[string]any{
				"exit_code": -1, "cwd_error": err.Error(),
			},
		}, nil
	}
	defer directoryFile.Close()
	if input.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(input.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	sandboxBackend, requireStrong := processSandbox(ctx, t.backend)
	started := time.Now()
	command := input.Command
	if requireStrong {
		command = wrapSandboxTempCommand(command)
	}
	result, err := process.Run(ctx, process.Options{
		Command: command, Dir: directory, PTY: t.pty,
		DirFile: directoryFile,
		Sandbox: sandboxBackend, RequireStrongSandbox: requireStrong,
		WorkspaceReadOnly: t.readOnly,
		DenyNetwork:       t.readOnly,
		OnOutput:          streamOutput(ctx),
	})
	durationMS := time.Since(started).Milliseconds()
	timedOut := errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(ctx.Err(), context.DeadlineExceeded)
	if err != nil && !timedOut {
		if requireStrong && errors.Is(err, guard.ErrSandboxDenied) {
			return tool.Result{}, guard.MarkSandboxDenial(err, "shell")
		}
		return tool.Result{}, err
	}
	content := formatShellOutput(result.Stdout, result.Stderr)
	status := "completed"
	if timedOut {
		status = "timed_out"
	} else if result.ExitCode != 0 {
		status = "failed"
	}
	exitCode := result.ExitCode
	if content == "" && result.ExitCode != 0 {
		content = fmt.Sprintf("command exited with code %d", result.ExitCode)
	}
	if hint := sandboxPathHint(result.Stdout + "\n" + result.Stderr); hint != "" {
		if content != "" {
			content += "\n"
		}
		content += "[hint] " + hint
	}
	tail := content
	if len(tail) > 2<<10 {
		tail = tail[len(tail)-(2<<10):]
	}
	identity := tool.InvocationIdentityFrom(ctx)
	callID := identity.CallID
	metadata := map[string]any{
		"stdout": result.Stdout, "stderr": result.Stderr, "exit_code": result.ExitCode, "pty": t.pty,
		"command_execution": map[string]any{
			"command": input.Command, "status": status, "exit_code": exitCode,
			"duration_ms": durationMS, "output_tail": tail, "call_id": callID,
		},
	}
	if input.Description != "" {
		metadata["description"] = input.Description
	}
	if timedOut {
		if content != "" {
			content += "\n"
		}
		content += ForegroundTimeoutHint
		metadata["timed_out"] = true
		metadata["timeout_hint"] = ForegroundTimeoutHint
		return tool.Result{Content: content, IsError: true, Metadata: metadata}, nil
	}
	return tool.Result{
		Content:  content,
		IsError:  result.ExitCode != 0,
		Metadata: metadata,
	}, nil
}

// streamOutput bridges a running command's output to whoever is watching this
// tool call. It returns nil when nobody is, so an unobserved command pays nothing
// for the copy.
func streamOutput(ctx context.Context) func(process.Chunk) {
	observe := tool.OutputObserverFrom(ctx)
	if observe == nil {
		return nil
	}
	return func(chunk process.Chunk) {
		observe(tool.OutputChunk{
			Stream: string(chunk.Stream), Data: string(chunk.Data), Cursor: chunk.Cursor,
		})
	}
}

func formatShellOutput(stdout, stderr string) string {
	content := stdout
	if stderr == "" {
		return content
	}
	if content != "" {
		content += "\n"
	}
	return content + "[stderr]\n" + stderr
}

// wrapSandboxTempCommand remaps `cd /tmp` onto $TMPDIR. Seatbelt makes host
// /tmp look like "Not a directory"; agents still emit that path constantly.
func wrapSandboxTempCommand(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return command
	}
	const preamble = `cd(){ if [ "$#" -eq 1 ] && { [ "$1" = /tmp ] || [ "$1" = /tmp/ ]; }; then` +
		` command cd "${TMPDIR:-.}"; else command cd "$@"; fi; }; `
	return preamble + command
}

func sandboxPathHint(text string) string {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "go: command not found") ||
		strings.Contains(lower, "go: not found") {
		return "Go toolchain not on sandbox PATH; restart codehelper from a shell where `which go` works (GOROOT/bin is injected automatically in current builds)"
	}
	if strings.Contains(lower, "could not create module cache") ||
		strings.Contains(lower, "mkdir /var: file exists") {
		return "Go module cache path hit macOS /var symlink under sandbox; upgrade/restart codehelper so private temp uses /private/var realpath"
	}
	if !strings.Contains(lower, "not a directory") &&
		!strings.Contains(lower, "operation not permitted") &&
		!strings.Contains(lower, "permission denied") {
		return ""
	}
	if strings.Contains(lower, "/tmp") || strings.Contains(lower, "/var/folders") ||
		strings.Contains(lower, "outside") {
		return "sandbox blocks host /tmp and paths outside the workspace; use $TMPDIR or stay inside the workspace"
	}
	if strings.Contains(lower, "not a directory") {
		return "path denied by sandbox (often looks like 'Not a directory'); use workspace-relative paths and $TMPDIR"
	}
	return ""
}
