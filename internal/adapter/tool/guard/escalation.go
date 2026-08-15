package guard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
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

// EscalationPolicy controls whether sandbox denial may retry without sandbox
// after an explicit re-approval. Unsandboxed execution never proceeds silently.
type EscalationPolicy struct {
	EscalateOnFailure bool
}

// DefaultEscalationPolicy enables escalate-on-failure for strong-sandbox tools.
func DefaultEscalationPolicy() EscalationPolicy {
	return EscalationPolicy{EscalateOnFailure: true}
}

// ErrSandboxDenied marks a sandbox denial that may trigger escalation.
var ErrSandboxDenied = errors.New("sandbox denied")

// ApprovalReasonSandboxEscalate is set on ApprovalRequest.Reason when Guard
// asks to retry without sandbox after a strong-sandbox denial.
const ApprovalReasonSandboxEscalate = "sandbox_escalate"

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

// ProcessSandbox selects backend strength from the Guard SandboxAttempt on ctx.
// Unsandboxed retries only happen after an explicit escalate approval.
func ProcessSandbox(ctx context.Context, backend sandbox.Backend) (sandbox.Backend, bool) {
	if attempt, ok := SandboxAttemptFromContext(ctx); ok && attempt.Mode == SandboxModeNone {
		return nil, false
	}
	return backend, true
}

// IsSandboxDenial reports whether an execution failure looks like a sandbox deny.
func IsSandboxDenial(err error, outcome tool.Outcome) bool {
	if err != nil {
		if errors.Is(err, ErrSandboxDenied) {
			return true
		}
	}
	return outcome.Security != nil && outcome.Security.SandboxDenied
}

// MarkSandboxDenial wraps err (or builds one from detail) as ErrSandboxDenied.
func MarkSandboxDenial(err error, detail string) error {
	detail = strings.TrimSpace(detail)
	switch {
	case err == nil && detail == "":
		return ErrSandboxDenied
	case err == nil:
		return fmt.Errorf("%w: %s", ErrSandboxDenied, detail)
	case detail == "":
		return fmt.Errorf("%w: %v", ErrSandboxDenied, err)
	default:
		return fmt.Errorf("%w: %s: %v", ErrSandboxDenied, detail, err)
	}
}

func sandboxNoneResource() tool.Resource {
	return tool.Resource{
		Kind: "sandbox", ID: string(SandboxModeNone), Access: tool.AccessWrite,
	}
}

func withSandboxNoneResource(resources []tool.Resource) []tool.Resource {
	out := append([]tool.Resource(nil), resources...)
	for _, resource := range out {
		if resource.Kind == "sandbox" && resource.ID == string(SandboxModeNone) {
			return out
		}
	}
	return append(out, sandboxNoneResource())
}
