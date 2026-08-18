package engine

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func normalizeEngineOptions(options *Options) error {
	if err := validateReasoningEffort(
		options.Routes,
		options.ReasoningEffort,
	); err != nil {
		return err
	}
	if options.MaxRetries < 0 {
		return errors.New("max retries cannot be negative")
	}
	if options.MaxRetryDelay < 0 {
		return errors.New("max retry delay cannot be negative")
	}
	if options.MaxRetryDelay == 0 {
		options.MaxRetryDelay = 2 * time.Minute
	}
	return nil
}

func waitRetryDelay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func validateReasoningEffort(
	routes model.RouteSet,
	effort string,
) error {
	if effort == "" {
		return nil
	}
	entries := []model.ReadyRoute{routes.Act()}
	for _, purpose := range routes.Slots() {
		route, err := routes.For(purpose)
		if err != nil {
			return err
		}
		entries = append(entries, route)
	}
	for _, route := range entries {
		if !route.Model().Capabilities.SupportsReasoningEffort(effort) {
			return fmt.Errorf(
				"model %q does not support reasoning effort %q",
				route.Model().ID,
				effort,
			)
		}
	}
	return nil
}
