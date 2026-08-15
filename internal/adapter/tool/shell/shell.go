package shell

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
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

type foregroundInput struct {
	Command     string   `json:"command"`
	CWD         string   `json:"cwd"`
	TimeoutMS   int64    `json:"timeout_ms"`
	Description string   `json:"description"`
	WritePaths  []string `json:"write_paths"`
}

type foregroundExecutor struct {
	tool    *Tool
	runtime interface {
		tool.OutcomeExecutor
		tool.DispositionProvider
	}
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
	for _, implementation := range []*Tool{
		&Tool{workspace: workspace, backend: backend},
		&Tool{workspace: workspace, backend: backend, readOnly: true},
		&Tool{workspace: workspace, backend: backend, pty: true},
	} {
		executor, err := newForegroundExecutor(implementation)
		if err != nil {
			return err
		}
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
		"Do not cd outside the workspace. Workspace files are read-only: use file_edit, " +
		"file_write, or file_apply for persistent changes. Use shell_read when the command " +
		"only inspects data."
	aliases := []tool.Alias{{Name: "bash", Hidden: true}}
	capability := tool.CapabilityProcess
	access := tool.AccessRead
	accessMode := tool.AccessRead
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
	} else if t.pty {
		name = "terminal_run"
		description = "Run a command in a pseudo-terminal inside the workspace sandbox. " +
			"Workspace files are read-only; persistent changes must use guarded file tools."
		aliases = []tool.Alias{{Name: "run_terminal", Hidden: true}}
	} else {
		description = "Run a shell command in the workspace sandbox. " +
			"cwd must be workspace-relative (or omitted). Host /tmp is blocked — use $TMPDIR. " +
			"Workspace files are read-only unless write_paths declares existing exact files " +
			"or write_globs expands to a bounded set of existing files. " +
			"Declared writes pass through approval, journal, and receipt tracking. " +
			"Use file_edit, file_write, or file_apply for ordinary persistent changes."
	}
	description += " Commands run under POSIX sh, not Bash. Do not use Bash-only " +
		"syntax such as process substitution (<(...))."
	properties := map[string]any{
		"command":     map[string]any{"type": "string", "minLength": float64(1)},
		"cwd":         map[string]any{"type": "string"},
		"timeout_ms":  map[string]any{"type": "integer"},
		"description": map[string]any{"type": "string"},
	}
	resolver := tool.ResourceResolver{Templates: []tool.ResourceTemplate{
		{Kind: "repo", ID: ".", Access: access, Tree: true},
		{Kind: "process", ID: "workspace", Access: access, Tree: true},
	}}
	if !t.readOnly && !t.pty {
		properties["write_paths"] = map[string]any{
			"type": "array", "items": map[string]any{
				"type": "string", "minLength": float64(1),
			},
			"maxItems":    float64(sandbox.MaxExactWorkspaceWritePaths),
			"uniqueItems": true,
		}
		properties["write_globs"] = map[string]any{
			"type": "array", "items": map[string]any{
				"type": "string", "minLength": float64(1),
			},
			"maxItems": float64(32), "uniqueItems": true,
		}
		resolver.PathsField = "write_paths"
	}
	return tool.Descriptor{
		Name: name, Description: description, Visibility: tool.VisibleModel, Aliases: aliases,
		Capability: capability, AccessMode: accessMode,
		ResourceResolver:   resolver,
		ParallelPolicy:     tool.ParallelSerial,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		RepeatPolicy: tool.RepeatExecute,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           properties,
			"required":             []string{"command"},
			"additionalProperties": false,
		},
	}
}

func (t *Tool) ExpandArguments(
	_ context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	if t.readOnly || t.pty {
		return raw, nil
	}
	return t.expandWriteGlobs(raw)
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	input, err := typed.DecodeStrict[foregroundInput](raw)
	if err != nil {
		return tool.Result{}, err
	}
	return t.execute(ctx, input)
}

