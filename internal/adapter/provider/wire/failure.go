package wire

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
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
	problem.RateLimit = rateLimitMetadata(failure.Header)
	return problem
}
func RetryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests || status >= 500
}
func rateLimitMetadata(header http.Header) *protocol.RateLimitMetadata {
	retryDelay, hasRetryAfter := retryAfter(header.Get("Retry-After"), time.Now())
	metadata := &protocol.RateLimitMetadata{
		Limit:     firstHeader(header, "RateLimit-Limit", "X-RateLimit-Limit"),
		Remaining: firstHeader(header, "RateLimit-Remaining", "X-RateLimit-Remaining"),
		Reset:     firstHeader(header, "RateLimit-Reset", "X-RateLimit-Reset"),
	}
	if hasRetryAfter {
		metadata.RetryAfterMS = uint64(retryDelay / time.Millisecond)
	}
	if metadata.Limit == "" && metadata.Remaining == "" && metadata.Reset == "" && metadata.RetryAfterMS == 0 {
		return nil
	}
	return metadata
}
func retryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second, true
	}
	at, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := at.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}
func firstHeader(header http.Header, names ...string) string {
	for _, name := range names {
		if value := header.Get(name); value != "" {
			return value
		}
	}
	return ""
}
