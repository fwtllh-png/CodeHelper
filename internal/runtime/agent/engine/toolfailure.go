package engine

import (
	"errors"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// recoverableToolFailure decides whether a tool error is handed back to the
// model as a failed tool result, or aborts the turn.
//
// Recoverable means the model can plausibly fix it by issuing a different call:
// a malformed or unknown call, or an edit that skipped the mandatory read.
// Policy and sandbox rejections are deliberately not recoverable — replaying a
// rejection invites the model to keep probing the permission boundary, which
// would require a per-turn rejection budget before it is safe.
func recoverableToolFailure(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	var decision *policy.DecisionError
	if errors.As(err, &decision) {
		if decision.Code == "approval_denied" {
			return decision.Reason, true
		}
		return "", false
	}
	switch {
	case errors.Is(err, workspacejournal.ErrUnread):
		return err.Error() + "; read the file with file_read before editing it", true
	case errors.Is(err, workspacejournal.ErrStale):
		return err.Error() + "; re-read the file to refresh its fingerprint, then retry", true
	case errors.Is(err, tool.ErrInvalidArguments),
		errors.Is(err, tool.ErrUnknownTool),
		errors.Is(err, tool.ErrToolUnavailable),
		errors.Is(err, tool.ErrCatalogStale),
		errors.Is(err, tool.ErrToolRevoked),
		errors.Is(err, tool.ErrToolLoadFailed),
		errors.Is(err, tool.ErrCatalogLimit),
		errors.Is(err, mcpruntime.ErrServerUnavailable),
		errors.Is(err, mcpruntime.ErrCircuitOpen),
		errors.Is(err, skillruntime.ErrDependencyConflict),
		errors.Is(err, skillruntime.ErrDependencyCycle),
		errors.Is(err, skillruntime.ErrCompatibilityMismatch),
		errors.Is(err, skillruntime.ErrLockDrift):
		return err.Error(), true
	case errors.Is(err, tool.ErrPrecondition):
		// The tool refused before touching anything, so replaying is free: the
		// model can fix the call without the workspace holding a partial change.
		return err.Error() + "; the workspace was not changed", true
	}
	return "", false
}

func toolFailureCategory(err error) string {
	if category := tool.ErrorCategory(err); category != "" {
		return category
	}
	if category := mcpruntime.ErrorCategory(err); category != "" {
		return category
	}
	return skillruntime.ErrorCategory(err)
}
