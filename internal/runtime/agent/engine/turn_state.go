package engine

import (
	"fmt"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
)

// TurnDiff returns the net file-tool changes recorded for the active/last turn (N18).
func (e *Engine) TurnDiff() []TurnDiffEntry {
	if e == nil || e.turnDiff == nil {
		return nil
	}
	return e.turnDiff.Snapshot()
}

// RollbackConflicts describes the paths an automatic rollback of the last turn
// could not restore. They are the turn's real residue: the workspace holds
// changes nobody accepted, so the receipt must name them instead of burying the
// count in an error string.
func (e *Engine) RollbackConflicts() []string {
	if e == nil {
		return nil
	}
	e.rollbackMu.Lock()
	defer e.rollbackMu.Unlock()
	return append([]string(nil), e.rollbackConflicts...)
}

func (e *Engine) recordRollbackConflicts(receipt workspacejournal.Receipt) {
	if e == nil || len(receipt.Conflicts) == 0 {
		return
	}
	e.rollbackMu.Lock()
	defer e.rollbackMu.Unlock()
	for _, conflict := range receipt.Conflicts {
		e.rollbackConflicts = append(e.rollbackConflicts, fmt.Sprintf(
			"workspace rollback could not restore %s: %s", conflict.Path, conflict.Reason,
		))
	}
}

func (e *Engine) resetRollbackConflicts() {
	if e == nil {
		return
	}
	e.rollbackMu.Lock()
	defer e.rollbackMu.Unlock()
	e.rollbackConflicts = nil
}

// FormatTurnDiff renders the turn-diff tracker, or empty when nothing was recorded.
func (e *Engine) FormatTurnDiff() string {
	if e == nil || e.turnDiff == nil {
		return ""
	}
	return e.turnDiff.Format()
}
