package wire

import (
	"context"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/security/credential"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

func configureProviderClient(
	execution *config.Execution,
	egressGate *egress.Gate,
	metrics *telemetry.Metrics,
	control *credential.Control,
	controlled model.CredentialRef,
) *httpclient.Client {
	client := httpclient.New()
	if control != nil {
		client.Credentials = liveCredentialResolver{
			control: control, controlled: controlled,
			fallback: httpclient.DefaultCredentials(),
		}
	}
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

type liveCredentialResolver struct {
	control    *credential.Control
	controlled model.CredentialRef
	fallback   httpclient.Credentials
}

func (r liveCredentialResolver) Resolve(
	ctx context.Context,
	reference model.CredentialRef,
) (string, error) {
	if reference == r.controlled {
		current, err := r.control.Reference(ctx)
		if err != nil {
			return "", err
		}
		reference = model.CredentialRef{
			Kind: current.Kind,
			Name: current.Name,
		}
	}
	return r.fallback.Resolve(ctx, reference)
}

func effectiveProviderDeadline(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
