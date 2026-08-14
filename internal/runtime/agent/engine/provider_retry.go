package engine

import (
	"context"
	"errors"
	"io"
	"syscall"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const providerRetryPolicyRevision = "provider-retry/v1"

func (e *Engine) providerRetry(
	err error,
	meaningful bool,
	retries uint32,
	contextChanged bool,
) (ProviderRetry, bool) {
	failure := providerFailure(err, meaningful)
	limit := e.options.MaxRetries
	if limit < 1 && protocol.IsRetryable(err) {
		limit = 1
	}
	retryable := false
	switch failure.Code {
	case provider.FailureRateLimit,
		provider.FailureServer,
		provider.FailureTransport:
		retryable = !meaningful && int(retries) < limit
	case provider.FailureStreamClosed:
		retryable = !meaningful && int(retries) < limit
	case provider.FailureTimeout:
		retryable = int(retries) < limit
	case provider.FailureContextWindowExceeded:
		retryable = contextChanged && retries < uint32(max(1, limit))
	case provider.FailureEmptyResponse:
		retryable = retries < 1
	case provider.FailureUnknown:
		retryable = int(retries) < limit
	}
	if !retryable {
		return ProviderRetry{}, false
	}
	delayMS := failure.RetryAfterMS
	maxDelayMS := uint64(e.options.MaxRetryDelay / time.Millisecond)
	if maxDelayMS > 0 && delayMS > maxDelayMS {
		delayMS = maxDelayMS
	}
	delay := time.Duration(delayMS) * time.Millisecond
	now := e.options.Now()
	return ProviderRetry{
		Attempt:        int(retries + 1),
		Retry:          retries + 1,
		Code:           protocol.CodeOf(err),
		Category:       providerFailureCategory(err, failure.Code),
		Failure:        failure,
		EffectiveDelay: delay,
		RetryAt:        now.Add(delay),
		PolicyRevision: providerRetryPolicyRevision,
	}, true
}

func (e *Engine) recoverContextOverflow(
	err error,
	meaningful bool,
	history *[]provider.Message,
	input contextstore.Snapshot,
	outputReserve uint64,
	send func(State, Event) error,
) (bool, error) {
	if meaningful ||
		providerFailure(err, meaningful).Code !=
			provider.FailureContextWindowExceeded {
		return false, nil
	}
	receipt := e.compactHistoryWithPolicy(
		history,
		true,
		true,
		input,
		outputReserve,
	)
	if receipt == nil || receipt.RetainedTokens >= receipt.OriginalTokens {
		return false, nil
	}
	receipt.Phase = CompactionPhaseMidTurn
	if err := send(Compacting, Event{Compaction: receipt}); err != nil {
		return false, err
	}
	return true, nil
}

func providerFailure(err error, meaningful bool) provider.Failure {
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
	case errors.Is(err, syscall.ECONNRESET),
		errors.Is(err, syscall.EPIPE):
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

func providerFailureCategory(err error, code provider.FailureCode) string {
	category := "provider_unavailable"
	switch {
	case errors.Is(err, syscall.ECONNRESET):
		category = "connection_reset"
	case errors.Is(err, syscall.EPIPE):
		category = "broken_pipe"
	case errors.Is(err, io.ErrUnexpectedEOF):
		category = "unexpected_eof"
	case errors.Is(err, context.DeadlineExceeded):
		category = "deadline_exceeded"
	case code != "":
		category = string(code)
	}
	return category
}
