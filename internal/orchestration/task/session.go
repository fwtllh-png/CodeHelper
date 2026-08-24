package task

import (
	"context"
	"errors"

	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
)

// EnsureSession inserts workspace + session rows when missing so task tools can
// enqueue against a process-local or persistent SQLite store without requiring
// a prior Accept StartTurn.
func (r *Repository) EnsureSession(ctx context.Context, sessionID, workspaceRoot string) error {
	if r == nil || r.db == nil {
		return errors.New("task repository database is required")
	}
	return sessionstate.NewRepository(r.db).EnsureSeed(
		ctx,
		sessionID,
		workspaceRoot,
	)
}

// NormalizeWorkspaceRoot returns the identity stored in workspaces.root_path.
// Existing roots resolve symlinks so the scheduler, sandbox and task tools agree
// even when the Host was started through a symlink.
func NormalizeWorkspaceRoot(root string) (string, error) {
	return sessionstate.NormalizeWorkspaceRoot(root)
}
