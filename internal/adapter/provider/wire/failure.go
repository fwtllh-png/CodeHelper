package wire

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func GenericHTTPFailure(failure HTTPFailure) error {
	retryable := RetryableStatus(failure.Status)
	code := protocol.CodeUnavailable
	if failure.Status >= 400 && failure.Status < 500 && failure.Status != http.StatusTooManyRequests {
		code = protocol.CodeInvalidArgument
	}
	message := fmt.Sprintf("provider returned HTTP %d", failure.Status)
	if failure.Body != "" {
		message += ": " + failure.Body
	}
	problem := protocol.NewProblem(code, message, retryable, nil)
	problem.HTTPStatus = failure.Status
	problem.RateLimit = rateLimitMetadata(failure.Header, time.Now())
	return problem
}

func TypedHTTPFailure(
	failure HTTPFailure,
	code provider.FailureCode,
	message, requestID string,
) error {
	base := GenericHTTPFailure(failure).(*protocol.Problem)
	fact := &provider.Failure{
		Code: code, Message: message, HTTPStatus: failure.Status,
		RequestID: requestID,
	}
	if base.RateLimit != nil {
		fact.RetryAfterMS = base.RateLimit.RetryAfterMS
	}
	result := protocol.NewProblem(
		base.Code, base.Message, base.Retryable, fact,
	)
	result.HTTPStatus, result.RateLimit = base.HTTPStatus, base.RateLimit
	return result
}
func FirstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}
func RetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests || status >= 500
}

func IsQuotaFailure(values ...string) bool {
	value := strings.ToLower(strings.Join(values, " "))
	for _, marker := range []string{
		"insufficient_quota",
		"insufficient quota",
		"quota exceeded",
		"billing_hard_limit",
		"billing hard limit",
		"credits exhausted",
		"credit balance",
		"insufficient balance",
		"account balance",
		"out of credits",
		"payment required",
	} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}

func rateLimitMetadata(header http.Header, now time.Time) *protocol.RateLimitMetadata {
	delay, hasDelay := retryAfter(header.Get("Retry-After"), now)
	metadata := &protocol.RateLimitMetadata{
		Limit: FirstHeader(header, "RateLimit-Limit", "X-RateLimit-Limit"),
		Remaining: FirstHeader(
			header, "RateLimit-Remaining", "X-RateLimit-Remaining",
		),
		Reset: FirstHeader(header, "RateLimit-Reset", "X-RateLimit-Reset"),
	}
	if hasDelay {
		metadata.RetryAfterMS = uint64(delay / time.Millisecond)
	}
	if metadata.Limit == "" && metadata.Remaining == "" &&
		metadata.Reset == "" && metadata.RetryAfterMS == 0 {
		return nil
	}
	return metadata
}

func retryAfter(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	return max(at.Sub(now), 0), true
}
