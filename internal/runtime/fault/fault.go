package fault

import (
	"context"
	"errors"
	"fmt"
)

type Code string

const Version = 1

const (
	InvalidArgument   Code = "invalid_argument"
	Conflict          Code = "conflict"
	ResourceExhausted Code = "resource_exhausted"
	Unavailable       Code = "unavailable"
	Canceled          Code = "canceled"
	DeadlineExceeded  Code = "deadline_exceeded"
	Internal          Code = "internal"
)

type Origin string

const (
	OriginRuntime      Origin = "runtime"
	OriginProvider     Origin = "provider"
	OriginTool         Origin = "tool"
	OriginHook         Origin = "hook"
	OriginVerification Origin = "verification"
	OriginPersistence  Origin = "persistence"
	OriginProjection   Origin = "projection"
	OriginKernel       Origin = "kernel"
)

type Disposition string

const (
	FailTurn   Disposition = "fail_turn"
	RetryStep  Disposition = "retry_step"
	RetryTurn  Disposition = "retry_turn"
	ResumeTurn Disposition = "resume_turn"
	Reject     Disposition = "reject"
)

type SideEffectState string

const (
	SideEffectNone       SideEffectState = "none"
	SideEffectUnchanged  SideEffectState = "unchanged"
	SideEffectDraft      SideEffectState = "draft"
	SideEffectCommitted  SideEffectState = "committed"
	SideEffectRolledBack SideEffectState = "rolled_back"
	SideEffectUnknown    SideEffectState = "unknown"
)

type Metadata struct {
	Origin         Origin          `json:"origin"`
	Disposition    Disposition     `json:"disposition"`
	SideEffects    SideEffectState `json:"side_effects,omitempty"`
	RecoveryAction string          `json:"recovery_action,omitempty"`
}

type Details struct {
	Reason           string `json:"reason"`
	ResourceID       string `json:"resource_id,omitempty"`
	SessionStatus    string `json:"session_status,omitempty"`
	ExpectedRevision uint64 `json:"expected_revision,omitempty"`
	ActualRevision   uint64 `json:"actual_revision,omitempty"`
}

type RateLimitMetadata struct {
	Limit        string `json:"limit,omitempty"`
	Remaining    string `json:"remaining,omitempty"`
	Reset        string `json:"reset,omitempty"`
	RetryAfterMS uint64 `json:"retry_after_ms,omitempty"`
}

type Problem struct {
	Version    int                `json:"version"`
	Code       Code               `json:"code"`
	Message    string             `json:"message"`
	Retryable  bool               `json:"retryable"`
	HTTPStatus int                `json:"http_status,omitempty"`
	RateLimit  *RateLimitMetadata `json:"rate_limit,omitempty"`
	Details    *Details           `json:"details,omitempty"`
	Fault      *Metadata          `json:"fault,omitempty"`
	cause      error
}

func New(code Code, message string, retryable bool, cause error) *Problem {
	if !ValidCode(code) {
		code, retryable = Internal, false
	}
	problem := &Problem{
		Version: Version, Code: code, Message: message,
		Retryable: retryable, cause: cause,
		Fault: defaultMetadata(code),
	}
	return problem
}

func defaultMetadata(code Code) *Metadata {
	value := Metadata{
		Origin: OriginRuntime, SideEffects: SideEffectUnknown,
	}
	switch code {
	case Internal:
		value.Disposition = FailTurn
	case Unavailable, DeadlineExceeded:
		value.Disposition = ResumeTurn
	case ResourceExhausted:
		value.Disposition = RetryTurn
	default:
		value.Disposition = Reject
	}
	return &value
}

func NewWithDetails(
	code Code,
	message string,
	retryable bool,
	details Details,
	cause error,
) *Problem {
	problem := New(code, message, retryable, cause)
	if details.Reason != "" {
		problem.Details = &details
	}
	return problem
}

func NewClassified(
	code Code,
	message string,
	retryable bool,
	metadata Metadata,
	cause error,
) *Problem {
	problem := New(code, message, retryable, cause)
	problem.Fault = &metadata
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

func CodeOf(err error) Code {
	if err == nil {
		return ""
	}
	var problem *Problem
	if errors.As(err, &problem) {
		if !ValidCode(problem.Code) {
			return Internal
		}
		return problem.Code
	}
	switch {
	case errors.Is(err, context.Canceled):
		return Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return DeadlineExceeded
	default:
		return Internal
	}
}

func ValidCode(code Code) bool {
	switch code {
	case InvalidArgument, Conflict, ResourceExhausted, Unavailable,
		Canceled, DeadlineExceeded, Internal:
		return true
	default:
		return false
	}
}

func Of(err error) *Problem {
	if err == nil {
		return nil
	}
	var problem *Problem
	if errors.As(err, &problem) && problem != nil {
		return Clone(problem)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return NewClassified(
			Canceled, err.Error(), false,
			Metadata{
				Origin: OriginRuntime, Disposition: Reject,
				SideEffects: SideEffectUnknown,
			},
			err,
		)
	case errors.Is(err, context.DeadlineExceeded):
		return NewClassified(
			DeadlineExceeded, err.Error(), true,
			Metadata{
				Origin: OriginRuntime, Disposition: ResumeTurn,
				SideEffects:    SideEffectUnknown,
				RecoveryAction: "resume after the external deadline is extended",
			},
			err,
		)
	default:
		return NewClassified(
			Unavailable, err.Error(), true,
			Metadata{
				Origin: OriginRuntime, Disposition: ResumeTurn,
				SideEffects:    SideEffectUnknown,
				RecoveryAction: "resume from the durable turn state",
			},
			err,
		)
	}
}

func Clone(source *Problem) *Problem {
	if source == nil {
		return nil
	}
	copy := *source
	if source.RateLimit != nil {
		value := *source.RateLimit
		copy.RateLimit = &value
	}
	if source.Details != nil {
		value := *source.Details
		copy.Details = &value
	}
	if source.Fault != nil {
		value := *source.Fault
		copy.Fault = &value
	}
	copy.cause = nil
	return &copy
}

func CloneMetadata(source *Metadata) *Metadata {
	if source == nil {
		return nil
	}
	value := *source
	return &value
}

func DispositionOf(err error) Disposition {
	problem := Of(err)
	if problem == nil || problem.Fault == nil {
		return ""
	}
	return problem.Fault.Disposition
}

func Wrap(
	code Code,
	message string,
	retryable bool,
	cause error,
) error {
	if cause == nil {
		return nil
	}
	return New(
		code,
		fmt.Sprintf("%s: %v", message, cause),
		retryable,
		cause,
	)
}
