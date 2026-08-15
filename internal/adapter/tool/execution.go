package tool

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"strings"
	"time"
)

// ToolRef is the authority-frozen identity of one executable catalog entry.
// Authority is Registry-private and intentionally omitted from serialized
// receipts.
type ToolRef struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	CatalogID  string `json:"catalog_id"`
	Generation uint64 `json:"generation"`
	Revision   uint64 `json:"revision"`
	Authority  uint64 `json:"-"`
}

func (r ToolRef) Validate() error {
	if strings.TrimSpace(r.Name) == "" ||
		strings.TrimSpace(r.Source) == "" ||
		strings.TrimSpace(r.CatalogID) == "" {
		return errors.New("tool reference identity is incomplete")
	}
	if r.Generation == 0 || r.Revision == 0 || r.Authority == 0 {
		return errors.New("tool reference authority is incomplete")
	}
	return nil
}

func (r ToolRef) Binding() CatalogBinding {
	return CatalogBinding{
		CatalogID: r.CatalogID, Generation: r.Generation,
		Revision: r.Revision, Authority: r.Authority,
	}
}

type InvocationSource string

const (
	InvocationSourceUnknown  InvocationSource = "unknown"
	InvocationSourceModel    InvocationSource = "model"
	InvocationSourceHost     InvocationSource = "host"
	InvocationSourceReplay   InvocationSource = "replay"
	InvocationSourceInternal InvocationSource = "internal"
)

type ExecutionDisposition string

const (
	DispositionAbortImmediately ExecutionDisposition = "abort_immediately"
	DispositionWaitForTeardown  ExecutionDisposition = "wait_for_teardown"
	DispositionDetached         ExecutionDisposition = "detached"
)

func (d ExecutionDisposition) Valid() bool {
	switch d {
	case DispositionAbortImmediately, DispositionWaitForTeardown, DispositionDetached:
		return true
	default:
		return false
	}
}

// DispositionProvider lets typed and lifecycle-aware executors declare how
// Runtime cancellation must treat their work.
type DispositionProvider interface {
	ExecutionDisposition() ExecutionDisposition
}

func DispositionFor(executor Executor) ExecutionDisposition {
	if provider, ok := executor.(DispositionProvider); ok {
		if disposition := provider.ExecutionDisposition(); disposition.Valid() {
			return disposition
		}
	}
	return DispositionWaitForTeardown
}

// PreparedInvocation is immutable after Guard preparation. Replacement
// arguments always produce a new value and repeat policy authorization.
type PreparedInvocation struct {
	Identity    InvocationIdentity   `json:"identity"`
	CallID      string               `json:"call_id"`
	Tool        string               `json:"tool"`
	Ref         ToolRef              `json:"tool_ref"`
	Arguments   json.RawMessage      `json:"arguments"`
	Resources   []Resource           `json:"resources"`
	Descriptor  Descriptor           `json:"descriptor"`
	Source      InvocationSource     `json:"source"`
	Disposition ExecutionDisposition `json:"disposition"`
}

type OutcomeStatus string

const (
	OutcomeSucceeded OutcomeStatus = "succeeded"
	OutcomeFailed    OutcomeStatus = "failed"
	OutcomeRejected  OutcomeStatus = "rejected"
	OutcomeCanceled  OutcomeStatus = "canceled"
)

type TerminalOwner string

const (
	TerminalOwnerGuard    TerminalOwner = "guard"
	TerminalOwnerExecutor TerminalOwner = "executor"
)

type NetworkTarget struct {
	Host     string `json:"host"`
	Protocol string `json:"protocol"`
}

// SecuritySignal carries policy-relevant output outside arbitrary Metadata.
type SecuritySignal struct {
	SandboxDenied bool           `json:"sandbox_denied,omitempty"`
	EgressDenied  *NetworkTarget `json:"egress_denied,omitempty"`
}

// Outcome is the typed, non-model execution projection. Result remains the
// model projection while hooks and telemetry can consume these explicit facts.
type Outcome struct {
	Status    OutcomeStatus   `json:"status"`
	Security  *SecuritySignal `json:"security,omitempty"`
	Hook      any             `json:"hook,omitempty"`
	Telemetry map[string]any  `json:"telemetry,omitempty"`
}

func OutcomeFromResult(result Result) Outcome {
	status := OutcomeSucceeded
	if result.IsError {
		status = OutcomeFailed
	}
	return Outcome{Status: status}
}

