package wire

import (
	"context"
	"fmt"
	"os"

	"github.com/fwtllh-png/CodeHelper/internal/observability/privacy"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
)

type observabilityModule struct{}

type observationSession struct {
	runtime trace.Runtime
	router  trace.ObservationRouter
}

func (observabilityModule) Name() string { return "observability" }

func (observabilityModule) Build(
	_ context.Context,
	state *buildState,
) error {
	store := state.options.PersistentStore
	if store == nil {
		return nil
	}
	mode, err := privacy.ParseMode(
		os.Getenv("CODEHELPER_OBSERVATION_CAPTURE"),
	)
	if err != nil {
		return fmt.Errorf("observation capture configuration: %w", err)
	}
	var secrets []string
	credential := state.config.snapshot.Config.Credential
	if credential.Kind == "env" && credential.Name != "" {
		if value := os.Getenv(credential.Name); value != "" {
			secrets = append(secrets, value)
		}
	}
	restrictedPaths := []string{store.Root()}
	if state.options.ConfigPath != "" {
		restrictedPaths = append(restrictedPaths, state.options.ConfigPath)
	}
	runtime, router, err := trace.OpenDurableRuntime(
		store.Root(),
		store.Content(),
		trace.DurableOptions{
			CaptureMode:     mode,
			Secrets:         secrets,
			RestrictedPaths: restrictedPaths,
		},
	)
	if err != nil {
		return err
	}
	state.session.observability = observationSession{
		runtime: runtime,
		router:  router,
	}
	return nil
}

func engineObservability(state *buildState) trace.Runtime {
	return state.session.observability.runtime
}

func runtimeObservability(state *buildState) app.RuntimeObservability {
	return app.RuntimeObservability{
		Metrics: state.session.metrics,
		Logger:  state.session.logger,
		Runtime: state.session.observability.runtime,
	}
}
