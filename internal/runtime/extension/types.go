// Package extension defines the runtime-owned contract for typed extensions.
// Implementations live in adapters; hosts only observe protocol projections.
package extension

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type ID string

var idPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

type FailurePolicy string

const (
	FailureFailClosed FailurePolicy = "fail_closed"
	FailureIsolate    FailurePolicy = "isolate"
	FailureIgnore     FailurePolicy = "ignore"
)

type Budget struct {
	Timeout    time.Duration
	MaxOutputs int
}

type Descriptor struct {
	ID            ID
	Version       string
	FailurePolicy FailurePolicy
	Budget        Budget
}

func (d Descriptor) Validate() error {
	if !idPattern.MatchString(string(d.ID)) {
		return errors.New("extension ID is invalid")
	}
	if strings.TrimSpace(d.Version) == "" {
		return errors.New("extension version is required")
	}
	switch d.FailurePolicy {
	case FailureFailClosed, FailureIsolate, FailureIgnore:
	default:
		return fmt.Errorf("extension %q failure policy is invalid", d.ID)
	}
	if d.Budget.Timeout <= 0 {
		return fmt.Errorf("extension %q timeout must be positive", d.ID)
	}
	if d.Budget.MaxOutputs <= 0 {
		return fmt.Errorf("extension %q output budget must be positive", d.ID)
	}
	return nil
}

type Extension interface {
	Descriptor() Descriptor
}

type ContributorKind string

const (
	KindThreadLifecycle ContributorKind = "thread_lifecycle"
	KindTurnLifecycle   ContributorKind = "turn_lifecycle"
	KindContext         ContributorKind = "context"
	KindTool            ContributorKind = "tool"
	KindMCP             ContributorKind = "mcp"
)

type OutcomeStatus string

const (
	OutcomeSucceeded OutcomeStatus = "succeeded"
	OutcomeSkipped   OutcomeStatus = "skipped"
	OutcomeFailed    OutcomeStatus = "failed"
)

type Outcome struct {
	Status  OutcomeStatus
	Code    string
	Message string
}

func Success() Outcome {
	return Outcome{Status: OutcomeSucceeded}
}

func Skip(code, message string) Outcome {
	return Outcome{Status: OutcomeSkipped, Code: code, Message: message}
}

func Failure(code string, err error) Outcome {
	message := ""
	if err != nil {
		message = err.Error()
	}
	return Outcome{Status: OutcomeFailed, Code: code, Message: message}
}

func (o Outcome) Validate() error {
	switch o.Status {
	case OutcomeSucceeded:
		if o.Code != "" || o.Message != "" {
			return errors.New("successful extension outcome contains failure details")
		}
	case OutcomeSkipped:
		if strings.TrimSpace(o.Code) == "" {
			return errors.New("skipped extension outcome requires a code")
		}
	case OutcomeFailed:
		if strings.TrimSpace(o.Code) == "" || strings.TrimSpace(o.Message) == "" {
			return errors.New("failed extension outcome requires code and message")
		}
	default:
		return errors.New("extension outcome status is invalid")
	}
	return nil
}

type Receipt struct {
	Extension   ID
	Kind        ContributorKind
	Status      OutcomeStatus
	Code        string
	Outputs     []string
	StartedAt   time.Time
	CompletedAt time.Time
}

func (r Receipt) Validate(descriptor Descriptor, kind ContributorKind) error {
	if r.Extension != descriptor.ID || r.Kind != kind {
		return errors.New("extension receipt identity does not match invocation")
	}
	if r.StartedAt.IsZero() || r.CompletedAt.IsZero() ||
		r.CompletedAt.Before(r.StartedAt) {
		return errors.New("extension receipt timing is invalid")
	}
	if len(r.Outputs) > descriptor.Budget.MaxOutputs {
		return fmt.Errorf(
			"extension %q produced %d outputs, limit is %d",
			descriptor.ID,
			len(r.Outputs),
			descriptor.Budget.MaxOutputs,
		)
	}
	return nil
}

type Invocation[T any] struct {
	Value   T
	Outcome Outcome
	Receipt Receipt
}