func CloneOutcome(source *Outcome) *Outcome {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Telemetry = maps.Clone(source.Telemetry)
	if source.Security != nil {
		security := *source.Security
		if source.Security.EgressDenied != nil {
			egress := *source.Security.EgressDenied
			security.EgressDenied = &egress
		}
		cloned.Security = &security
	}
	return &cloned
}

type AttemptReceipt struct {
	Sequence         uint32        `json:"sequence"`
	Sandbox          string        `json:"sandbox"`
	Status           OutcomeStatus `json:"status"`
	TerminalOwner    TerminalOwner `json:"terminal_owner"`
	Reason           string        `json:"reason,omitempty"`
	StartedAt        time.Time     `json:"started_at"`
	CompletedAt      time.Time     `json:"completed_at"`
	DurationMS       int64         `json:"duration_ms"`
	Teardown         time.Duration `json:"teardown,omitempty"`
	TeardownMS       int64         `json:"teardown_ms,omitempty"`
	TeardownTimedOut bool          `json:"teardown_timed_out,omitempty"`
}

type ExecutionReceipt struct {
	Tool             ToolRef              `json:"tool"`
	Source           InvocationSource     `json:"source"`
	Disposition      ExecutionDisposition `json:"disposition"`
	Attempts         []AttemptReceipt     `json:"attempts"`
	ApprovalWait     time.Duration        `json:"approval_wait,omitempty"`
	DispatchWait     time.Duration        `json:"dispatch_wait,omitempty"`
	ClaimWait        time.Duration        `json:"claim_wait,omitempty"`
	TerminalStatus   OutcomeStatus        `json:"terminal_status"`
	TerminalOwner    TerminalOwner        `json:"terminal_owner"`
	Teardown         time.Duration        `json:"teardown,omitempty"`
	TeardownMS       int64                `json:"teardown_ms,omitempty"`
	TeardownTimedOut bool                 `json:"teardown_timed_out,omitempty"`
}

func CloneExecutionReceipt(source *ExecutionReceipt) *ExecutionReceipt {
	if source == nil {
		return nil
	}
	cloned := *source
	cloned.Attempts = append([]AttemptReceipt(nil), source.Attempts...)
	return &cloned
}

// OutcomeExecutor is the preferred core boundary. Legacy Executor values are
// adapted by Registry until their package is migrated.
type OutcomeExecutor interface {
	Executor
	ExecuteOutcome(context.Context, json.RawMessage) (Result, Outcome, error)
}

type invocationSourceKey struct{}

func WithInvocationSource(ctx context.Context, source InvocationSource) context.Context {
	if !validInvocationSource(source) {
		source = InvocationSourceUnknown
	}
	return context.WithValue(ctx, invocationSourceKey{}, source)
}

func InvocationSourceFrom(ctx context.Context) InvocationSource {
	if ctx == nil {
		return InvocationSourceUnknown
	}
	source, _ := ctx.Value(invocationSourceKey{}).(InvocationSource)
	if !validInvocationSource(source) {
		return InvocationSourceUnknown
	}
	return source
}

func validInvocationSource(source InvocationSource) bool {
	switch source {
	case InvocationSourceUnknown, InvocationSourceModel, InvocationSourceHost,
		InvocationSourceReplay, InvocationSourceInternal:
		return true
	default:
		return false
	}
}

type ExecutionAdmission func(
	context.Context,
	ParallelPolicy,
) (release func(), err error)

type executionAdmissionKey struct{}

func WithExecutionAdmission(ctx context.Context, admission ExecutionAdmission) context.Context {
	if admission == nil {
		return ctx
	}
	return context.WithValue(ctx, executionAdmissionKey{}, admission)
}

func AdmitExecution(
	ctx context.Context,
	policy ParallelPolicy,
) (release func(), err error) {
	if ctx == nil {
		return func() {}, nil
	}
	admission, _ := ctx.Value(executionAdmissionKey{}).(ExecutionAdmission)
	if admission == nil {
		return func() {}, nil
	}
	return admission(ctx, policy)
}

type TeardownReport struct {
	Duration time.Duration
	TimedOut bool
}

type teardownObserverKey struct{}

func WithTeardownObserver(
	ctx context.Context,
	observe func(TeardownReport),
) context.Context {
	if observe == nil {
		return ctx
	}
	return context.WithValue(ctx, teardownObserverKey{}, observe)
}

func ReportTeardown(ctx context.Context, report TeardownReport) {
	if ctx == nil {
		return
	}
	observe, _ := ctx.Value(teardownObserverKey{}).(func(TeardownReport))
	if observe != nil {
		observe(report)
	}
}
