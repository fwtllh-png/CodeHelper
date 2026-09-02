package engine

import (
	"slices"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/observability/verify"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
)

func (e *Engine) completionCandidate(
	call provider.ToolCall,
	result tool.Result,
	batchMutated bool,
	batchSize int,
	mutationRevision uint64,
) turnkernel.CompletionCandidate {
	evidenceInputs := e.verificationEvidence()
	currentEvidence := make(map[string]struct{}, len(evidenceInputs))
	for _, evidence := range evidenceInputs {
		if evidence.Status == verify.StatusPassed &&
			evidence.MutationRevision == mutationRevision &&
			evidence.CallID != "" {
			currentEvidence[evidence.CallID] = struct{}{}
		}
	}
	candidate := turnkernel.NewCompletionCandidate(
		call,
		result,
		batchMutated,
		batchSize,
		sortedMapKeys(currentEvidence),
	)
	e.planMu.Lock()
	for _, step := range e.plan.Steps {
		if !step.Done() {
			candidate.PlanOpenSteps++
		}
	}
	e.planMu.Unlock()
	return candidate
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
