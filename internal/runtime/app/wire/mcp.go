package wire

import (
	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

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
