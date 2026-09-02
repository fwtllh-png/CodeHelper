package app

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func (r *Runtime) HistoryWorkspaceRoot() string {
	if r == nil {
		return ""
	}
	return r.workspaceRoot
}

func (r *Runtime) HistoryThreadIDs(
	ctx context.Context,
	sessionID string,
) ([]protocol.ThreadID, error) {
	return r.sessionLifecycle.ThreadIDs(ctx, sessionID)
}

func (r *Runtime) HistoryReadFence(
	ctx context.Context,
	sessionID string,
) (protocol.SessionReadFence, error) {
	store, ok := r.sessionLifecycle.(sessionPresentationStore)
	if !ok {
		return protocol.SessionReadFence{}, runtimeProblem(
			protocol.CodeUnavailable,
			"transactional session presentation is unavailable",
			nil,
		)
	}
	return store.PresentationReadFence(ctx, sessionID)
}
