package authority

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type ManagedFileInput struct {
	ID                  string
	Tool                string
	WorkspaceRoot       string
	WorkspaceID         string
	WorkspaceGeneration uint64
	Subject             Subject
	Paths               []string
	MutationDigest      string
	Risk                policy.RiskLevel
}

func BuildManagedFileOperation(
	input ManagedFileInput,
) (ExecutionOperation, error) {
	if err := input.Subject.Validate(); err != nil {
		return ExecutionOperation{}, err
	}
	if strings.TrimSpace(input.ID) == "" ||
		strings.TrimSpace(input.Tool) == "" ||
		strings.TrimSpace(input.WorkspaceRoot) == "" ||
		!validDigest(input.WorkspaceID) ||
		input.WorkspaceGeneration == 0 ||
		!validDigest(input.MutationDigest) ||
		len(input.Paths) == 0 {
		return ExecutionOperation{}, errors.New("managed file operation is incomplete")
	}
	resources := make([]Resource, 0, len(input.Paths))
	for _, path := range input.Paths {
		path = filepath.ToSlash(filepath.Clean(path))
		if path == "" || path == "." || filepath.IsAbs(path) ||
			path == ".." || strings.HasPrefix(path, "../") {
			return ExecutionOperation{}, errors.New("managed file path is invalid")
		}
		resource := Resource{
			Namespace: NamespaceWorkspace,
			RootID:    input.WorkspaceID, RootGeneration: input.WorkspaceGeneration,
			RelativePath: path, Kind: "file", Access: tool.AccessWrite,
		}
		if err := resource.Validate(); err != nil {
			return ExecutionOperation{}, err
		}
		resources = append(resources, resource)
	}
	resources = normalizeResources(resources)
	digests := make([]string, 0, len(resources))
	for _, resource := range resources {
		digest, err := resourceDigest(resource)
		if err != nil {
			return ExecutionOperation{}, err
		}
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	operation := ExecutionOperation{
		SchemaVersion: OperationSchemaVersion,
		ID:            input.ID, Tool: input.Tool,
		WorkspaceID:         input.WorkspaceID,
		WorkspaceGeneration: input.WorkspaceGeneration,
		Subject:             input.Subject,
		Effect: EffectContract{
			Kind:                   policy.EffectWorkspaceEdit,
			Reversibility:          ReversibilityReversible,
			Risk:                   input.Risk,
			WorkspaceTransaction:   WorkspaceTransactionBeforeImage,
			RequireReadBeforeWrite: true,
		},
		Required: RequiredControls{
			FilesystemRead: true, FilesystemWrite: true, SymlinkSafety: true,
		},
		Resources: resources,
		File: &FileIntent{
			ResourceDigests: digests, MutationDigest: input.MutationDigest,
		},
	}
	digest, err := operationDigest(operation)
	if err != nil {
		return ExecutionOperation{}, err
	}
	operation.Digest = digest
	return operation, operation.Validate()
}

func BuildManagedFileProfile(
	operation ExecutionOperation,
	revision uint64,
	workspaceRoot string,
) (EffectivePermissionProfile, error) {
	if err := operation.Validate(); err != nil {
		return EffectivePermissionProfile{}, err
	}
	if operation.File == nil || revision == 0 {
		return EffectivePermissionProfile{}, errors.New("managed file profile is incomplete")
	}
	profile := EffectivePermissionProfile{
		SchemaVersion: SchemaVersion, Revision: revision,
		Tool: operation.Tool, Capability: tool.CapabilityWrite,
		Access: tool.AccessTree,
		Filesystem: FilesystemAuthority{
			WorkspaceRoot: workspaceRoot,
		},
		Network: NetworkAuthority{Mode: "denied"},
		Process: ProcessAuthority{
			Enforcement: "none", Backend: "file_broker", Strength: "none",
		},
		Controls: EffectiveControls{
			FilesystemRead: true, FilesystemWrite: true, SymlinkSafety: true,
		},
		Provenance: []AuthoritySource{{
			Kind: "file_broker", Value: operation.Subject.ID,
			Digest:   operation.Subject.Digest,
			Revision: operation.Subject.Generation,
		}},
	}
	for _, resource := range operation.Resources {
		profile.Filesystem.WritePaths = append(
			profile.Filesystem.WritePaths,
			filepath.Join(workspaceRoot, filepath.FromSlash(resource.RelativePath)),
		)
	}
	normalize(&profile)
	digest, err := profileDigest(profile)
	if err != nil {
		return EffectivePermissionProfile{}, err
	}
	profile.Digest = digest
	return profile, profile.Validate()
}
