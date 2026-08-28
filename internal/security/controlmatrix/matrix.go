// Package controlmatrix defines comparable execution controls shared by tool
// bindings, sandbox probes, authority profiles, and brokers.
package controlmatrix

import (
	"fmt"
	"strings"
)

type FilesystemRead string
type FilesystemWrite string
type Network string
type ProcessTree string
type CrossProcess string
type Syscall string
type IPC string
type PathIdentity string
type ArtifactOrigin string
type DurableRecovery string

const (
	FilesystemReadUnrestricted  FilesystemRead = "unrestricted"
	FilesystemReadDeclaredRoots FilesystemRead = "declared_roots"
	FilesystemReadExactPaths    FilesystemRead = "exact_paths"

	FilesystemWriteUnrestricted FilesystemWrite = "unrestricted"
	FilesystemWriteWorkspace    FilesystemWrite = "workspace_tree"
	FilesystemWriteExactPaths   FilesystemWrite = "exact_paths"
	FilesystemWriteDenied       FilesystemWrite = "denied"

	NetworkDirect        Network = "direct"
	NetworkProxyTargets  Network = "proxy_targets"
	NetworkLoopbackExact Network = "loopback_exact"
	NetworkDenied        Network = "denied"

	ProcessTreeUnmanaged    ProcessTree = "unmanaged"
	ProcessTreeGroupKill    ProcessTree = "group_kill"
	ProcessTreeJobObject    ProcessTree = "job_object"
	ProcessTreePIDNamespace ProcessTree = "pid_namespace"

	CrossProcessUnrestricted CrossProcess = "unrestricted"
	CrossProcessRestricted   CrossProcess = "restricted"
	CrossProcessIsolated     CrossProcess = "isolated"

	SyscallUnrestricted  Syscall = "unrestricted"
	SyscallDenyDangerous Syscall = "deny_dangerous"
	SyscallAllowlist     Syscall = "allowlist"

	IPCUnrestricted     IPC = "unrestricted"
	IPCUnixOnly         IPC = "unix_only"
	IPCPrivateNamespace IPC = "private_namespace"

	PathIdentityLexical            PathIdentity = "lexical"
	PathIdentityCanonical          PathIdentity = "canonical"
	PathIdentityDescriptorRelative PathIdentity = "descriptor_relative"

	ArtifactOriginUnverifiedPath   ArtifactOrigin = "unverified_path"
	ArtifactOriginVerifiedManifest ArtifactOrigin = "verified_manifest"
	ArtifactOriginBrokerSnapshot   ArtifactOrigin = "broker_snapshot"

	DurableRecoveryMemoryOnly           DurableRecovery = "memory_only"
	DurableRecoveryExternalJournal      DurableRecovery = "external_journal"
	DurableRecoveryResumableTransaction DurableRecovery = "resumable_transaction"
)

type Matrix struct {
	FilesystemRead  FilesystemRead  `json:"filesystem_read,omitempty"`
	FilesystemWrite FilesystemWrite `json:"filesystem_write,omitempty"`
	Network         Network         `json:"network,omitempty"`
	ProcessTree     ProcessTree     `json:"process_tree,omitempty"`
	CrossProcess    CrossProcess    `json:"cross_process,omitempty"`
	Syscall         Syscall         `json:"syscall,omitempty"`
	IPC             IPC             `json:"ipc,omitempty"`
	PathIdentity    PathIdentity    `json:"path_identity,omitempty"`
	ArtifactOrigin  ArtifactOrigin  `json:"artifact_origin,omitempty"`
	DurableRecovery DurableRecovery `json:"durable_recovery,omitempty"`
}

type Requirements Matrix

func (r Requirements) Validate() error { return Matrix(r).validate(true) }
func (m Matrix) Validate() error       { return m.validate(false) }
func (r Requirements) IsZero() bool    { return r == (Requirements{}) }

func (m Matrix) StringMap() map[string]string {
	return map[string]string{
		"filesystem_read":  string(m.FilesystemRead),
		"filesystem_write": string(m.FilesystemWrite),
		"network":          string(m.Network),
		"process_tree":     string(m.ProcessTree),
		"cross_process":    string(m.CrossProcess),
		"syscall":          string(m.Syscall),
		"ipc":              string(m.IPC),
		"path_identity":    string(m.PathIdentity),
		"artifact_origin":  string(m.ArtifactOrigin),
		"durable_recovery": string(m.DurableRecovery),
	}
}

