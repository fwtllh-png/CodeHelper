package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
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
}

type executor struct {
	options Options
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
	return &executor{options: options}
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
	command, err := process.NewCommand(runCtx, process.Options{
		Path: hook.Command, Args: hook.Args, Dir: hook.WorkingDirectory,
		DirFile: directory, Env: hook.Env, Sandbox: e.options.Sandbox,
		RequireStrongSandbox: e.options.RequireStrongSandbox,
	})
	if err != nil {
		result := execution{exitCode: -1, errCode: "prepare_process", err: err}
		result.duration = e.options.Now().Sub(started)
		return result
	}
	stdout := newBoundedBuffer(limit)
	stderr := newBoundedBuffer(limit)
	command.Stdin = bytes.NewReader(payload)
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Start(); err != nil {
		result := execution{exitCode: -1, errCode: "start_process", err: err}
		result.duration = e.options.Now().Sub(started)
		return result
	}
	killTree, cleanupTree, err := attachProcessTree(command)
	if err != nil {
		_ = command.Cancel()
		_ = command.Wait()
		result := execution{exitCode: -1, errCode: "attach_process_tree", err: err}
		result.duration = e.options.Now().Sub(started)
		return result
	}
	defer cleanupTree()

	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var waitErr error
	select {
	case waitErr = <-done:
	case <-runCtx.Done():
		_ = killTree()
		waitErr = <-done
	}
	result := execution{
		source: hook.Source, trust: hook.Trust, scope: hook.Scope, mode: hook.Mode,
		stdout: stdout.Bytes(), stderr: stderr.Bytes(),
		stdoutBytes: stdout.Total(), stderrBytes: stderr.Total(),
		stdoutTruncated: stdout.Truncated(), stderrTruncated: stderr.Truncated(),
		exitCode: process.ExitCode(waitErr),
		duration: e.options.Now().Sub(started),
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
		}
	}
	return result
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
