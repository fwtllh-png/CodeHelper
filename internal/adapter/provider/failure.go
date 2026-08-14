package provider

import "fmt"

type FailureCode string

const (
	FailureAuth                  FailureCode = "auth"
	FailureQuota                 FailureCode = "quota"
	FailureRateLimit             FailureCode = "rate_limit"
	FailureContextWindowExceeded FailureCode = "context_window_exceeded"
	FailureInvalidRequest        FailureCode = "invalid_request"
	FailureUnsupportedContent    FailureCode = "unsupported_content"
	FailureServer                FailureCode = "server"
	FailureTransport             FailureCode = "transport"
	FailureTimeout               FailureCode = "timeout"
	FailureAborted               FailureCode = "aborted"
	FailureMalformedResponse     FailureCode = "malformed_response"
	FailureStreamClosed          FailureCode = "stream_closed"
	FailureEmptyResponse         FailureCode = "empty_response"
	FailureUnknown               FailureCode = "unknown"
)

type Failure struct {
	Code         FailureCode `json:"code"`
	Message      string      `json:"message"`
	HTTPStatus   int         `json:"http_status,omitempty"`
	RetryAfterMS uint64      `json:"retry_after_ms,omitempty"`
	RequestID    string      `json:"request_id,omitempty"`
}

func (f *Failure) Error() string {
	if f == nil {
		return "<nil>"
	}
	if f.Message != "" {
		return f.Message
	}
	return fmt.Sprintf("provider failure: %s", f.Code)
}
