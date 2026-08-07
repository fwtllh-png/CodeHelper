package app

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const SessionIsolationWorktree = "worktree"

var ErrSessionWorkspaceClean = errors.New(
	"Chat worktree has no changes to merge",
)

// SessionWorkspace binds one durable host session to an isolated execution
// root. Root is returned for diagnostics only; callers persist Mode and derive
// the trusted path again when loading the session.
type SessionWorkspace struct {
	Mode string
	Root string
}

// SessionWorkspaceManager owns the lifecycle and merge boundary of isolated
// host sessions. Implementations must register the thread engine before
// Provision or Restore returns.
type SessionWorkspaceManager interface {
	Provision(
		ctx context.Context,
		sessionID string,
		threadID protocol.ThreadID,
	) (SessionWorkspace, error)
	Restore(
		ctx context.Context,
		sessionID string,
		threadID protocol.ThreadID,
	) (SessionWorkspace, error)
	Discard(
		ctx context.Context,
		sessionID string,
		threadID protocol.ThreadID,
	) error
	PlanMerge(
		ctx context.Context,
		sessionID string,
		threadID protocol.ThreadID,
	) (tool.EditPlan, error)
	ApplyMerge(
		ctx context.Context,
		sessionID string,
		threadID protocol.ThreadID,
		planID string,
	) (tool.EditPlan, error)
}
