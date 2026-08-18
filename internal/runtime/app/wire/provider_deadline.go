package wire

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

func configureProviderClient(
	execution *config.Execution,
	egressGate *egress.Gate,
	metrics *telemetry.Metrics,
) *httpclient.Client {
	client := httpclient.New()
	client.Egress, client.Metrics = egressGate, metrics
	client.SetDeadlineConfig(httpclient.DeadlineConfig{
		Connection: effectiveProviderDeadline(
			execution.ConnectionTimeout,
			execution.Timeout,
		),
		TLSHandshake: effectiveProviderDeadline(
			execution.TLSHandshakeTimeout,
			execution.Timeout,
		),
		ResponseHeaders: effectiveProviderDeadline(
			execution.ResponseHeaderTimeout,
			execution.Timeout,
		),
	})
	client.IdleTimeout = execution.IdleTimeout
	client.MaxConcurrent = execution.MaxConcurrent
	client.RequestsPerSecond = execution.RateLimit
	return client
}

func effectiveProviderDeadline(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
