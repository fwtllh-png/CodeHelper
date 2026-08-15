package wire

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	memorytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/memory"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func newExtensionContributors(state *buildState) []extensionContributor {
	output := &state.extensions
	return []extensionContributor{
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
		memoryContributor{
			config: state.config.snapshot.Config.Memory,
			output: output,
		},
		dynamicToolContributor{
			enabled: state.options.TrustedDynamicTools,
			output:  output,
		},
		hookContributor{
			path:      state.config.extensionPaths.HooksConfigPath,
			explicit:  state.options.Extensions.HooksConfigPath != "",
			workspace: state.config.execution.Workspace,
			backend:   state.platform.backend, output: output,
		},
		mcpContributor{
			configPath: state.options.MCPConfigPath,
			output:     output,
		},
	}
}

func publishExtensionOutputs(state *buildState) {
	if state == nil || state.session == nil {
		return
	}
	output := &state.extensions
	session := state.session
	session.plugins = output.plugins
	session.pluginRegistry = output.pluginRegistry
	session.pluginTools = output.pluginTools
	session.memory = output.memory
	session.hooks = output.hooks
	session.mcpPool = output.mcpPool
	session.mcpPrewarm = output.mcpPrewarm
	session.dynamicTools = output.dynamicTools
	session.contributionReceipts = append(
		[]ContributionReceipt(nil),
		output.receipts...,
	)
	state.tools.skillCatalog = output.skillCatalog
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
	_ context.Context,
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
		c.output.pluginRegistry, c.output.pluginTools = lifecycle, adapter
		return nil
	})
}

type skillContributor struct {
	paths     ExtensionPaths
	workspace string
	output    *extensionBuildState
}

func (skillContributor) ID() string { return "skills" }

func (c skillContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), []string{"skill-catalog"}, func() error {
		stateStore, err := skill.NewStateStore(c.paths.SkillsStatePath)
		if err != nil {
			return fmt.Errorf("skill state: %w", err)
		}
		lockStore, err := skill.NewLockStore(c.paths.SkillsLockPath)
		if err != nil {
			return fmt.Errorf("skill lock: %w", err)
		}
		catalog, err := skill.Discover(skill.DiscoveryOptions{
			Workspace: c.workspace, ConfiguredDir: c.paths.SkillsConfiguredDir,
			UserHome: c.paths.UserHome, Locale: c.paths.SkillsLocale,
			State: stateStore, Lock: lockStore, RuntimeVersion: buildinfo.Version,
		})
		if err != nil {
			return fmt.Errorf("skill discovery: %w", err)
		}
		if err := catalog.Verify(ctx); err != nil {
			return fmt.Errorf("skill lock verify: %w", err)
		}
		if err := skilltool.Register(registry, catalog); err != nil {
			return fmt.Errorf("skill tool: %w", err)
		}
		c.output.skillCatalog = catalog
		return nil
	})
}

type memoryContributor struct {
	config config.Memory
	output *extensionBuildState
}

func (memoryContributor) ID() string { return "memory" }

func (c memoryContributor) Contribute(
	_ context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), []string{"memory-store"}, func() error {
		if !c.config.Enabled {
			return nil
		}
		store, err := memory.Open(c.config.Path)
		if err != nil {
			return fmt.Errorf("memory store: %w", err)
		}
		if err := memorytool.Register(registry, store); err != nil {
			return fmt.Errorf("remember tool: %w", err)
		}
		c.output.memory = store
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

type hookContributor struct {
	path, workspace string
	explicit        bool
	backend         sandbox.Backend
	output          *extensionBuildState
}

func (hookContributor) ID() string { return "hooks" }

func (c hookContributor) Contribute(
	_ context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), []string{"hook-manager"}, func() error {
		info, err := os.Lstat(c.path)
		if err != nil {
			if c.explicit || !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("hooks config: %w", err)
			}
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("hooks config must be a regular non-symlink file")
		}
		config, err := hooks.LoadConfig(c.path)
		if err != nil {
			return fmt.Errorf("hooks config: %w", err)
		}
		manager, err := hooks.New(config, hooks.Options{
			Workspace: c.workspace, Sandbox: c.backend, RequireStrongSandbox: true,
		})
		if err != nil {
			return fmt.Errorf("hooks manager: %w", err)
		}
		c.output.hooks = manager
		return nil
	})
}

type mcpContributor struct {
	configPath string
	output     *extensionBuildState
}

func (mcpContributor) ID() string { return "mcp" }

func (c mcpContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), []string{"mcp-pool", "mcp-prewarm"}, func() error {
		if c.configPath == "" {
			return nil
		}
		pool, prewarm, err := RegisterMCPTools(ctx, registry, c.configPath)
		if err != nil {
			return fmt.Errorf("MCP tools: %w", err)
		}
		c.output.mcpPool, c.output.mcpPrewarm = pool, prewarm
		return nil
	})
}
