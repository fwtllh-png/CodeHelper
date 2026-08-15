package guard

import (
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

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
