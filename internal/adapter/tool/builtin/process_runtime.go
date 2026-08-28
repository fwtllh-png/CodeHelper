package builtin

import (
	"path/filepath"

	qualitytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/quality"
	"github.com/fwtllh-png/CodeHelper/internal/security/artifactbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/processbroker"
)

type ProcessRuntime struct {
	quality qualitytool.RuntimeDependencies
}

func NewProcessRuntime(
	workspace, stateRoot, workspaceID string,
	workspaceGeneration uint64,
	leaseAuthority *authority.LeaseAuthority,
) (*ProcessRuntime, error) {
	if stateRoot == "" {
		return nil, nil
	}
	artifacts, err := artifactbroker.New(artifactbroker.Options{
		WorkspaceRoot:       workspace,
		SandboxHomeRoot:     filepath.Join(stateRoot, "sandbox-home"),
		StagingRoot:         filepath.Join(stateRoot, "artifacts"),
		WorkspaceID:         workspaceID,
		WorkspaceGeneration: workspaceGeneration,
	})
	if err != nil {
		return nil, err
	}
	processes, err := processbroker.New(leaseAuthority)
	if err != nil {
		return nil, err
	}
	return &ProcessRuntime{quality: qualitytool.RuntimeDependencies{
		ArtifactBroker: artifacts,
		ProcessBroker:  processes,
	}}, nil
}
