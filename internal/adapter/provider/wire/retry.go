package wire

import (
	"context"
	"errors"
	"io"
	"syscall"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

const RetryPolicyRevision = "provider-retry/v3"

type RetryPolicy struct {
	MaxRetries          int
	MaxDelay            time.Duration
	RateLimitMaxRetries int
	RateLimitMaxWait    time.Duration
	RateLimitRetries    uint32
	RateLimitWaited     time.Duration
	RouteCooldown       time.Duration
	Now                 func() time.Time
}

type RetryDecision struct {
	Attempt        int                `json:"attempt"`
	Retry          uint32             `json:"retry"`
	Code           protocol.ErrorCode `json:"code"`
	Category       string             `json:"category"`
	Failure        provider.Failure   `json:"failure"`
	EffectiveDelay time.Duration      `json:"effective_delay"`
	RetryAt        time.Time          `json:"retry_at"`
	PolicyRevision string             `json:"policy_revision"`
}

func (p RetryPolicy) Decide(
	err error,
	meaningful bool,
	retries uint32,
	contextChanged bool,
) (RetryDecision, bool) {
	failure := ClassifyFailure(err, meaningful)
	limit := p.MaxRetries
	if limit < 1 && protocol.IsRetryable(err) {
		limit = 1
	}
	if limit < 1 {
		limit = 1
	}
	eligible := false
	attempt := int(retries)
	switch failure.Code {
	case provider.FailureRateLimit:
		eligible = true
		attempt = int(p.RateLimitRetries)
		if p.RateLimitMaxRetries > 0 {
			limit = p.RateLimitMaxRetries
		} else {
			limit = 0
		}
	case provider.FailureServer,
		provider.FailureTransport,
		provider.FailureStreamClosed,
		provider.FailureTimeout:
		eligible = true
	case provider.FailureContextWindowExceeded:
		eligible = contextChanged
		limit = max(1, limit)
	case provider.FailureEmptyResponse:
		eligible = true
		limit = 1
	case provider.FailureUnknown:
		eligible = protocol.IsRetryable(err)
	}
	if !eligible {
		return RetryDecision{}, false
	}
	decision := protocol.DecideRecovery(
		RecoveryFault(err, failure),
		protocol.RecoveryContext{
			Owner:       protocol.FaultRetryOwnerEngine,
			Idempotent:  true,
			Progress:    meaningful,
			Attempt:     attempt,
			MaxAttempts: limit,
		},
	)
	if decision.Action != protocol.RecoveryRetry &&
		decision.Action != protocol.RecoveryWait {
		return RetryDecision{}, false
	}
	backoffRetries := retries
	if failure.Code == provider.FailureRateLimit {
		backoffRetries = p.RateLimitRetries
	}
	delayMS := failure.RetryAfterMS
	if delayMS == 0 {
		delayMS = uint64(retryBackoff(backoffRetries) / time.Millisecond)
	}
	needed := time.Duration(delayMS) * time.Millisecond
	if p.RouteCooldown > needed {
		needed = p.RouteCooldown
	}
	providerSpecified := failure.RetryAfterMS > 0 || p.RouteCooldown > 0
	if !providerSpecified && p.MaxDelay > 0 && needed > p.MaxDelay {
		needed = p.MaxDelay
	}
	if failure.Code == provider.FailureRateLimit &&
		p.RateLimitMaxWait > 0 &&
		p.RateLimitWaited+needed > p.RateLimitMaxWait {
		return RetryDecision{}, false
	}
	if failure.Code != provider.FailureRateLimit &&
		p.MaxDelay > 0 && needed > p.MaxDelay {
		needed = p.MaxDelay
	}
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	return RetryDecision{
		Attempt: int(p.RateLimitRetries + retries + 1), Retry: p.RateLimitRetries + retries + 1,
		Code: protocol.CodeOf(err), Category: FailureCategory(err, failure.Code),
		Failure: failure, EffectiveDelay: needed, RetryAt: now.Add(needed),
		PolicyRevision: RetryPolicyRevision,
	}, true
}

func ClassifyFailure(err error, meaningful bool) provider.Failure {
	var classified *provider.Failure
	if errors.As(err, &classified) && classified != nil {
		failure := *classified
		if failure.Message == "" {
			failure.Message = errorText(err)
		}
		return failure
	}
	code := provider.FailureUnknown
	switch {
	case errors.Is(err, context.Canceled):
		code = provider.FailureAborted
	case errors.Is(err, context.DeadlineExceeded):
		code = provider.FailureTimeout
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		code = provider.FailureTransport
	case errors.Is(err, io.ErrUnexpectedEOF):
		if meaningful {
			code = provider.FailureStreamClosed
		} else {
			code = provider.FailureTransport
		}
	case protocol.CodeOf(err) == protocol.CodeUnavailable:
		code = provider.FailureTransport
	}
	return provider.Failure{Code: code, Message: errorText(err)}
}

func FailureCategory(err error, code provider.FailureCode) string {
	switch {
	case errors.Is(err, syscall.ECONNRESET):
		return "connection_reset"
	case errors.Is(err, syscall.EPIPE):
		return "broken_pipe"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case code != "":
		return string(code)
	default:
		return "provider_unavailable"
	}
}

func RecoveryFault(err error, failure provider.Failure) error {
	problem := protocol.ProblemOf(err)
	code := protocol.CodeUnavailable
	retryable := protocol.IsRetryable(err)
	switch failure.Code {
	case provider.FailureRateLimit,
		provider.FailureServer,
		provider.FailureTransport,
		provider.FailureStreamClosed,
		provider.FailureTimeout,
		provider.FailureContextWindowExceeded,
		provider.FailureEmptyResponse:
		retryable = true
	}
	if failure.Code == provider.FailureTimeout ||
		errors.Is(err, context.DeadlineExceeded) {
		code = protocol.CodeDeadlineExceeded
		retryable = true
	}
	if problem != nil {
		code = problem.Code
	}
	return protocol.NewFault(
		code,
		failure.Message,
		retryable,
		protocol.FaultMetadata{
			Origin:      protocol.FaultOriginProvider,
			Stage:       protocol.FaultStageModelSample,
			RetryOwner:  protocol.FaultRetryOwnerEngine,
			ResumeHint:  protocol.FaultResumeRetryStep,
			Disposition: protocol.FaultRetryStep,
			SideEffects: protocol.SideEffectUnchanged,
		},
		err,
	)
}

func retryBackoff(retries uint32) time.Duration {
	delay := 10 * time.Millisecond
	for index := uint32(0); index < retries && delay < 30*time.Second; index++ {
		delay *= 2
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	jitterSteps := (retries % 4) + 1
	return delay + (delay/20)*time.Duration(jitterSteps)
}

func errorText(err error) string {
	if err == nil {
		return "provider failed"
	}
	return err.Error()
}
