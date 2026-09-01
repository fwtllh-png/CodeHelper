package wire

import (
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestRetryPolicyRateLimitAttemptBudget(t *testing.T) {
	policy := RetryPolicy{MaxRetries: 2, RateLimitMaxRetries: 1}
	err := rateLimitError(1000)
	if _, ok := policy.Decide(err, false, 0, false); !ok {
		t.Fatal("first rate limit retry should be allowed")
	}
	policy.RateLimitRetries = 1
	if _, ok := policy.Decide(err, false, 2, false); ok {
		t.Fatal("rate limit attempt budget should be exhausted")
	}
}

func TestRetryPolicyRateLimitWaitBudgetUsesFullProviderDelay(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	policy := RetryPolicy{
		MaxRetries: 2, MaxDelay: 2 * time.Minute,
		RateLimitMaxWait: 2 * time.Minute, Now: func() time.Time { return now },
	}
	retry, ok := policy.Decide(rateLimitError(uint64(time.Hour/time.Millisecond)), false, 0, false)
	if ok {
		t.Fatalf("hour retry-after should exhaust a 2m wait budget: %+v", retry)
	}
	policy.RateLimitMaxWait = 0
	retry, ok = policy.Decide(rateLimitError(uint64(time.Hour/time.Millisecond)), false, 0, false)
	if !ok || retry.EffectiveDelay != time.Hour {
		t.Fatalf("unlimited wait should honor provider delay: %+v", retry)
	}
}

func TestRetryPolicyRateLimitWaitIncludesRouteCooldown(t *testing.T) {
	policy := RetryPolicy{
		MaxDelay: 2 * time.Minute, RouteCooldown: 5 * time.Second,
	}
	retry, ok := policy.Decide(rateLimitError(1000), false, 0, false)
	if !ok || retry.EffectiveDelay != 5*time.Second {
		t.Fatalf("cooldown should raise needed wait: %+v", retry)
	}
	policy.RateLimitMaxWait = 3 * time.Second
	if _, ok := policy.Decide(rateLimitError(1000), false, 0, false); ok {
		t.Fatal("cooldown beyond wait budget should exhaust")
	}
}

func TestRetryPolicyTransientFailuresStillUseAttemptAndDelayCaps(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	policy := RetryPolicy{
		MaxRetries: 2, MaxDelay: time.Second,
		Now: func() time.Time { return now },
	}
	err := protocol.NewProblem(
		protocol.CodeUnavailable,
		"server unavailable",
		true,
		&provider.Failure{Code: provider.FailureServer, Message: "server unavailable"},
	)
	if _, ok := policy.Decide(err, false, 2, false); ok {
		t.Fatal("transient attempt budget should still apply")
	}
	err = protocol.NewProblem(
		protocol.CodeUnavailable,
		"server unavailable",
		true,
		&provider.Failure{
			Code: provider.FailureServer, Message: "server unavailable",
			RetryAfterMS: uint64(time.Hour / time.Millisecond),
		},
	)
	retry, ok := policy.Decide(err, false, 0, false)
	if !ok || retry.EffectiveDelay != time.Second {
		t.Fatalf("transient retry should keep max delay cap: %+v", retry)
	}
}

func rateLimitError(retryAfterMS uint64) error {
	return protocol.NewProblem(
		protocol.CodeUnavailable,
		"rate limited",
		true,
		&provider.Failure{
			Code: provider.FailureRateLimit, Message: "rate limited",
			RetryAfterMS: retryAfterMS,
		},
	)
}
