package wire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
)

// openWorkspaceJournal builds the edit-transaction journal and, when it is
// durable, undoes whatever an earlier process left half applied before this one
// takes its first turn: an interrupted turn's writes are exactly the state the
// next turn would otherwise build on.
func openWorkspaceJournal(
	ctx context.Context, workspace string, content contentstore.Store,
	settings config.Journal, workspaceStateRoot, workspaceID string,
	session *Session,
) (*workspacejournal.Manager, error) {
	if !settings.Durable {
		journal, err := workspacejournal.New(workspace, content)
		if err != nil {
			return nil, fmt.Errorf("create workspace journal: %w", err)
		}
		return journal, nil
	}
	if workspaceStateRoot == "" {
		return nil, errors.New(
			"durable workspace journal requires an external Runtime state store",
		)
	}
	legacyPath := filepath.Join(workspace, ".codehelper", "journal")
	if _, err := os.Lstat(legacyPath); err == nil {
		if session.logger != nil {
			session.logger.Warn(
				"legacy workspace journal ignored",
				"path", legacyPath,
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect legacy workspace journal: %w", err)
	}
	journal, err := workspacejournal.Open(
		workspace,
		filepath.Join(workspaceStateRoot, "control", "journal"),
		workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("open durable workspace journal: %w", err)
	}
	session.journal = journal
	if !settings.RecoverOnStart {
		return journal, nil
	}
	recovery, err := journal.Recover(ctx)
	if err != nil {
		// Starting a turn on top of an unrecovered workspace is the failure this
		// whole mechanism exists to prevent, so refuse rather than continue.
		return nil, fmt.Errorf("recover interrupted workspace turns: %w", err)
	}
	session.journalRecovery = recovery
	if session.logger != nil && !recovery.Empty() {
		session.logger.Warn(
			"recovered interrupted workspace turns",
			"rolled_back", len(recovery.RolledBack),
			"kept_committed", len(recovery.Abandoned),
			"retained_drafts", len(recovery.Drafts),
			"skipped_live", len(recovery.Skipped),
		)
	}
	return journal, nil
}
