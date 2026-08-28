package wire

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
)

type observabilityModule struct{}

type traceSession struct {
	runtime trace.Runtime
}

func (observabilityModule) Name() string { return "observability" }

func (observabilityModule) Build(
	_ context.Context,
	state *buildState,
) error {
	state.session.traces = traceSession{runtime: trace.NewRuntime(nil)}
	return nil
}

func engineObservability(state *buildState) trace.Runtime {
	return state.session.traces.runtime
}

func runtimeObservability(state *buildState) app.RuntimeObservability {
	return app.RuntimeObservability{
		Metrics: state.session.metrics,
		Logger:  state.session.logger,
		Runtime: state.session.traces.runtime,
	}
}
