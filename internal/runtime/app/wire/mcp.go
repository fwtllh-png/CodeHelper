package wire

import (
	"context"
	"fmt"

	pluginextension "github.com/fwtllh-png/CodeHelper/internal/adapter/extension/plugin"
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type mcpContributor struct {
	configPath        string
	trustedConfigRoot string
	output            *extensionBuildState
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
			if err := sandbox.RequireTrustedConfigFile(
				c.configPath, c.trustedConfigRoot, "MCP config",
			); err != nil {
				return err
			}
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

func RegisterMCPConfig(
	registry *tool.Registry,
	config mcpruntime.Config,
) (*mcpruntime.Pool, *MCPPrewarm, error) {
	config = mcpruntime.CloneConfig(config)
	if err := config.Validate(); err != nil {
		return nil, nil, err
	}
	pool := mcpruntime.NewPool(nil)
	prewarm := NewMCPPrewarmConfig(pool, config)
	prewarm.SetRegistry(registry)
	prewarm.RequestRefresh()
	return pool, prewarm, nil
}
