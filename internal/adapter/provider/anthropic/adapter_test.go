package anthropic

import (
	"errors"
	"net/http"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/QCode/internal/adapter/provider/wire"
)

func TestClassifyHTTPDistinguishesRateLimitAndQuota(t *testing.T) {
	tests := []struct {
		name string
		body string
		want provider.FailureCode
	}{
		{
			name: "temporary rate limit",
			body: `{"error":{"type":"rate_limit_error",` +
				`"message":"rate limit exceeded"}}`,
			want: provider.FailureRateLimit,
		},
		{
			name: "credit balance",
			body: `{"error":{"type":"rate_limit_error",` +
				`"message":"Your credit balance is too low"}}`,
			want: provider.FailureQuota,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := NewAdapter().ClassifyHTTP(providerwire.HTTPFailure{
				Status: http.StatusTooManyRequests,
				Body:   test.body,
			})
			var failure *provider.Failure
			if !errors.As(err, &failure) {
				t.Fatalf("failure = %T %v", err, err)
			}
			if failure.Code != test.want {
				t.Fatalf("failure = %+v", failure)
			}
		})
	}
}