func (m Matrix) Identity() string {
	return strings.Join([]string{
		string(m.FilesystemRead),
		string(m.FilesystemWrite),
		string(m.Network),
		string(m.ProcessTree),
		string(m.CrossProcess),
		string(m.Syscall),
		string(m.IPC),
		string(m.PathIdentity),
		string(m.ArtifactOrigin),
		string(m.DurableRecovery),
	}, "/")
}

func (m Matrix) validate(allowEmpty bool) error {
	if m == (Matrix{}) && allowEmpty {
		return nil
	}
	values := []struct {
		name  string
		value string
		valid bool
	}{
		{"filesystem_read", string(m.FilesystemRead), validFilesystemRead(m.FilesystemRead)},
		{"filesystem_write", string(m.FilesystemWrite), validFilesystemWrite(m.FilesystemWrite)},
		{"network", string(m.Network), validNetwork(m.Network)},
		{"process_tree", string(m.ProcessTree), validProcessTree(m.ProcessTree)},
		{"cross_process", string(m.CrossProcess), validCrossProcess(m.CrossProcess)},
		{"syscall", string(m.Syscall), validSyscall(m.Syscall)},
		{"ipc", string(m.IPC), validIPC(m.IPC)},
		{"path_identity", string(m.PathIdentity), validPathIdentity(m.PathIdentity)},
		{"artifact_origin", string(m.ArtifactOrigin), validArtifactOrigin(m.ArtifactOrigin)},
		{"durable_recovery", string(m.DurableRecovery), validDurableRecovery(m.DurableRecovery)},
	}
	for _, value := range values {
		if value.value == "" && allowEmpty {
			continue
		}
		if value.value != "" && value.valid {
			continue
		}
		return fmt.Errorf("%s control %q is invalid", value.name, value.value)
	}
	return nil
}

func (r Requirements) SatisfiedBy(actual Matrix) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if err := actual.Validate(); err != nil {
		return err
	}
	checks := []struct {
		required, actual string
		satisfied        bool
		name             string
	}{
		{string(r.FilesystemRead), string(actual.FilesystemRead),
			satisfiesOrdered(string(r.FilesystemRead), string(actual.FilesystemRead),
				[]string{string(FilesystemReadUnrestricted), string(FilesystemReadDeclaredRoots), string(FilesystemReadExactPaths)}),
			"filesystem read"},
		{string(r.FilesystemWrite), string(actual.FilesystemWrite),
			satisfiesOrdered(string(r.FilesystemWrite), string(actual.FilesystemWrite),
				[]string{string(FilesystemWriteUnrestricted), string(FilesystemWriteWorkspace), string(FilesystemWriteExactPaths), string(FilesystemWriteDenied)}),
			"filesystem write"},
		{string(r.Network), string(actual.Network),
			satisfiesNetwork(r.Network, actual.Network),
			"network"},
		{string(r.ProcessTree), string(actual.ProcessTree),
			satisfiesProcessTree(r.ProcessTree, actual.ProcessTree), "process tree"},
		{string(r.CrossProcess), string(actual.CrossProcess),
			satisfiesOrdered(string(r.CrossProcess), string(actual.CrossProcess),
				[]string{string(CrossProcessUnrestricted), string(CrossProcessRestricted), string(CrossProcessIsolated)}),
			"cross-process"},
		{string(r.Syscall), string(actual.Syscall),
			satisfiesOrdered(string(r.Syscall), string(actual.Syscall),
				[]string{string(SyscallUnrestricted), string(SyscallDenyDangerous), string(SyscallAllowlist)}),
			"syscall"},
		{string(r.IPC), string(actual.IPC),
			satisfiesOrdered(string(r.IPC), string(actual.IPC),
				[]string{string(IPCUnrestricted), string(IPCUnixOnly), string(IPCPrivateNamespace)}),
			"IPC"},
		{string(r.PathIdentity), string(actual.PathIdentity),
			satisfiesOrdered(string(r.PathIdentity), string(actual.PathIdentity),
				[]string{string(PathIdentityLexical), string(PathIdentityCanonical), string(PathIdentityDescriptorRelative)}),
			"path identity"},
		{string(r.ArtifactOrigin), string(actual.ArtifactOrigin),
			satisfiesOrdered(string(r.ArtifactOrigin), string(actual.ArtifactOrigin),
				[]string{string(ArtifactOriginUnverifiedPath), string(ArtifactOriginVerifiedManifest), string(ArtifactOriginBrokerSnapshot)}),
			"artifact origin"},
		{string(r.DurableRecovery), string(actual.DurableRecovery),
			satisfiesOrdered(string(r.DurableRecovery), string(actual.DurableRecovery),
				[]string{string(DurableRecoveryMemoryOnly), string(DurableRecoveryExternalJournal), string(DurableRecoveryResumableTransaction)}),
			"durable recovery"},
	}
	for _, check := range checks {
		if check.required != "" && !check.satisfied {
			return fmt.Errorf("%s control requires %q, backend provides %q",
				check.name, check.required, check.actual)
		}
	}
	return nil
}

