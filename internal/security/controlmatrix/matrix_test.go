package controlmatrix

import "testing"

func TestRequirementsSatisfiedByComparableControls(t *testing.T) {
	required := Requirements{
		FilesystemRead:  FilesystemReadDeclaredRoots,
		FilesystemWrite: FilesystemWriteExactPaths,
		Network:         NetworkProxyTargets,
		ProcessTree:     ProcessTreeGroupKill,
		PathIdentity:    PathIdentityDescriptorRelative,
	}
	actual := Matrix{
		FilesystemRead:  FilesystemReadExactPaths,
		FilesystemWrite: FilesystemWriteDenied,
		Network:         NetworkDenied,
		ProcessTree:     ProcessTreePIDNamespace,
		CrossProcess:    CrossProcessIsolated,
		Syscall:         SyscallDenyDangerous,
		IPC:             IPCPrivateNamespace,
		PathIdentity:    PathIdentityDescriptorRelative,
		ArtifactOrigin:  ArtifactOriginBrokerSnapshot,
		DurableRecovery: DurableRecoveryResumableTransaction,
	}
	if err := required.SatisfiedBy(actual); err != nil {
		t.Fatal(err)
	}
}

func TestRequirementsRejectWeakerAndIncomparableControls(t *testing.T) {
	tests := []struct {
		name     string
		required Requirements
		actual   Matrix
	}{
		{
			name:     "weaker network",
			required: Requirements{Network: NetworkProxyTargets},
			actual:   completeMatrix(Matrix{Network: NetworkDirect}),
		},
		{
			name:     "incomparable process backend",
			required: Requirements{ProcessTree: ProcessTreePIDNamespace},
			actual:   completeMatrix(Matrix{ProcessTree: ProcessTreeJobObject}),
		},
		{
			name:     "canonical is not descriptor relative",
			required: Requirements{PathIdentity: PathIdentityDescriptorRelative},
			actual:   completeMatrix(Matrix{PathIdentity: PathIdentityCanonical}),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.required.SatisfiedBy(test.actual); err == nil {
				t.Fatal("weaker controls were accepted")
			}
		})
	}
}

func TestCapabilitiesCanOnlyPromiseSupportedCommandControls(t *testing.T) {
	if !CanEnforceNetwork(NetworkProxyTargets, NetworkDenied) ||
		!CanEnforceNetwork(NetworkDenied, NetworkLoopbackExact) ||
		CanEnforceNetwork(NetworkDirect, NetworkDenied) {
		t.Fatal("network enforcement relation is incorrect")
	}
	if !CanEnforceFilesystemWrite(
		FilesystemWriteExactPaths,
		FilesystemWriteDenied,
	) || CanEnforceFilesystemWrite(
		FilesystemWriteWorkspace,
		FilesystemWriteExactPaths,
	) {
		t.Fatal("filesystem write enforcement relation is incorrect")
	}
}

func completeMatrix(override Matrix) Matrix {
	value := Matrix{
		FilesystemRead:  FilesystemReadDeclaredRoots,
		FilesystemWrite: FilesystemWriteExactPaths,
		Network:         NetworkDenied, ProcessTree: ProcessTreeGroupKill,
		CrossProcess: CrossProcessRestricted, Syscall: SyscallDenyDangerous,
		IPC: IPCUnixOnly, PathIdentity: PathIdentityDescriptorRelative,
		ArtifactOrigin:  ArtifactOriginVerifiedManifest,
		DurableRecovery: DurableRecoveryExternalJournal,
	}
	if override.FilesystemRead != "" {
		value.FilesystemRead = override.FilesystemRead
	}
	if override.FilesystemWrite != "" {
		value.FilesystemWrite = override.FilesystemWrite
	}
	if override.Network != "" {
		value.Network = override.Network
	}
	if override.ProcessTree != "" {
		value.ProcessTree = override.ProcessTree
	}
	if override.PathIdentity != "" {
		value.PathIdentity = override.PathIdentity
	}
	return value
}
