package engine

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

type TurnDiffEntry = turnkernel.TurnDiffEntry

// TurnDiff returns the net file-tool changes recorded for the active/last turn (N18).
func (e *Engine) TurnDiff() []TurnDiffEntry {
	scope := e.currentScope()
	if scope == nil || scope.state.diff == nil {
		return nil
	}
	return scope.state.diff.Snapshot()
}

// RollbackConflicts describes the paths an automatic rollback of the last turn
// could not restore. They are the turn's real residue: the workspace holds
// changes nobody accepted, so the receipt must name them instead of burying the
// count in an error string.
func (e *Engine) RollbackConflicts() []string {
	if e == nil {
		return nil
	}
	scope := e.currentScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return append([]string(nil), scope.state.rollback...)
}

func (e *Engine) recordRollbackConflicts(receipt workspacejournal.Receipt) {
	if e == nil || len(receipt.Conflicts) == 0 {
		return
	}
	scope := e.executionScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	for _, conflict := range receipt.Conflicts {
		scope.state.rollback = append(scope.state.rollback, fmt.Sprintf(
			"workspace rollback could not restore %s: %s", conflict.Path, conflict.Reason,
		))
	}
}

// FormatTurnDiff renders the turn-diff tracker, or empty when nothing was recorded.
func (e *Engine) FormatTurnDiff() string {
	scope := e.currentScope()
	if scope == nil || scope.state.diff == nil {
		return ""
	}
	return scope.state.diff.Format()
}