func satisfiesNetwork(required, actual Network) bool {
	switch required {
	case "":
		return true
	case NetworkDirect:
		return validNetwork(actual)
	case NetworkProxyTargets:
		return actual == NetworkProxyTargets || actual == NetworkDenied
	case NetworkLoopbackExact:
		return actual == NetworkLoopbackExact || actual == NetworkDenied
	default:
		return required == actual
	}
}

func CanEnforceNetwork(capability, desired Network) bool {
	switch capability {
	case NetworkDenied:
		return desired == NetworkDenied ||
			desired == NetworkLoopbackExact
	case NetworkLoopbackExact:
		return desired == NetworkLoopbackExact || desired == NetworkDenied
	case NetworkProxyTargets:
		return desired == NetworkProxyTargets || desired == NetworkDenied
	case NetworkDirect:
		return desired == NetworkDirect
	default:
		return false
	}
}

func CanEnforceFilesystemWrite(
	capability, desired FilesystemWrite,
) bool {
	switch capability {
	case FilesystemWriteDenied:
		return desired == FilesystemWriteDenied
	case FilesystemWriteExactPaths:
		return desired == FilesystemWriteExactPaths ||
			desired == FilesystemWriteDenied
	case FilesystemWriteWorkspace:
		return desired == FilesystemWriteWorkspace
	case FilesystemWriteUnrestricted:
		return desired == FilesystemWriteUnrestricted
	default:
		return false
	}
}

func satisfiesOrdered(required, actual string, order []string) bool {
	if required == "" {
		return true
	}
	requiredIndex, actualIndex := -1, -1
	for index, value := range order {
		if value == required {
			requiredIndex = index
		}
		if value == actual {
			actualIndex = index
		}
	}
	return requiredIndex >= 0 && actualIndex >= requiredIndex
}

func satisfiesProcessTree(required, actual ProcessTree) bool {
	switch required {
	case "":
		return true
	case ProcessTreeUnmanaged:
		return validProcessTree(actual)
	case ProcessTreeGroupKill:
		return actual == ProcessTreeGroupKill ||
			actual == ProcessTreeJobObject ||
			actual == ProcessTreePIDNamespace
	default:
		return required == actual
	}
}

func validFilesystemRead(value FilesystemRead) bool {
	return value == FilesystemReadUnrestricted || value == FilesystemReadDeclaredRoots ||
		value == FilesystemReadExactPaths
}
func validFilesystemWrite(value FilesystemWrite) bool {
	return value == FilesystemWriteUnrestricted || value == FilesystemWriteWorkspace ||
		value == FilesystemWriteExactPaths || value == FilesystemWriteDenied
}
func validNetwork(value Network) bool {
	return value == NetworkDirect || value == NetworkProxyTargets ||
		value == NetworkLoopbackExact || value == NetworkDenied
}
func validProcessTree(value ProcessTree) bool {
	return value == ProcessTreeUnmanaged || value == ProcessTreeGroupKill ||
		value == ProcessTreeJobObject || value == ProcessTreePIDNamespace
}
func validCrossProcess(value CrossProcess) bool {
	return value == CrossProcessUnrestricted || value == CrossProcessRestricted ||
		value == CrossProcessIsolated
}
func validSyscall(value Syscall) bool {
	return value == SyscallUnrestricted || value == SyscallDenyDangerous ||
		value == SyscallAllowlist
}
func validIPC(value IPC) bool {
	return value == IPCUnrestricted || value == IPCUnixOnly || value == IPCPrivateNamespace
}
func validPathIdentity(value PathIdentity) bool {
	return value == PathIdentityLexical || value == PathIdentityCanonical ||
		value == PathIdentityDescriptorRelative
}
func validArtifactOrigin(value ArtifactOrigin) bool {
	return value == ArtifactOriginUnverifiedPath ||
		value == ArtifactOriginVerifiedManifest ||
		value == ArtifactOriginBrokerSnapshot
}
func validDurableRecovery(value DurableRecovery) bool {
	return value == DurableRecoveryMemoryOnly ||
		value == DurableRecoveryExternalJournal ||
		value == DurableRecoveryResumableTransaction
}
