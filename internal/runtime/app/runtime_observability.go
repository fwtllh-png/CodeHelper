package app

import (
	"context"
	"log/slog"

	"github.com/fwtllh-png/QCode/internal/observability/telemetry"
	"github.com/fwtllh-png/QCode/internal/observability/trace"
)

type RuntimeObservability struct {
	Metrics    *telemetry.Metrics
	Logger     *slog.Logger
	Runtime    trace.Runtime
	TraceQuery RuntimeTraceQuery
}

type RuntimeTraceQuery interface {
	Query(context.Context, trace.TraceQuery) (trace.TraceSnapshot, error)
}
