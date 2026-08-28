package hooks

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// TestBoundedBufferWriteAlwaysAcceptsData verifies the bounded buffer's
// design trade-off: it always accepts all data (returning len(data), nil)
// to avoid breaking the pipe to the hook process, but tracks truncation
// separately via the Truncated() method. Returning an error from Write
// would cause Go's runtime to close the pipe, killing the hook process
// before it can complete — which is worse than silent truncation.
// The hook contract documents that output may be truncated according to
// MaxOutputBytes; the hook process should not rely on seeing all output
// accepted.
func TestBoundedBufferWriteAlwaysAcceptsData(t *testing.T) {
	limit := 10
	buf := newBoundedBuffer(limit)

	// Fill the buffer to capacity.
	n, err := buf.Write([]byte("1234567890"))
	if err != nil {
		t.Fatalf("unexpected error on first write: %v", err)
	}
	if n != 10 {
		t.Fatalf("expected 10 bytes written, got %d", n)
	}
	if buf.Truncated() {
		t.Fatal("buffer should not be truncated after filling to capacity")
	}

	// Write more data — this overflows but the buffer still accepts it.
	// This is intentional: returning an error would close the pipe and
	// kill the hook process. Truncation is tracked separately.
	n, err = buf.Write([]byte("overflow"))
	if err != nil {
		t.Errorf("unexpected error on overflow: %v", err)
	}
	if n != len("overflow") {
		t.Errorf("expected %d bytes reported, got %d", len("overflow"), n)
	}
	if !buf.Truncated() {
		t.Error("buffer should be marked as truncated after overflow")
	}

	// Verify the buffer content is only the first 10 bytes.
	content := buf.Bytes()
	if len(content) != limit {
		t.Errorf("expected buffer content length %d, got %d", limit, len(content))
	}

	// Verify total bytes counts all data, even truncated.
	if buf.Total() != 18 {
		t.Errorf("expected total 18 bytes, got %d", buf.Total())
	}
}

type captureSandboxBackend struct {
	workspace string
	command   sandbox.Command
}

func (b *captureSandboxBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "capture",
		Strength: sandbox.StrengthStrong, Available: true,
		Controls: sandbox.Controls{
			ReadIsolation: true, WriteIsolation: true, NetworkIsolation: true,
			ProcessIsolation: true, SyscallIsolation: true, SymlinkSafe: true,
		},
	}
}

func (b *captureSandboxBackend) Policy() sandbox.Policy {
	return sandbox.Policy{ID: "capture-policy", WorkspaceRoot: b.workspace}
}

func (b *captureSandboxBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	b.command = command
	return sandbox.Command{}, errors.New("captured")
}

func TestProductionHookProcessIsReadOnlyAndNetworkDenied(t *testing.T) {
	workspace := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	backend := &captureSandboxBackend{workspace: workspace}
	runner := newExecutor(Options{
		Workspace: workspace, Sandbox: backend, RequireStrongSandbox: true,
	})
	result := runner.run(t.Context(), MessageSubmit, HookConfig{
		ID: "capture", Source: SourceRepository, Trust: TrustWorkspace,
		Scope: ScopeProcess, Mode: ModeEnforce,
		Command: executable, WorkingDirectory: workspace,
		Authority: func(context.Context) error { return nil },
	}, MessageSubmitInput{SessionID: "session", Message: "fixture"})
	if result.errCode != "prepare_process" ||
		!backend.command.WorkspaceReadOnly ||
		!backend.command.DenyNetwork {
		t.Fatalf(
			"hook execution = %+v, sandbox command = %+v",
			result,
			backend.command,
		)
	}
}
