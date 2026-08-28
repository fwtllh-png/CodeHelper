package guard

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type LeaseAuthority = authority.LeaseAuthority

func NewLeaseAuthority() *LeaseAuthority {
	return authority.NewLeaseAuthority(authority.LeaseAuthorityOptions{})
}

func (g *Guard) compileAuthority(
	prepared preparedExecution,
	mode SandboxMode,
	revision uint64,
) (authority.EffectivePermissionProfile, error) {
	if prepared.runtime == nil {
		return authority.EffectivePermissionProfile{}, errors.New(
			"authorized policy snapshot is required",
		)
	}
	backend := g.registry.InjectedSandbox(prepared.invocation.Tool)
	var capability sandbox.Capability
	var sandboxPolicy sandbox.Policy
	if backend != nil {
		capability = backend.Capability()
		sandboxPolicy, _ = sandbox.BackendPolicy(backend)
	}
	if sandboxPolicy.WorkspaceRoot == "" {
		sandboxPolicy.WorkspaceRoot = g.workspace
	}
	enforcement := string(mode)
	profile, err := authority.Compile(authority.CompileInput{
		Runtime:       prepared.runtime,
		Invocation:    policyInput(prepared.invocation.CallID, prepared.invocation),
		Decision:      prepared.decision,
		Authorized:    true,
		Revision:      revision,
		Enforcement:   enforcement,
		Capability:    capability,
		SandboxPolicy: sandboxPolicy,
	})
	if err != nil {
		return authority.EffectivePermissionProfile{}, err
	}
	return profile, nil
}

func (g *Guard) issueExecutionLease(
	ctx context.Context,
	prepared preparedExecution,
	profile authority.EffectivePermissionProfile,
	attempt uint64,
) (
	authority.ExecutionOperation,
	authority.ExecutionLease,
	authority.LeaseSnapshot,
	error,
) {
	policyInvocation := policyInput(
		prepared.invocation.CallID,
		prepared.invocation,
	)
	operation, err := authority.BuildExecutionOperation(authority.OperationInput{
		WorkspaceRoot:          g.workspace,
		WorkspaceID:            g.workspaceID,
		WorkspaceGeneration:    g.workspaceGeneration,
		Invocation:             prepared.invocation,
		Effect:                 policy.NormalizeEffect(policyInvocation),
		Journaled:              policyInvocation.Journaled,
		RequireReadBeforeWrite: policyInvocation.Journaled,
		Required:               requiredControls(prepared.invocation),
		HostReadRoots: append(
			append([]string(nil), profile.Filesystem.ReadRoots...),
			profile.Filesystem.WritePaths...,
		),
	})
	if err != nil {
		return authority.ExecutionOperation{}, authority.ExecutionLease{},
			authority.LeaseSnapshot{}, err
	}
	sandboxPolicyID := ""
	for _, source := range profile.Provenance {
		if source.Kind == "sandbox" {
			sandboxPolicyID = source.Digest
			break
		}
	}
	if sandboxPolicyID == "" && profile.Process.Enforcement == "strong" {
		sandboxPolicyID = authority.FallbackSandboxPolicyID(
			profile.Filesystem.WorkspaceRoot,
			profile.Process.Backend,
			profile.Controls,
		)
		if sandboxPolicyID == "" {
			return operation, authority.ExecutionLease{},
				authority.LeaseSnapshot{}, fmt.Errorf(
					"derive sandbox policy binding for %q", prepared.invocation.Tool,
				)
		}
	}
	expiresAt := g.now().Add(g.leaseTTL)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expiresAt) {
		expiresAt = deadline
	}
	lease, err := g.leaseAuthority.Issue(authority.LeaseIssueRequest{
		Operation: operation, Profile: profile,
		PolicyRevision:  prepared.runtime.Revision,
		SandboxPolicyID: sandboxPolicyID, Attempt: attempt,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		return operation, authority.ExecutionLease{},
			authority.LeaseSnapshot{}, err
	}
	artifactDigest := ""
	if operation.Artifact != nil {
		artifactDigest = operation.Artifact.ManifestDigest
	}
	err = g.leaseAuthority.Consume(lease, authority.LeaseValidation{
		Operation: operation, PolicyRevision: prepared.runtime.Revision,
		WorkspaceID:         operation.WorkspaceID,
		WorkspaceGeneration: operation.WorkspaceGeneration,
		SubjectDigest:       operation.Subject.Digest,
		SubjectGeneration:   operation.Subject.Generation,
		SandboxPolicyID:     sandboxPolicyID, ArtifactDigest: artifactDigest,
		Attempt: attempt,
	})
	if err != nil {
		return operation, lease, authority.LeaseSnapshot{}, err
	}
	snapshot, err := g.leaseAuthority.Snapshot(lease)
	return operation, lease, snapshot, err
}

func requiredControls(invocation Invocation) authority.RequiredControls {
	if invocation.Descriptor.SandboxRequirement != tool.SandboxStrong {
		return authority.RequiredControls{}
	}
	required := authority.RequiredControls{
		FilesystemRead: true,
		Network:        true,
		ProcessTree: invocation.Descriptor.Capability == tool.CapabilityProcess ||
			invocation.Descriptor.Capability == tool.CapabilityPlugin,
	}
	for _, resource := range invocation.Resources {
		if resource.Access == tool.AccessWrite && isPathKind(resource.Kind) {
			required.FilesystemWrite = true
			required.SymlinkSafety = true
		}
	}
	return required
}

func settleExecutionLease(
	manager *authority.LeaseAuthority,
	lease authority.ExecutionLease,
	status tool.OutcomeStatus,
	reason string,
	completed time.Time,
) (authority.LeaseSnapshot, error) {
	if status == "" {
		status = tool.OutcomeFailed
	}
	if err := manager.Settle(lease, authority.Settlement{
		Status: string(status), Reason: reason, CompletedAt: completed,
	}); err != nil {
		return authority.LeaseSnapshot{}, err
	}
	return manager.Snapshot(lease)
}
