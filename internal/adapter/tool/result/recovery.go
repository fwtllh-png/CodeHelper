package result

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	mcpruntime "github.com/fwtllh-png/QCode/internal/adapter/mcp"
	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	skillruntime "github.com/fwtllh-png/QCode/internal/adapter/skill"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
	"github.com/fwtllh-png/QCode/internal/security/policy"
)

func RecoverResult(
	registry *tool.Registry,
	call provider.ToolCall,
	result tool.Result,
	err error,
) (tool.Result, bool) {
	content, recoverable := RecoverableFailure(err)
	if !recoverable {
		_, descriptor, _, resolveErr := registry.ResolveBound(
			call.Name,
			tool.BindingForCall(call),
		)
		recoverable = resolveErr == nil &&
			(descriptor.Capability == tool.CapabilityRead ||
				result.Content != "" ||
				result.Outcome != nil)
		content = err.Error()
	}
	if !recoverable {
		return result, false
	}
	result.Content = content
	result.IsError = true
	result.Metadata = maps.Clone(result.Metadata)
	if metadata := FailureMetadata(err); metadata != nil {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any, len(metadata))
		}
		maps.Copy(result.Metadata, metadata)
	} else if category := FailureCategory(err); category != "" {
		if result.Metadata == nil {
			result.Metadata = make(map[string]any, 1)
		}
		result.Metadata["error_category"] = category
	}
	category := FailureCategory(err)
	if category == "" {
		category, _ = result.Metadata["error_category"].(string)
	}
	if category == "" {
		category = "tool_execution_failed"
		if result.Metadata == nil {
			result.Metadata = make(map[string]any, 1)
		}
		result.Metadata["error_category"] = category
	}
	tool.EnsureOutcomeFacts(&result).Failure =
		&tool.FailureFact{Category: category}
	return result, true
}

func RecoverableFailure(err error) (string, bool) {
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
	if _, ok := BudgetExhaustionCategory(err); ok {
		return err.Error() +
			"; required_action=report_budget_exhaustion; retry_original=false", true
	}
	if decision, ok := errors.AsType[*policy.DecisionError](err); ok {
		switch decision.Code {
		case "approval_denied":
			return decision.Reason, true
		case "plan_required":
			return decision.Reason + "; call submit_plan with a structured plan, then retry the requested action", true
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

func FailureMetadata(err error) map[string]any {
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
	if category, ok := BudgetExhaustionCategory(err); ok {
		return map[string]any{
			"error_category":  category,
			"required_action": "report_budget_exhaustion",
			"retry_original":  false,
		}
	}
	if decision, ok := errors.AsType[*policy.DecisionError](err); ok {
		switch decision.Code {
		case "plan_required":
			return map[string]any{"error_category": "plan_required", "required_action": "submit_plan", "retry_original": false}
		case "edit_plan_stale":
			return map[string]any{"error_category": "edit_plan_stale", "required_action": "file_read", "retry_original": false, "approval_required": true}
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

func FailureCategory(err error) string {
	if category := tool.ErrorCategory(err); category != "" {
		return category
	}
	if category := mcpruntime.ErrorCategory(err); category != "" {
		return category
	}
	return skillruntime.ErrorCategory(err)
}

func BudgetExhaustionCategory(err error) (string, bool) {
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
