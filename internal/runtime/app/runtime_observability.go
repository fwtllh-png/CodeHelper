package app

import (
	"context"
	"log/slog"

	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
)

type RuntimeObservability struct {
	Metrics             *telemetry.Metrics
	Logger              *slog.Logger
	Runtime             trace.Runtime
	ObservationSnapshot trace.ObservationSnapshotter
	TraceQuery          RuntimeTraceQuery
	TraceExport         RuntimeTraceExport
}

type RuntimeTraceQuery interface {
	Query(context.Context, trace.TraceQuery) (trace.TraceSnapshot, error)
}

type RuntimeTraceExport interface {
	Export(context.Context, trace.ExportRequest) (trace.ExportResult, error)
}
