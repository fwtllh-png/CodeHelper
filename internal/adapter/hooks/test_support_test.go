package hooks

import (
	"context"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/security/controlmatrix"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type hookTestBackend struct {
	policy sandbox.Policy
}

func (b hookTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough", Available: true,
		Effective: controlmatrix.Matrix{
			FilesystemRead:  controlmatrix.FilesystemReadDeclaredRoots,
			FilesystemWrite: controlmatrix.FilesystemWriteExactPaths,
			Network:         controlmatrix.NetworkDenied,
			ProcessTree:     controlmatrix.ProcessTreeGroupKill,
			CrossProcess:    controlmatrix.CrossProcessRestricted,
			Syscall:         controlmatrix.SyscallDenyDangerous,
			IPC:             controlmatrix.IPCUnixOnly,
			PathIdentity:    controlmatrix.PathIdentityDescriptorRelative,
			ArtifactOrigin:  controlmatrix.ArtifactOriginUnverifiedPath,
			DurableRecovery: controlmatrix.DurableRecoveryMemoryOnly,
		},
	}
}

func (b hookTestBackend) Policy() sandbox.Policy { return b.policy }

func (b hookTestBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	command.PreparedPolicyID = b.policy.ID
	command.PreparedAuthorityDigest = command.AuthorityDigest
	command.PreparedControls = sandbox.CommandControls(
		b.Capability(),
		b.policy,
		command,
	)
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedReadPaths = append(
		[]string(nil),
		command.AdditionalReadPaths...,
	)
	command.PreparedWritePaths = append(
		[]string(nil),
		command.WorkspaceWritePaths...,
	)
	command.PreparedHiddenPaths = append(
		[]string(nil),
		command.WorkspaceHiddenPaths...,
	)
	command.PreparedNetworkDenied = command.DenyNetwork
	command.PreparedLoopbackAllowed = command.AllowLoopback && !command.DenyNetwork
	return command, nil
}

func hookTestOptions(t *testing.T, workspace string) Options {
	t.Helper()
	runtime, err := NewRuntime("", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := sandbox.BuildPolicy(sandbox.Options{
		WorkspaceRoot:     workspace,
		PrivateTemp:       t.TempDir(),
		SkipPATHReadRoots: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Workspace: workspace,
		Sandbox:   hookTestBackend{policy: policy},
		Runtime:   runtime,
	}
}
