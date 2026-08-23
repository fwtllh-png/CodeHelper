package wire

import (
	"context"
	"errors"
	"io"
	"syscall"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const RetryPolicyRevision = "provider-retry/v1"

type RetryPolicy struct {
	MaxRetries int
	MaxDelay   time.Duration
	Now        func() time.Time
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
	eligible := false
	switch failure.Code {
	case provider.FailureRateLimit,
		provider.FailureServer,
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
	if limit < 1 {
		limit = 1
	}
	decision := protocol.DecideRecovery(
		RecoveryFault(err, failure),
		protocol.RecoveryContext{
			Owner:       protocol.FaultRetryOwnerEngine,
			Idempotent:  true,
			Progress:    meaningful,
			Attempt:     int(retries),
			MaxAttempts: limit,
		},
	)
	if decision.Action != protocol.RecoveryRetry &&
		decision.Action != protocol.RecoveryWait {
		return RetryDecision{}, false
	}
	delayMS := failure.RetryAfterMS
	if delayMS == 0 {
		delayMS = uint64(retryBackoff(retries) / time.Millisecond)
	}
	maxDelayMS := uint64(p.MaxDelay / time.Millisecond)
	if maxDelayMS > 0 && delayMS > maxDelayMS {
		delayMS = maxDelayMS
	}
	delay := time.Duration(delayMS) * time.Millisecond
	now := time.Now()
	if p.Now != nil {
		now = p.Now()
	}
	return RetryDecision{
		Attempt: int(retries + 1), Retry: retries + 1,
		Code: protocol.CodeOf(err), Category: FailureCategory(err, failure.Code),
		Failure: failure, EffectiveDelay: delay, RetryAt: now.Add(delay),
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
