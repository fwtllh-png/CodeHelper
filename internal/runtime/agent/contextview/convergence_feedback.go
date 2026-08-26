package contextview

import (
	"encoding/json"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

type StepBudgetEvidence struct {
	CompletedSamples       uint32 `json:"completed_samples"`
	SamplesWithoutProgress uint32 `json:"samples_without_progress"`
	SuccessfulToolCalls    int    `json:"successful_tool_calls"`
	FailedToolCalls        int    `json:"failed_tool_calls"`
	ChangedPaths           int    `json:"changed_paths"`
	VerificationStatus     string `json:"verification_status"`
	SuppressedFailedCalls  int    `json:"suppressed_failed_calls"`
}

func StepBudgetWarningRemaining(maxSteps, step int) int {
	if maxSteps <= 0 || step < 0 || step >= maxSteps {
		return 0
	}
	remaining := max(1, maxSteps/4)
	if step != maxSteps-remaining {
		return 0
	}
	return remaining
}

func StepBudgetFeedback(
	turn uint64,
	used int,
	maxSteps int,
	state turnkernel.State,
	suppressedFailedCalls int,
) provider.Message {
	evidence := stepBudgetEvidence(state, suppressedFailedCalls)
	encoded, _ := json.Marshal(evidence)
	message := provider.TextMessage(provider.RoleUser, fmt.Sprintf(
		"[step_budget]\nused_steps=%d\nmax_steps=%d\nremaining_steps=%d\n"+
			"hard_limit=true\nexecution_evidence=%s\n"+
			"Prioritize the requested deliverable now. Stop broad exploration, "+
			"reuse recorded reads and validation evidence, and do not repeat a "+
			"failed call whose result says retry_original=false. Finish the "+
			"smallest coherent verified result, and call turn_complete. If "+
			"required work cannot fit, call turn_complete with status=incomplete "+
			"and concrete pending_actions instead of waiting for forced termination.",
		used,
		maxSteps,
		maxSteps-used,
		encoded,
	))
	message.Turn = turn
	return message
}

func stepBudgetEvidence(
	state turnkernel.State,
	suppressedFailedCalls int,
) StepBudgetEvidence {
	evidence := StepBudgetEvidence{
		SamplesWithoutProgress: state.Progress.NoProgressSamples,
		VerificationStatus:     string(state.Verification.Status),
		SuppressedFailedCalls:  suppressedFailedCalls,
	}
	for _, sample := range state.SampleLedger {
		if sample.Status == turnkernel.SampleCompleted {
			evidence.CompletedSamples++
		}
	}
	for _, result := range state.ClosedCalls {
		if result.IsError {
			evidence.FailedToolCalls++
		} else {
			evidence.SuccessfulToolCalls++
		}
	}
	changed := make(map[string]struct{}, len(state.Changes))
	for _, change := range state.Changes {
		changed[change.Path] = struct{}{}
	}
	evidence.ChangedPaths = len(changed)
	return evidence
}
