package wire

import (
	"context"
	"fmt"
	"time"
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
	if automations := state.orchestration.automations; automations != nil {
		if _, err := automations.Tick(ctx, time.Time{}); err != nil {
			return fmt.Errorf("automation reconcile: %w", err)
		}
	}
	scheduler, err := state.orchestration.scheduler.Build(
		state.runtime.application,
		state.agent.workspaceTurnGate,
	)
	if err != nil {
		return fmt.Errorf("worker scheduler: %w", err)
	}
	if scheduler == nil {
		return nil
	}
	if err := scheduler.Start(ctx); err != nil {
		return fmt.Errorf("start worker scheduler: %w", err)
	}
	state.session.scheduler = scheduler
	return nil
}
