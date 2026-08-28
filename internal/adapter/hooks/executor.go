package hooks

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/processbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Options struct {
	Workspace            string
	Sandbox              sandbox.Backend
	RequireStrongSandbox bool
	DefaultTimeout       time.Duration
	MaxOutputBytes       int
	Audit                AuditSink
	Now                  func() time.Time
	Runtime              *Runtime
}

type Runtime struct {
	WorkspaceID         string
	WorkspaceGeneration uint64
	LeaseAuthority      *authority.LeaseAuthority
	ProcessBroker       *processbroker.Broker
}

func NewRuntime(
	workspaceID string,
	workspaceGeneration uint64,
	leases *authority.LeaseAuthority,
) (*Runtime, error) {
	if workspaceGeneration == 0 {
		workspaceGeneration = 1
	}
	if leases == nil {
		leases = authority.NewLeaseAuthority(authority.LeaseAuthorityOptions{})
	}
	broker, err := processbroker.New(leases)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		WorkspaceID: workspaceID, WorkspaceGeneration: workspaceGeneration,
		LeaseAuthority: leases, ProcessBroker: broker,
	}, nil
}

type executor struct {
	options     Options
	hiddenPaths []string
}

type execution struct {
	source          Source
	trust           Trust
	scope           Scope
	mode            Mode
	stdout          []byte
	stderr          []byte
	stdoutBytes     int64
	stderrBytes     int64
	stdoutTruncated bool
	stderrTruncated bool
	exitCode        int
	duration        time.Duration
	timedOut        bool
	canceled        bool
	errCode         string
	err             error
}

type hookEnvelope struct {
	Version int    `json:"version"`
	Event   Event  `json:"event"`
	Source  Source `json:"source"`
	Trust   Trust  `json:"trust"`
	Scope   Scope  `json:"scope"`
	Mode    Mode   `json:"mode"`
	Input   any    `json:"input"`
}

