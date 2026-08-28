package builtin

import (
	"path/filepath"
	"time"

	qualitytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/quality"
	"github.com/fwtllh-png/CodeHelper/internal/platform/workspacequery"
	"github.com/fwtllh-png/CodeHelper/internal/security/artifactbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/processbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
	"github.com/fwtllh-png/CodeHelper/internal/security/workspacebroker"
)

type ProcessRuntime struct {
	quality   qualitytool.RuntimeDependencies
	workspace *workspacebroker.Runtime
}

func NewWorkspaceBroker(
	workspace string,
	leaseAuthority *authority.LeaseAuthority,
	leaseTTL time.Duration,
) (*workspacebroker.Runtime, error) {
	return workspacebroker.New(workspace, leaseAuthority, leaseTTL)
}

func NewWorkspaceQuery(
	workspace string,
	backend sandbox.Backend,
	leaseAuthority *authority.LeaseAuthority,
	leaseTTL time.Duration,
) (*workspacequery.Service, error) {
	brokers, err := NewWorkspaceBroker(
		workspace, leaseAuthority, leaseTTL,
	)
	if err != nil {
		return nil, err
	}
	return workspacequery.New(workspace, backend, brokers.VCS)
}

func NewProcessRuntime(
	workspace, stateRoot, workspaceID string,
	workspaceGeneration uint64,
	leaseAuthority *authority.LeaseAuthority,
	leaseTTL time.Duration,
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
	workspaceRuntime, err := workspacebroker.New(
		workspace, leaseAuthority, leaseTTL,
	)
	if err != nil {
		return nil, err
	}
	return &ProcessRuntime{
		workspace: workspaceRuntime,
		quality: qualitytool.RuntimeDependencies{
			ArtifactBroker: artifacts,
			ProcessBroker:  processes,
		}}, nil
}
