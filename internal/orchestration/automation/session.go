package automation

import (
	"context"
	"errors"

	sessionstate "github.com/fwtllh-png/CodeHelper/internal/persist/session"
)

// EnsureSession inserts workspace + session rows when missing so automation
// tools can create schedules without a prior Accept StartTurn.
func (r *Repository) EnsureSession(ctx context.Context, sessionID, workspaceRoot string) error {
	if r == nil || r.db == nil {
		return errors.New("automation repository database is required")
	}
	return sessionstate.NewRepository(r.db).EnsureSeed(
		ctx,
		sessionID,
		workspaceRoot,
	)
}
