package tool

import "context"

type invocationIdentityKey struct{}

// InvocationIdentity carries call/thread/turn IDs through tool execution context (N3).
type InvocationIdentity struct {
	CallID   string
	ThreadID string
	TurnID   string
}

// WithInvocationIdentity attaches identity to ctx for downstream executors.
func WithInvocationIdentity(ctx context.Context, id InvocationIdentity) context.Context {
	return context.WithValue(ctx, invocationIdentityKey{}, id)
}

// InvocationIdentityFrom returns identity previously attached to ctx, or zero.
func InvocationIdentityFrom(ctx context.Context) InvocationIdentity {
	id, _ := ctx.Value(invocationIdentityKey{}).(InvocationIdentity)
	return id
}
