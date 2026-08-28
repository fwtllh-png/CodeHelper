package wire

import (
	"context"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func newExtensionContributors(state *buildState) []extensionActivation {
	output := &state.extensions
	hookRuntime, _ := hooks.NewRuntime(
		state.config.workspaceStateID, 1, state.platform.leaseAuthority,
	)
	mcpAuthority, _ := mcpruntime.NewRuntimeAuthority(
		state.config.execution.Workspace,
		state.config.workspaceStateID,
		1,
		state.platform.backend,
		state.platform.leaseAuthority,
	)
	return []extensionActivation{
		pluginBundleContributor{
			bundle: state.options.PluginBundle, receipt: state.options.PluginReceipt,
			workspace: state.config.execution.Workspace,
			backend:   state.platform.backend, output: output,
		},
		pluginRegistryContributor{
			paths: state.config.extensionPaths, workspace: state.config.execution.Workspace,
			backend: state.platform.backend, output: output,
		},
		skillContributor{
			paths: state.config.extensionPaths, workspace: state.config.execution.Workspace,
			output: output,
		},
		newMemoryContributor(state.config.snapshot.Config.Memory, output),
		dynamicToolContributor{
			enabled: state.options.TrustedDynamicTools,
			output:  output,
		},
		hookContributor{
			path:      state.config.extensionPaths.HooksConfigPath,
			explicit:  state.options.Extensions.HooksConfigPath != "",
			workspace: state.config.execution.Workspace,
			backend:   state.platform.backend, runtime: hookRuntime, output: output,
		},
		mcpContributor{
			configPath: state.options.MCPConfigPath, trustedConfigRoot: state.config.extensionPaths.DataDir,
			runtimeAuthority: mcpAuthority, output: output,
		},
	}
}

func runContribution(
	registry *tool.Registry,
	id string,
	outputs []string,
	contribute func() error,
) (ContributionReceipt, error) {
	before, err := snapshotCatalog(registry)
	if err != nil {
		return ContributionReceipt{}, err
	}
	if err := contribute(); err != nil {
		return ContributionReceipt{}, err
	}
	after, err := snapshotCatalog(registry)
	if err != nil {
		return ContributionReceipt{}, err
	}
	return contributionReceipt(id, before, after, outputs...), nil
}

type pluginBundleContributor struct {
	bundle, receipt, workspace string
	backend                    sandbox.Backend
	output                     *extensionBuildState
}

func (pluginBundleContributor) ID() string { return "plugin-bundle" }

func (c pluginBundleContributor) Contribute(
	_ context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), nil, func() error {
		if c.bundle == "" {
			return nil
		}
		receipt, err := pluginruntime.LoadReceipt(c.receipt)
		if err != nil {
			return fmt.Errorf("plugin receipt: %w", err)
		}
		loader, err := pluginruntime.NewLoader(c.workspace, c.backend)
		if err != nil {
			return fmt.Errorf("plugin loader: %w", err)
		}
		loaded, err := loader.Load(c.bundle, receipt)
		if err != nil {
			return fmt.Errorf("plugin load: %w", err)
		}
		if err := plugintool.Register(registry, loaded); err != nil {
			_ = loaded.Close()
			return fmt.Errorf("plugin register: %w", err)
		}
		c.output.plugins = append(c.output.plugins, loaded)
		return nil
	})
}

type pluginRegistryContributor struct {
	paths     ExtensionPaths
	workspace string
	backend   sandbox.Backend
	output    *extensionBuildState
}

func (pluginRegistryContributor) ID() string { return "plugin-registry" }

func (c pluginRegistryContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), []string{"plugin-registry"}, func() error {
		lifecycle, err := NewPluginRegistry(c.paths, c.workspace, c.backend)
		if err != nil {
			return fmt.Errorf("plugin registry: %w", err)
		}
		if err := lifecycle.Reload(); err != nil {
			_ = lifecycle.Close()
			return fmt.Errorf("plugin registry reload: %w", err)
		}
		adapter, err := plugintool.NewAdapter(registry, lifecycle)
		if err != nil {
			_ = lifecycle.Close()
			return fmt.Errorf("register lifecycle plugin tools: %w", err)
		}
		capabilities, err := lifecycle.CapabilityBundles(ctx)
		if err != nil {
			_ = adapter.Close()
			_ = lifecycle.Close()
			return fmt.Errorf("compile plugin capabilities: %w", err)
		}
		c.output.pluginRegistry, c.output.pluginTools = lifecycle, adapter
		c.output.pluginCapabilities = capabilities
		return nil
	})
}

type dynamicToolContributor struct {
	enabled bool
	output  *extensionBuildState
}

func (dynamicToolContributor) ID() string { return "dynamic-tools" }

func (c dynamicToolContributor) Contribute(
	_ context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), []string{"dynamic-tool-manager"}, func() error {
		if !c.enabled {
			return nil
		}
		manager, err := dynamictool.NewManager(
			registry,
			dynamictool.DefaultRegistrationPolicy(),
		)
		if err != nil {
			return fmt.Errorf("dynamic tool manager: %w", err)
		}
		c.output.dynamicTools = manager
		return nil
	})
}
