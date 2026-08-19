package toolfailure

import (
	"errors"
	"fmt"
	"strings"

	mcpruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// Recoverable decides whether a tool error is handed back to the model as a
// failed tool result. Recoverable errors are known to have no uncertain side
// effects, so the model can correct the call without duplicating an action.
func Recoverable(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	if hint, ok := tool.RecoveryHintFromError(err); ok &&
		hint.ErrorCategory != "" && hint.RequiredAction != "" {
		content := fmt.Sprintf(
			"%s; required_action=%s; retry_original=%t",
			err.Error(),
			hint.RequiredAction,
			hint.RetryOriginal,
		)
		if hint.Path != "" {
			content += "; path=" + hint.Path
		}
		if hint.FailedChange > 0 {
			content += fmt.Sprintf(
				"; failed_change=%d; match_count=%d",
				hint.FailedChange,
				hint.MatchCount,
			)
		}
		if hint.CurrentExcerpt != "" {
			content += fmt.Sprintf(
				"; current_excerpt_lines=%d-%d:\n%s",
				hint.StartLine,
				hint.EndLine,
				hint.CurrentExcerpt,
			)
		}
		if len(hint.CandidatePaths) != 0 {
			content += "; candidate_paths=" +
				strings.Join(hint.CandidatePaths, ",")
		}
		return content, true
	}
	if _, ok := budgetExhaustionCategory(err); ok {
		return err.Error() +
			"; required_action=report_budget_exhaustion; retry_original=false", true
	}
	var decision *policy.DecisionError
	if errors.As(err, &decision) {
		switch decision.Code {
		case "approval_denied":
			return decision.Reason, true
		case "edit_plan_stale":
			return decision.Reason +
				"; re-read the affected file and submit a new edit for approval", true
		}
		return "", false
	}
	switch {
	case errors.Is(err, workspacejournal.ErrUnread):
		return err.Error() +
			"; read the file with file_read before editing it", true
	case errors.Is(err, workspacejournal.ErrStale):
		return err.Error() +
			"; re-read the file to refresh its fingerprint, then retry", true
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
		errors.Is(err, skillruntime.ErrLockDrift),
		errors.Is(err, skillruntime.ErrNotSelected):
		return err.Error(), true
	case errors.Is(err, tool.ErrPrecondition):
		return err.Error() + "; the workspace was not changed", true
	}
	return "", false
}

func Metadata(err error) map[string]any {
	if hint, ok := tool.RecoveryHintFromError(err); ok {
		metadata := map[string]any{
			"error_category":  hint.ErrorCategory,
			"required_action": hint.RequiredAction,
			"path":            hint.Path,
			"retry_original":  hint.RetryOriginal,
		}
		if hint.FailedChange > 0 {
			metadata["failed_change"] = hint.FailedChange
			metadata["match_count"] = hint.MatchCount
		}
		if hint.CurrentExcerpt != "" {
			metadata["start_line"] = hint.StartLine
			metadata["end_line"] = hint.EndLine
			metadata["current_excerpt"] = hint.CurrentExcerpt
		}
		if len(hint.CandidatePaths) != 0 {
			metadata["candidate_paths"] = append(
				[]string(nil),
				hint.CandidatePaths...,
			)
		}
		return metadata
	}
	if category, ok := budgetExhaustionCategory(err); ok {
		return map[string]any{
			"error_category":  category,
			"required_action": "report_budget_exhaustion",
			"retry_original":  false,
		}
	}
	var decision *policy.DecisionError
	if errors.As(err, &decision) &&
		decision.Code == "edit_plan_stale" {
		return map[string]any{
			"error_category":    "edit_plan_stale",
			"required_action":   "file_read",
			"retry_original":    false,
			"approval_required": true,
		}
	}
	var validation *workspacejournal.ReadValidationError
	if !errors.As(err, &validation) {
		return nil
	}
	category := "read_before_edit_required"
	if errors.Is(err, workspacejournal.ErrStale) {
		category = "read_before_edit_stale"
	}
	return map[string]any{
		"error_category":  category,
		"required_action": "file_read",
		"path":            validation.Path,
		"retry_original":  true,
	}
}

func Category(err error) string {
	if category := tool.ErrorCategory(err); category != "" {
		return category
	}
	if category := mcpruntime.ErrorCategory(err); category != "" {
		return category
	}
	return skillruntime.ErrorCategory(err)
}

func budgetExhaustionCategory(err error) (string, bool) {
	var problem *protocol.Problem
	if !errors.As(err, &problem) ||
		problem.Code != protocol.CodeResourceExhausted ||
		problem.Details == nil {
		return "", false
	}
	switch problem.Details.Reason {
	case protocol.ProblemReasonTokenBudgetExhausted,
		protocol.ProblemReasonCostBudgetExhausted:
		return problem.Details.Reason, true
	default:
		return "", false
	}
}
