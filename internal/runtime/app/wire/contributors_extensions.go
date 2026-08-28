package wire

import (
	"errors"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func newExtensionContributors(state *buildState) []extensionActivation {
	output := &state.extensions
	mcpAuthority, _ := mcpruntime.NewRuntimeAuthority(
		state.config.execution.Workspace,
		state.config.workspaceStateID,
		1,
		state.platform.backend,
		state.platform.leaseAuthority,
	)
	return []extensionActivation{
		skillContributor{
			paths: state.config.extensionPaths, workspace: state.config.execution.Workspace,
			output: output,
		},
		newMemoryContributor(state.config.snapshot.Config.Memory, output),
		mcpContributor{
			configPath: state.options.MCPConfigPath, trustedConfigRoot: state.config.extensionPaths.DataDir,
			runtimeAuthority: mcpAuthority, output: output,
		},
	}
}

func runContribution(
	registry *tool.Registry,
	id string,
	contribute func() error,
) (ContributionReceipt, error) {
	if registry == nil {
		return ContributionReceipt{}, errors.New("shared tool registry is required")
	}
	if err := contribute(); err != nil {
		return ContributionReceipt{}, err
	}
	return ContributionReceipt{Contributor: id}, nil
}
