package wire

import (
	"context"
	"fmt"

	memorystore "github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	memorytool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/memory"
	"github.com/fwtllh-png/CodeHelper/internal/config"
)

func contributeMemory(
	ctx context.Context,
	registry *tool.Registry,
	configuration config.Memory,
	output *capabilityBuildState,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !configuration.Enabled {
		return nil
	}
	store, err := memorystore.Open(configuration.Path, memorystore.Options{
		MaxCandidates:  configuration.MaxCandidates,
		MaxPromptBytes: configuration.MaxPromptBytes,
	})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	if err := memorytool.Register(registry, store); err != nil {
		return fmt.Errorf("register tools: %w", err)
	}
	output.memory = store
	return nil
}

func publishCapabilityOutputs(state *buildState) {
	if state == nil || state.session == nil {
		return
	}
	output := &state.capabilities
	state.session.memory = output.memory
	state.session.mcpPool = output.mcpPool
	state.session.mcpPrewarm = output.mcpPrewarm
	state.tools.skillCatalog = output.skillCatalog
}