func newExecutor(options Options) *executor {
	if options.DefaultTimeout <= 0 {
		options.DefaultTimeout = defaultTimeout
	}
	if options.MaxOutputBytes <= 0 {
		options.MaxOutputBytes = defaultMaxOutputBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	sum := sha256.Sum256([]byte(filepath.Clean(options.Workspace)))
	workspaceID := hex.EncodeToString(sum[:])
	if options.Runtime == nil {
		options.Runtime, _ = NewRuntime(
			workspaceID, 1,
			authority.NewLeaseAuthority(
				authority.LeaseAuthorityOptions{Now: options.Now},
			),
		)
	} else if options.Runtime.WorkspaceID == "" {
		runtime := *options.Runtime
		runtime.WorkspaceID = workspaceID
		options.Runtime = &runtime
	}
	return &executor{
		options:     options,
		hiddenPaths: existingHookControlPaths(options.Workspace),
	}
}

func existingHookControlPaths(workspace string) []string {
	var paths []string
	for _, name := range []string{
		".agents", ".codehelper", ".codehelper-worktree", ".codex", ".git",
	} {
		path := filepath.Join(workspace, name)
		if _, err := os.Lstat(path); err == nil {
			paths = append(paths, path)
		}
	}
	return paths
}

func (e *executor) run(ctx context.Context, event Event, hook HookConfig, input any) execution {
	started := e.options.Now()
	metadata := execution{
		source: hook.Source, trust: hook.Trust, scope: hook.Scope, mode: hook.Mode,
	}
	if hook.Authority != nil {
		if err := hook.Authority(ctx); err != nil {
			metadata.exitCode = -1
			metadata.errCode = "authority_revoked"
			metadata.err = err
			metadata.duration = e.options.Now().Sub(started)
			return metadata
		}
	}
	timeout := hook.Timeout
	if timeout <= 0 {
		timeout = e.options.DefaultTimeout
	}
	limit := hook.MaxOutputBytes
	if limit <= 0 {
		limit = e.options.MaxOutputBytes
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(hookEnvelope{
		Version: ConfigVersion, Event: event,
		Source: hook.Source, Trust: hook.Trust, Scope: hook.Scope, Mode: hook.Mode,
		Input: input,
	})
	if err != nil {
		result := execution{exitCode: -1, errCode: "encode_input", err: err}
		result.duration = e.options.Now().Sub(started)
		return result
	}

	var directory *os.File
	if e.options.RequireStrongSandbox {
		directory, err = process.OpenPinnedDirectory(e.options.Sandbox, hook.WorkingDirectory)
		if err != nil {
			result := execution{exitCode: -1, errCode: "pin_working_directory", err: err}
			result.duration = e.options.Now().Sub(started)
			return result
		}
		defer directory.Close()
	}
	processOptions := process.Options{
		Path: hook.Command, Args: hook.Args, Dir: hook.WorkingDirectory,
		DirFile: directory, Env: hook.Env, Sandbox: e.options.Sandbox,
		Stdin: bytes.NewReader(payload), OutputLimitBytes: limit,
		RequireStrongSandbox: e.options.RequireStrongSandbox,
		WorkspaceReadOnly:    e.options.RequireStrongSandbox,
		WorkspaceHiddenPaths: append([]string(nil), e.hiddenPaths...),
		DenyNetwork:          e.options.RequireStrongSandbox,
	}
	operation, lease, validation, err := e.authorizeProcess(
		runCtx, event, hook, processOptions,
	)
	if err != nil {
		result := execution{exitCode: -1, errCode: "authorize_process", err: err}
		result.duration = e.options.Now().Sub(started)
		return result
	}
	identity := hookIdentity(input, hook.ID, event)
	brokerResult, waitErr := e.options.Runtime.ProcessBroker.RunCommand(
		runCtx,
		processbroker.CommandRequest{
			Lease: lease, Validation: validation, Options: processOptions,
			Identity: identity,
		},
	)
	if snapshot, snapshotErr := e.options.Runtime.LeaseAuthority.Snapshot(lease); snapshotErr == nil &&
		snapshot.State == authority.LeaseIssued {
		waitErr = errors.Join(waitErr, e.options.Runtime.LeaseAuthority.Revoke(lease))
	}
	waitErr = errors.Join(waitErr, e.options.Runtime.LeaseAuthority.Release(lease))
	_ = operation
	stdoutReceipt := brokerResult.Process.OutputReceipt.Stdout
	stderrReceipt := brokerResult.Process.OutputReceipt.Stderr
	result := execution{
		source: hook.Source, trust: hook.Trust, scope: hook.Scope, mode: hook.Mode,
		stdout:          []byte(brokerResult.Process.Stdout),
		stderr:          []byte(brokerResult.Process.Stderr),
		stdoutBytes:     int64(stdoutReceipt.TotalBytes),
		stderrBytes:     int64(stderrReceipt.TotalBytes),
		stdoutTruncated: stdoutReceipt.Truncated(),
		stderrTruncated: stderrReceipt.Truncated(),
		exitCode:        brokerResult.Process.ExitCode,
		duration:        e.options.Now().Sub(started),
	}
	if runCtx.Err() != nil {
		result.err = runCtx.Err()
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			result.timedOut = true
			result.errCode = "timeout"
		} else {
			result.canceled = true
			result.errCode = "canceled"
		}
	} else if waitErr != nil {
		var exitError *exec.ExitError
		if !errors.As(waitErr, &exitError) {
			result.err = waitErr
			result.errCode = "wait_process"
			if brokerResult.Settlement.Reason == "runner_failure" {
				result.exitCode = -1
				result.errCode = "prepare_process"
			}
		}
	}
	return result
}

func (e *executor) authorizeProcess(
	ctx context.Context,
	event Event,
	hook HookConfig,
	options process.Options,
) (
	authority.ExecutionOperation,
	authority.ExecutionLease,
	authority.LeaseValidation,
	error,
) {
	if e.options.Runtime == nil ||
		e.options.Runtime.LeaseAuthority == nil ||
		e.options.Runtime.ProcessBroker == nil ||
		e.options.Runtime.WorkspaceGeneration == 0 ||
		strings.TrimSpace(e.options.Runtime.WorkspaceID) == "" {
		return authority.ExecutionOperation{}, authority.ExecutionLease{},
			authority.LeaseValidation{}, errors.New("hook Process Broker is unavailable")
	}
	subjectKind, trust := authority.SubjectRepositoryHook, authority.TrustWorkspace
	if hook.Source == SourcePlugin {
		subjectKind, trust = authority.SubjectPlugin, authority.TrustExternal
	} else if hook.Source == SourceBuiltin {
		subjectKind, trust = authority.SubjectBuiltin, authority.TrustBuiltin
	}
	subject, err := authority.NewManagedProcessSubject(
		subjectKind, hook.ID, trust, 1,
		struct {
			Event            Event
			ID               string
			Source           Source
			Trust            Trust
			Scope            Scope
			Mode             Mode
			Command          string
			Args             []string
			Environment      []string
			WorkingDirectory string
		}{
			Event: event, ID: hook.ID, Source: hook.Source,
			Trust: hook.Trust, Scope: hook.Scope, Mode: hook.Mode,
			Command: hook.Command, Args: append([]string(nil), hook.Args...),
			Environment:      append([]string(nil), hook.Env...),
			WorkingDirectory: hook.WorkingDirectory,
		},
	)
	if err != nil {
		return authority.ExecutionOperation{}, authority.ExecutionLease{},
			authority.LeaseValidation{}, err
	}
	enforcement := "none"
	capability := sandbox.Capability{Backend: "host"}
	required := authority.RequiredControls{}
	if e.options.RequireStrongSandbox {
		if e.options.Sandbox == nil {
			return authority.ExecutionOperation{}, authority.ExecutionLease{},
				authority.LeaseValidation{}, errors.New("hook strong Sandbox is unavailable")
		}
		enforcement = "strong"
		capability = e.options.Sandbox.Capability()
		required = authority.RequiredControls{
			FilesystemRead: true, Network: true,
			ProcessTree: true, SymlinkSafety: true,
		}
	}
	operation, err := authority.BuildManagedProcessOperation(
		authority.ManagedProcessInput{
			ID: hook.ID + ":" + string(event), Tool: "hook:" + hook.ID,
			WorkspaceID:         e.options.Runtime.WorkspaceID,
			WorkspaceGeneration: e.options.Runtime.WorkspaceGeneration,
			Subject:             subject, Executable: options.Path,
			Args: options.Args, WorkingDirectory: options.Dir,
			Environment: options.Env,
			Effect:      authority.ManagedProcessEffect(policy.RiskHigh),
			Required:    required,
		},
	)
	if err != nil {
		return authority.ExecutionOperation{}, authority.ExecutionLease{},
			authority.LeaseValidation{}, err
	}
	policyID := ""
	var proxyPort uint16
	if sandboxPolicy, ok := sandbox.BackendPolicy(e.options.Sandbox); ok {
		policyID = sandboxPolicy.ID
		proxyPort = sandboxPolicy.ManagedProxyPort
	}
	profile, err := authority.BuildManagedProcessProfile(
		authority.ManagedProfileInput{
			Operation: operation, Revision: 1,
			WorkspaceRoot:      e.options.Workspace,
			WorkspaceBaseWrite: !e.options.RequireStrongSandbox,
			AllowNetwork:       !e.options.RequireStrongSandbox,
			ManagedProxyPort:   proxyPort,
			Enforcement:        enforcement, Backend: capability.Backend,
			Strength: string(capability.Strength),
			Controls: authority.EffectiveControls{
				FilesystemRead:  capability.Controls.ReadIsolation,
				FilesystemWrite: capability.Controls.WriteIsolation,
				Network:         capability.Controls.NetworkIsolation,
				ProcessTree:     capability.Controls.ProcessIsolation,
				Syscall:         capability.Controls.SyscallIsolation,
				SymlinkSafety:   capability.Controls.SymlinkSafe,
			},
		},
	)
	if err != nil {
		return authority.ExecutionOperation{}, authority.ExecutionLease{},
			authority.LeaseValidation{}, err
	}
	lease, err := e.options.Runtime.LeaseAuthority.Issue(authority.LeaseIssueRequest{
		Operation: operation, Profile: profile, PolicyRevision: 1,
		SandboxPolicyID: policyID, Attempt: 1,
		ExpiresAt: deadlineOr(ctx, e.options.Now().Add(e.options.DefaultTimeout)),
	})
	if err != nil {
		return operation, authority.ExecutionLease{}, authority.LeaseValidation{}, err
	}
	validation := authority.LeaseValidation{
		Operation: operation, PolicyRevision: 1,
		WorkspaceID:         operation.WorkspaceID,
		WorkspaceGeneration: operation.WorkspaceGeneration,
		SubjectDigest:       operation.Subject.Digest,
		SubjectGeneration:   operation.Subject.Generation,
		SandboxPolicyID:     policyID, Attempt: 1,
	}
	return operation, lease, validation, nil
}

func deadlineOr(ctx context.Context, fallback time.Time) time.Time {
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(fallback) {
		return deadline
	}
	return fallback
}

func hookIdentity(input any, hookID string, event Event) processbroker.Identity {
	identity := processbroker.Identity{
		SessionID: "hook", ThreadID: hookID, TurnID: string(event),
	}
	encoded, _ := json.Marshal(input)
	var values map[string]json.RawMessage
	if json.Unmarshal(encoded, &values) == nil {
		_ = json.Unmarshal(values["sessionId"], &identity.SessionID)
		_ = json.Unmarshal(values["turnId"], &identity.TurnID)
	}
	if identity.SessionID == "" {
		identity.SessionID = "hook"
	}
	if identity.TurnID == "" {
		identity.TurnID = string(event)
	}
	return identity
}

func (e *executor) audit(
	ctx context.Context,
	event Event,
	hookID string,
	inputKeys, outputKeys []string,
	result execution,
	outcome string,
	action Action,
) {
	record := AuditRecord{
		Time: e.options.Now(), Event: event, HookID: hookID,
		Source: result.source, Trust: result.trust,
		Scope: result.scope, Mode: result.mode,
		Outcome: outcome, Action: action, ErrorCode: result.errCode,
		ExitCode: result.exitCode, Duration: result.duration,
		TimedOut: result.timedOut, Canceled: result.canceled,
		StdoutBytes: result.stdoutBytes, StderrBytes: result.stderrBytes,
		StdoutTruncated: result.stdoutTruncated, StderrTruncated: result.stderrTruncated,
		InputKeys:  append([]string(nil), inputKeys...),
		OutputKeys: append([]string(nil), outputKeys...),
	}
	auditCtx := context.WithoutCancel(ctx)
	if e.options.Audit != nil {
		e.options.Audit.Record(auditCtx, record)
	}
	emitContextAudit(auditCtx, record)
}

type boundedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	total     int64
	truncated bool
}

func newBoundedBuffer(limit int) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.total += int64(len(data))
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		_, _ = b.buffer.Write(data[:min(remaining, len(data))])
	}
	if len(data) > remaining {
		b.truncated = true
	}
	return len(data), nil
}

func (b *boundedBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buffer.Bytes())
}

func (b *boundedBuffer) Total() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.total
}

func (b *boundedBuffer) Truncated() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.truncated
}

var _ io.Writer = (*boundedBuffer)(nil)