func (t *Tool) execute(ctx context.Context, input foregroundInput) (tool.Result, error) {
	if token := unsupportedPOSIXShellSyntax(input.Command); token != "" {
		content, _ := json.Marshal(map[string]any{
			"status":          "rejected",
			"error_category":  "unsupported_shell_syntax",
			"shell_dialect":   "posix_sh",
			"syntax":          token,
			"required_action": "rewrite_without_process_substitution",
		})
		return tool.Result{
			Content: string(content),
			IsError: true,
			Metadata: map[string]any{
				"exit_code":       -1,
				"error_category":  "unsupported_shell_syntax",
				"shell_dialect":   "posix_sh",
				"syntax":          token,
				"required_action": "rewrite_without_process_substitution",
			},
		}, nil
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
	writePaths, err := t.resolveWritePaths(input.WritePaths)
	if err != nil {
		return tool.Result{}, fmt.Errorf("resolve shell write paths: %w", err)
	}
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
		WorkspaceReadOnly:   true,
		WorkspaceWritePaths: writePaths,
		DenyNetwork:         t.readOnly,
		OnOutput:            streamOutput(ctx),
		OutputLimitBytes:    process.ModelOutputLimitBytes,
	})
	durationMS := time.Since(started).Milliseconds()
	timedOut := errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(ctx.Err(), context.DeadlineExceeded)
	if err != nil && !timedOut {
		if requireStrong && errors.Is(err, guard.ErrSandboxDenied) {
			return tool.Result{}, guard.MarkSandboxDenial(err, "shell")
		}
		if recovery := t.recoverWorkspaceChange(err); recovery != nil {
			return tool.Result{}, recovery
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
		"output_receipt": result.OutputReceipt,
		"workspace_write_scope": func() string {
			if len(input.WritePaths) == 0 {
				return "none"
			}
			return "exact"
		}(),
		"command_execution": map[string]any{
			"command": input.Command, "status": status, "exit_code": exitCode,
			"duration_ms": durationMS, "output_tail": tail, "call_id": callID,
			"stdout_bytes": result.OutputReceipt.Stdout.TotalBytes,
			"stderr_bytes": result.OutputReceipt.Stderr.TotalBytes,
			"omitted_bytes": result.OutputReceipt.Stdout.OmittedBytes +
				result.OutputReceipt.Stderr.OmittedBytes,
		},
	}
	if input.Description != "" {
		metadata["description"] = input.Description
	}
	if len(input.WritePaths) != 0 {
		metadata["write_paths"] = append([]string(nil), input.WritePaths...)
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

func newForegroundExecutor(implementation *Tool) (tool.Executor, error) {
	executor, err := typed.Define(typed.Spec[foregroundInput, tool.Result]{
		Descriptor:  implementation.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Run:         implementation.execute,
		Encode: func(result tool.Result) (tool.Result, error) {
			return result, nil
		},
	})
	if err != nil {
		return nil, err
	}
	runtime, ok := executor.(interface {
		tool.OutcomeExecutor
		tool.DispositionProvider
	})
	if !ok {
		return nil, errors.New("typed shell executor has no outcome runtime")
	}
	return &foregroundExecutor{tool: implementation, runtime: runtime}, nil
}

func (e *foregroundExecutor) Descriptor() tool.Descriptor {
	return e.runtime.Descriptor()
}

func (e *foregroundExecutor) ExecutionDisposition() tool.ExecutionDisposition {
	return e.runtime.ExecutionDisposition()
}

func (e *foregroundExecutor) Execute(
	ctx context.Context,
	raw json.RawMessage,
) (tool.Result, error) {
	return e.runtime.Execute(ctx, raw)
}

func (e *foregroundExecutor) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (tool.Result, tool.Outcome, error) {
	return e.runtime.ExecuteOutcome(ctx, raw)
}

func (e *foregroundExecutor) ExpandArguments(
	ctx context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	return e.tool.ExpandArguments(ctx, raw)
}

func (t *Tool) recoverWorkspaceChange(err error) error {
	if !errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var pathError *os.PathError
	if !errors.As(err, &pathError) {
		return nil
	}
	missingPath := canonicalMissingPath(pathError.Path)
	relative, relErr := filepath.Rel(t.workspace.Root(), missingPath)
	if relErr != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	requiredAction := "shell_run"
	if t.readOnly {
		requiredAction = "shell_read"
	} else if t.pty {
		requiredAction = "terminal_run"
	}
	return tool.Precondition(tool.WithRecoveryHint(
		fmt.Errorf("workspace changed during sandbox validation: %w", err),
		tool.RecoveryHint{
			ErrorCategory:  "workspace_changed",
			RequiredAction: requiredAction,
			Path:           filepath.ToSlash(relative),
			RetryOriginal:  true,
		},
	))
}

func canonicalMissingPath(path string) string {
	current := filepath.Clean(path)
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return filepath.Clean(path)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return filepath.Clean(path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func (t *Tool) resolveWritePaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if t.readOnly || t.pty {
		return nil, errors.New("this shell tool does not accept workspace write paths")
	}
	if len(paths) > sandbox.MaxExactWorkspaceWritePaths {
		return nil, fmt.Errorf(
			"shell write paths exceed the %d-file limit",
			sandbox.MaxExactWorkspaceWritePaths,
		)
	}
	resolved := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		canonical, err := t.workspace.Resolve(path, sandbox.MustExist)
		if err != nil {
			return nil, err
		}
		info, err := os.Lstat(canonical)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("write path %q is not a regular file", path)
		}
		if _, exists := seen[canonical]; exists {
			return nil, fmt.Errorf("duplicate shell write path %q", path)
		}
		seen[canonical] = struct{}{}
		resolved = append(resolved, canonical)
	}
	sort.Strings(resolved)
	return resolved, nil
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

func unsupportedPOSIXShellSyntax(command string) string {
	var singleQuoted, doubleQuoted, escaped, comment bool
	for index := 0; index < len(command); index++ {
		current := command[index]
		if comment {
			if current == '\n' {
				comment = false
			}
			continue
		}
		if escaped {
			escaped = false
			continue
		}
		if current == '\\' && !singleQuoted {
			escaped = true
			continue
		}
		if current == '\'' && !doubleQuoted {
			singleQuoted = !singleQuoted
			continue
		}
		if current == '"' && !singleQuoted {
			doubleQuoted = !doubleQuoted
			continue
		}
		if singleQuoted {
			continue
		}
		if current == '#' &&
			(index == 0 || strings.ContainsRune(" \t\r\n;|&()", rune(command[index-1]))) {
			comment = true
			continue
		}
		if (current == '<' || current == '>') &&
			index+1 < len(command) && command[index+1] == '(' {
			return command[index : index+2]
		}
	}
	return ""
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
