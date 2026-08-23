package wire

import (
	"context"
	"errors"
	"fmt"
	"os"

	pluginextension "github.com/fwtllh-png/CodeHelper/internal/adapter/extension/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/buildinfo"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

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
	return runContribution(
		registry, c.ID(),
		[]string{"skill-catalog", "skills_list", "skills_read"},
		func() error {
			stateStore, err := skill.NewStateStore(c.paths.SkillsStatePath)
			if err != nil {
				return fmt.Errorf("skill state: %w", err)
			}
			lockStore, err := skill.NewLockStore(c.paths.SkillsLockPath)
			if err != nil {
				return fmt.Errorf("skill lock: %w", err)
			}
			pluginSkills, err := pluginextension.StageSkills(
				ctx, c.output.pluginCapabilities, c.output.pluginRegistry,
			)
			if err != nil {
				return err
			}
			catalog, err := skill.Discover(skill.DiscoveryOptions{
				Workspace: c.workspace, ConfiguredDir: c.paths.SkillsConfiguredDir,
				UserHome: c.paths.UserHome, Locale: c.paths.SkillsLocale,
				State: stateStore, Lock: lockStore, RuntimeVersion: buildinfo.Version,
				Plugins: pluginSkills,
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
			if err := skilltool.RegisterDiscovery(registry, catalog); err != nil {
				return fmt.Errorf("skill discovery tools: %w", err)
			}
			c.output.skillCatalog = catalog
			return nil
		},
	)
}

type hookContributor struct {
	path, workspace string
	explicit        bool
	backend         sandbox.Backend
	output          *extensionBuildState
}

func (hookContributor) ID() string { return "hooks" }

func (c hookContributor) Contribute(
	ctx context.Context,
	registry *tool.Registry,
) (ContributionReceipt, error) {
	return runContribution(registry, c.ID(), []string{"hook-manager"}, func() error {
		combined := hooks.Config{
			Version: hooks.ConfigVersion,
			Hooks:   make(map[hooks.Event][]hooks.HookConfig),
		}
		configured := false
		info, err := os.Lstat(c.path)
		if err != nil {
			if c.explicit || !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("hooks config: %w", err)
			}
		} else if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("hooks config must be a regular non-symlink file")
		} else {
			config, loadErr := hooks.LoadConfig(c.path)
			if loadErr != nil {
				return fmt.Errorf("hooks config: %w", loadErr)
			}
			mergeHookConfig(&combined, config)
			configured = true
		}
		pluginHooks, pluginConfigured, err := pluginextension.HookConfig(
			ctx, c.output.pluginCapabilities, c.output.pluginRegistry,
		)
		if err != nil {
			return err
		}
		if pluginConfigured {
			mergeHookConfig(&combined, pluginHooks)
			configured = true
		}
		if !configured {
			return nil
		}
		if err := combined.Validate(); err != nil {
			return fmt.Errorf("merged hooks config: %w", err)
		}
		manager, err := hooks.New(combined, hooks.Options{
			Sandbox: c.backend, RequireStrongSandbox: true, Workspace: c.workspace,
		})
		if err != nil {
			return fmt.Errorf("hooks manager: %w", err)
		}
		c.output.hooks = manager
		return nil
	})
}

func mergeHookConfig(target *hooks.Config, source hooks.Config) {
	if target == nil {
		return
	}
	if target.Hooks == nil {
		target.Hooks = make(map[hooks.Event][]hooks.HookConfig)
	}
	for event, configured := range source.Hooks {
		target.Hooks[event] = append(target.Hooks[event], configured...)
	}
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
		combined := mcpruntime.Config{
			Version: mcpruntime.ConfigVersion,
			Servers: make(map[string]mcpruntime.ServerConfig),
		}
		configured := false
		if c.configPath != "" {
			config, err := mcpruntime.LoadConfig(c.configPath)
			if err != nil {
				return fmt.Errorf("MCP tools: %w", err)
			}
			for name, server := range config.Servers {
				combined.Servers[name] = server
			}
			configured = true
		}
		pluginMCP, pluginConfigured, err := pluginextension.MCPConfig(
			ctx, c.output.pluginCapabilities, c.output.pluginRegistry,
		)
		if err != nil {
			return err
		}
		if pluginConfigured {
			for name, server := range pluginMCP.Servers {
				if _, exists := combined.Servers[name]; exists {
					return fmt.Errorf("MCP server %q is duplicated", name)
				}
				combined.Servers[name] = server
			}
			configured = true
		}
		if !configured {
			return nil
		}
		pool, prewarm, err := RegisterMCPConfig(registry, combined)
		if err != nil {
			return fmt.Errorf("MCP tools: %w", err)
		}
		c.output.mcpPool, c.output.mcpPrewarm = pool, prewarm
		return nil
	})
}
