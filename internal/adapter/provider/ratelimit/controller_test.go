package ratelimit

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestMetadataUsesResetWhenRetryAfterIsAbsent(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		header http.Header
		want   time.Duration
	}{
		{
			name: "standard delta",
			header: http.Header{
				"Ratelimit-Remaining": {"0"},
				"Ratelimit-Reset":     {"12.5"},
			},
			want: 12500 * time.Millisecond,
		},
		{
			name: "legacy epoch",
			header: http.Header{
				"X-Ratelimit-Remaining": {"0"},
				"X-Ratelimit-Reset": {
					strconv.FormatInt(now.Unix()+30, 10),
				},
			},
			want: 30 * time.Second,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := Metadata(test.header, now)
			if metadata == nil ||
				metadata.RetryAfterMS != uint64(test.want/time.Millisecond) {
				t.Fatalf("metadata = %+v", metadata)
			}
		})
	}
}

func TestMetadataPrefersRetryAfter(t *testing.T) {
	metadata := Metadata(http.Header{
		"Retry-After":         {"7"},
		"Ratelimit-Remaining": {"0"},
		"Ratelimit-Reset":     {"30"},
	}, time.Now())
	if metadata == nil || metadata.RetryAfterMS != 7000 {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestControllerCooldownIsCancelableAndRouteScoped(t *testing.T) {
	var controller Controller
	controller.Observe(
		"limited",
		0,
		http.StatusTooManyRequests,
		http.Header{"Retry-After": {"1"}},
		nil,
	)

	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if err := controller.Wait(ctx, "limited", 0); err == nil {
		t.Fatal("limited route ignored cancellation")
	}
	if err := controller.Wait(t.Context(), "other", 0); err != nil {
		t.Fatalf("independent route was throttled: %v", err)
	}
}

func TestControllerIncreasesHeaderlessCooldownAfterRepeatedLimits(t *testing.T) {
	var controller Controller
	controller.Observe(
		"limited", 100, http.StatusTooManyRequests, nil, nil,
	)
	first := controller.routes["limited"].penalty
	controller.Observe(
		"limited", 100, http.StatusTooManyRequests, nil, nil,
	)
	second := controller.routes["limited"].penalty
	if first != 10*time.Millisecond || second != 2*first {
		t.Fatalf("adaptive penalties = %s, %s", first, second)
	}
}
