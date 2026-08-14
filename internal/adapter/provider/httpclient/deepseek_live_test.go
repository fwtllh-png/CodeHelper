package httpclient

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

const deepSeekLiveControlEnv = "CODEHELPER_DEEPSEEK_LIVE_CONTROL"

func TestDeepSeekP0LiveControl(t *testing.T) {
	if os.Getenv(deepSeekLiveControlEnv) != "1" {
		t.Skipf("DeepSeek live control disabled; set %s=1", deepSeekLiveControlEnv)
	}
	route := p0BundledRoute(t, "deepseek-v4-flash", "deepseek-v4-flash")
	credential, err := DefaultCredentials().Resolve(t.Context(), route.Credential())
	if err != nil {
		t.Skipf("DeepSeek live control skipped: configured credential is unavailable: %v", err)
	}

	gate := &egress.Gate{Enforce: true}
	if !gate.AllowURL(route.Endpoint()) {
		t.Fatalf("cannot grant DeepSeek endpoint %q", route.Endpoint())
	}
	metrics := telemetry.NewMetrics()
	client := New()
	client.HTTP = &http.Client{Timeout: 3 * time.Minute}
	client.Credentials = p0LiveCredential(credential)
	client.Egress = gate
	client.Metrics = metrics
	client.MaxAttempts = 1
	client.IdleTimeout = 2 * time.Minute

	stream, err := client.Stream(t.Context(), provider.ModelRequest{
		Route: route,
		Messages: []provider.Message{
			provider.TextMessage(
				provider.RoleUser,
				"Reply with exactly codehelper-provider-p0-live-ok.",
			),
		},
		MaxOutputTokens: 4096,
		ReasoningEffort: "max",
		Idempotent:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, err := provider.Drain(stream)
	if err != nil {
		t.Fatal(err)
	}
	var meaningful, usage, stopped bool
	for _, event := range events {
		switch event.Type {
		case provider.EventTextDelta, provider.EventReasoningDelta,
			provider.EventToolCallDelta, provider.EventSearchResult,
			provider.EventCitation:
			meaningful = true
		case provider.EventUsage:
			usage = true
		case provider.EventMessageStop:
			stopped = true
		}
	}
	if !meaningful || !usage || !stopped {
		t.Fatalf(
			"DeepSeek live control events: meaningful=%t usage=%t stopped=%t",
			meaningful, usage, stopped,
		)
	}
	if requests := metrics.Snapshot().ProviderRequests; requests != 1 {
		t.Fatalf("DeepSeek live control provider requests = %d, want 1", requests)
	}
}

type p0LiveCredential string

func (c p0LiveCredential) Resolve(
	context.Context,
	model.CredentialRef,
) (string, error) {
	return string(c), nil
}
