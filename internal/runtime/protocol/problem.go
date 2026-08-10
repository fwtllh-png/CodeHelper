package protocol

import (
	"context"
	"errors"
	"fmt"
)

type ErrorCode string

const ProblemVersion = 1

const (
	CodeInvalidArgument   ErrorCode = "invalid_argument"
	CodeConflict          ErrorCode = "conflict"
	CodeResourceExhausted ErrorCode = "resource_exhausted"
	CodeUnavailable       ErrorCode = "unavailable"
	CodeCanceled          ErrorCode = "canceled"
	CodeDeadlineExceeded  ErrorCode = "deadline_exceeded"
	CodeInternal          ErrorCode = "internal"
)

type Problem struct {
	Version    int                `json:"version"`
	Code       ErrorCode          `json:"code"`
	Message    string             `json:"message"`
	Retryable  bool               `json:"retryable"`
	HTTPStatus int                `json:"http_status,omitempty"`
	RateLimit  *RateLimitMetadata `json:"rate_limit,omitempty"`
	Details    *ProblemDetails    `json:"details,omitempty"`
	cause      error
}

type ProblemDetails struct {
	Reason           string `json:"reason"`
	ResourceID       string `json:"resource_id,omitempty"`
	SessionStatus    string `json:"session_status,omitempty"`
	ExpectedRevision uint64 `json:"expected_revision,omitempty"`
	ActualRevision   uint64 `json:"actual_revision,omitempty"`
}

const (
	ProblemReasonSessionBusy          = "session_busy"
	ProblemReasonStaleProfileRevision = "stale_profile_revision"
	ProblemReasonUnsupported          = "unsupported"
	ProblemReasonWrongSession         = "wrong_session"
)

type RateLimitMetadata struct {
	Limit        string `json:"limit,omitempty"`
	Remaining    string `json:"remaining,omitempty"`
	Reset        string `json:"reset,omitempty"`
	RetryAfterMS uint64 `json:"retry_after_ms,omitempty"`
}

func NewProblem(code ErrorCode, message string, retryable bool, cause error) *Problem {
	if !ValidErrorCode(code) {
		code = CodeInternal
		retryable = false
	}
	return &Problem{
		Version: ProblemVersion, Code: code, Message: message, Retryable: retryable, cause: cause,
	}
}

func NewProblemWithDetails(
	code ErrorCode,
	message string,
	retryable bool,
	details ProblemDetails,
	cause error,
) *Problem {
	problem := NewProblem(code, message, retryable, cause)
	if details.Reason != "" {
		problem.Details = &details
	}
	return problem
}

func (p *Problem) Error() string {
	if p == nil {
		return "<nil>"
	}
	if p.Message != "" {
		return p.Message
	}
	return string(p.Code)
}

func (p *Problem) Unwrap() error {
	if p == nil {
		return nil
	}
	return p.cause
}

func CodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var problem *Problem
	if errors.As(err, &problem) {
		if !ValidErrorCode(problem.Code) {
			return CodeInternal
		}
		return problem.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return CodeDeadlineExceeded
	default:
		return CodeInternal
	}
}

func ValidErrorCode(code ErrorCode) bool {
	switch code {
	case CodeInvalidArgument, CodeConflict, CodeResourceExhausted, CodeUnavailable,
		CodeCanceled, CodeDeadlineExceeded, CodeInternal:
		return true
	default:
		return false
	}
}

func IsCode(err error, code ErrorCode) bool {
	return CodeOf(err) == code
}

func IsRetryable(err error) bool {
	var problem *Problem
	return errors.As(err, &problem) && problem.Retryable
}

func WrapProblem(code ErrorCode, message string, retryable bool, cause error) error {
	if cause == nil {
		return nil
	}
	return NewProblem(code, fmt.Sprintf("%s: %v", message, cause), retryable, cause)
}
