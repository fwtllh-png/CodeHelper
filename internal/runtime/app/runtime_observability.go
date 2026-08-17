package app

import (
	"log/slog"

	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
)

type RuntimeObservability struct {
	Metrics *telemetry.Metrics
	Logger  *slog.Logger
	Runtime trace.Runtime
}
