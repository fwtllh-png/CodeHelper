package automation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/sqlkit"
)

// EnsureSession inserts workspace + session rows when missing so automation
// tools can create schedules without a prior Accept StartTurn.
func (r *Repository) EnsureSession(ctx context.Context, sessionID, workspaceRoot string) error {
	if r.db == nil {
		return errors.New("automation repository database is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	absRoot, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return fmt.Errorf("resolve workspace root: %w", err)
	}
	var existing string
	err = r.db.QueryRowContext(ctx, `SELECT id FROM sessions WHERE id = ?`, sessionID).Scan(&existing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	now := sqlkit.Timestamp(time.Now().UTC())
	return sqlkit.WithTx(ctx, r.db, nil, func(tx *sql.Tx) error {
		var workspaceID string
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM workspaces WHERE root_path = ?`, absRoot,
		).Scan(&workspaceID)
		if errors.Is(err, sql.ErrNoRows) {
			workspaceID = "workspace-" + sessionID
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO workspaces(id, root_path, display_name, created_at, updated_at)
				VALUES (?, ?, 'codehelper', ?, ?)`,
				workspaceID, absRoot, now, now,
			); err != nil {
				return fmt.Errorf("ensure workspace: %w", err)
			}
		} else if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
			VALUES (?, ?, 'open', ?, ?)`,
			sessionID, workspaceID, now, now,
		)
		if err != nil {
			return fmt.Errorf("ensure session: %w", err)
		}
		return nil
	})
}
