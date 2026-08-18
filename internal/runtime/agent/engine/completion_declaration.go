package engine

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

func (e *Engine) completionCandidate(
	call provider.ToolCall,
	result tool.Result,
	batchMutated bool,
	batchSize int,
	mutationRevision uint64,
) turnkernel.CompletionCandidate {
	candidate := turnkernel.CompletionCandidate{
		CompletionCall: call.ID,
		BatchMutated:   batchMutated,
		BatchSize:      batchSize,
		ToolError:      result.IsError,
	}
	var declaration *tool.CompletionDeclaration
	if result.Outcome != nil && result.Outcome.Facts != nil {
		declaration = result.Outcome.Facts.Completion
	}
	if declaration != nil {
		candidate.DeclarationValid = true
		candidate.Status = declaration.Status
		candidate.Summary = declaration.Summary
		candidate.OutputMode = declaration.OutputMode
		candidate.PendingActions = append(
			[]string(nil),
			declaration.PendingActions...,
		)
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
	candidate.QualityCalls = sortedMapKeys(currentEvidence)
	return candidate
}

func bindCompletionDecision(
	result *tool.Result,
	decision turnkernel.CompletionDecision,
) {
	if result == nil {
		return
	}
	result.Metadata = maps.Clone(result.Metadata)
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	result.Metadata["completion_declaration_accepted"] = decision.Accepted
	result.Metadata["completion_declaration_rejection"] = decision.Reason
	errorDetail := ""
	if result.IsError {
		errorDetail = strings.TrimSpace(result.Content)
		if errorDetail != "" {
			result.Metadata["completion_declaration_error"] = errorDetail
		}
	}
	if decision.Accepted {
		facts := tool.EnsureOutcomeFacts(result)
		if facts.Completion != nil {
			facts.Completion.ChangedPaths = append(
				[]string(nil),
				decision.ChangedPaths...,
			)
			facts.Completion.VerificationCallIDs = append(
				[]string(nil),
				decision.QualityCalls...,
			)
			facts.Completion.MutationRevision = decision.Mutation
			facts.Completion.CallID = decision.CompletionCall
		}
	}
	result.Content = completionDecisionContent(
		decision.Accepted,
		decision.Reason,
		decision.RequiredAction,
		errorDetail,
	)
}

func completionDecisionContent(
	accepted bool,
	reason string,
	requiredAction string,
	errorDetail string,
) string {
	status := "rejected"
	if accepted {
		status = "accepted"
	}
	payload := map[string]any{
		"status":          status,
		"accepted":        accepted,
		"reason":          reason,
		"required_action": requiredAction,
	}
	if errorDetail != "" {
		payload["error_detail"] = errorDetail
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return `{"status":"rejected","accepted":false,"reason":"encode_decision_failed"}`
	}
	return string(content)
}

func sortedMapKeys(values map[string]struct{}) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, value)
	}
	slices.Sort(keys)
	return keys
}
