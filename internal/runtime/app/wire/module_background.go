package wire

import (
	"context"
	"fmt"
)

type backgroundModule struct{}

func (backgroundModule) Name() string { return "background" }

func (backgroundModule) Build(ctx context.Context, state *buildState) error {
	if prewarm := state.extensions.mcpPrewarm; prewarm != nil {
		if err := prewarm.RefreshNow(ctx); err != nil {
			return fmt.Errorf("initial MCP refresh: %w", err)
		}
	}
	if err := state.runtime.application.Start(ctx); err != nil {
		return fmt.Errorf("start runtime recovery: %w", err)
	}
	if prewarm := state.extensions.mcpPrewarm; prewarm != nil {
		prewarm.Start(ctx)
	}
	return nil
}
