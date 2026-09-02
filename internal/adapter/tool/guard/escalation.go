package guard

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

// SandboxMode is the strength requested for a single execution attempt.
type SandboxMode string

const (
	SandboxModeStrong SandboxMode = "strong"
	SandboxModeNone   SandboxMode = "none"
)

// SandboxAttempt describes the sandbox posture for one tool execution try.
type SandboxAttempt struct {
	Mode SandboxMode
}

// EscalationPolicy controls whether a typed sandbox denial may request one
// narrowly scoped permission amendment before retrying in the strong sandbox.
type EscalationPolicy struct {
	EscalateOnFailure bool
}

// DefaultEscalationPolicy enables escalate-on-failure for strong-sandbox tools.
func DefaultEscalationPolicy() EscalationPolicy {
	return EscalationPolicy{EscalateOnFailure: true}
}

const ApprovalReasonAdditionalPermission = "additional_permission"

// ApprovalReasonNetworkHost is set when Guard asks to allow egress to a host
// (Immediate for web/MCP; Deferred reserved for shell managed egress).
const ApprovalReasonNetworkHost = "network_host"

type sandboxAttemptKey struct{}

// WithSandboxAttempt attaches the attempt mode to ctx for tool executors.
func WithSandboxAttempt(ctx context.Context, attempt SandboxAttempt) context.Context {
	if attempt.Mode == "" {
		attempt.Mode = SandboxModeStrong
	}
	return context.WithValue(ctx, sandboxAttemptKey{}, attempt)
}

// SandboxAttemptFromContext returns the attempt attached by Guard, if any.
func SandboxAttemptFromContext(ctx context.Context) (SandboxAttempt, bool) {
	if ctx == nil {
		return SandboxAttempt{}, false
	}
	attempt, ok := ctx.Value(sandboxAttemptKey{}).(SandboxAttempt)
	return attempt, ok
}

// ProcessSandbox keeps process-backed tools on the injected strong backend.
func ProcessSandbox(_ context.Context, backend sandbox.Backend) (sandbox.Backend, bool) {
	return backend, true
}

// IsSandboxDenial reports only structured sandbox denials.
func IsSandboxDenial(err error, outcome tool.Outcome) bool {
	_, ok := SandboxDenial(err, outcome)
	return ok
}

func SandboxDenial(err error, outcome tool.Outcome) (sandbox.Denial, bool) {
	if denial, ok := sandbox.DenialFromError(err); ok {
		return denial, true
	}
	if outcome.Security != nil && outcome.Security.SandboxDenied != nil {
		denial := *outcome.Security.SandboxDenied
		if denial.Validate() == nil {
			return denial, true
		}
	}
	return sandbox.Denial{}, false
}
