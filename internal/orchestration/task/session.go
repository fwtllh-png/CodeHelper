package task

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/sqlkit"
)

// EnsureSession inserts workspace + session rows when missing so task tools can
// enqueue against a process-local or persistent SQLite store without requiring
// a prior Accept StartTurn.
func (r *Repository) EnsureSession(ctx context.Context, sessionID, workspaceRoot string) error {
	if r.db == nil {
		return errors.New("task repository database is required")
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session id is required")
	}
	absRoot, err := NormalizeWorkspaceRoot(workspaceRoot)
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

// NormalizeWorkspaceRoot returns the identity stored in workspaces.root_path.
// Existing roots resolve symlinks so the scheduler, sandbox and task tools agree
// even when the Host was started through a symlink.
func NormalizeWorkspaceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(absolute)
	resolved, err := filepath.EvalSymlinks(clean)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return clean, nil
	}
	return "", err
}
