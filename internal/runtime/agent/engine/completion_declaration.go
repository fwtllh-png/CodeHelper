package engine

import (
	"slices"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func (e *Engine) completionCandidate(
	call provider.ToolCall,
	result tool.Result,
	batchMutated bool,
	batchSize int,
	mutationRevision uint64,
) turnkernel.CompletionCandidate {
	if submitted, _ := result.Metadata["submitted_plan"].(bool); submitted &&
		!result.IsError &&
		e.options.Security.PlanningSnapshot().PlanApproval ==
			string(policy.PlanApprovalManual) {
		return turnkernel.CompletionCandidate{
			DeclarationValid: true,
			Status:           "complete",
			Summary:          "Plan submitted for review.",
			CompletionCall:   call.ID,
			BatchMutated:     batchMutated,
			BatchSize:        batchSize,
		}
	}
	evidenceInputs := e.verificationEvidence()
	currentEvidence := make(map[string]struct{}, len(evidenceInputs))
	for _, evidence := range evidenceInputs {
		if evidence.Status == verify.StatusPassed &&
			evidence.MutationRevision == mutationRevision &&
			evidence.CallID != "" {
			currentEvidence[evidence.CallID] = struct{}{}
		}
	}
	return turnkernel.NewCompletionCandidate(
		call,
		result,
		batchMutated,
		batchSize,
		sortedMapKeys(currentEvidence),
	)
}

func bindCompletionDecision(
	result *tool.Result,
	decision turnkernel.CompletionDecision,
) {
	turnkernel.BindCompletionDecision(result, decision)
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	slices.Sort(keys)
	return keys
}
