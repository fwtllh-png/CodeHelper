package engine

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (e *Engine) completionGateRequired(intent protocol.TurnIntent) bool {
	return e.options.RequireCompletionDeclaration &&
		e.strictWorkspaceVerificationRequired(intent)
}

func (e *Engine) strictWorkspaceVerificationRequired(
	intent protocol.TurnIntent,
) bool {
	if protocol.NormalizeTurnIntent(intent) ==
		protocol.TurnIntentWorkspaceChange {
		return true
	}
	return e.options.RequireCompletionDeclaration && len(e.TurnDiff()) != 0
}

func (e *Engine) completionCandidate(
	call provider.ToolCall,
	result tool.Result,
	batchMutated bool,
	batchSize int,
	mutationRevision uint64,
) turnkernel.CompletionCandidate {
	candidate := turnkernel.CompletionCandidate{
		CompletionCall:  call.ID,
		BatchMutated:    batchMutated,
		BatchSize:       batchSize,
		ToolError:       result.IsError,
		QualityRequired: e.qualityEvidenceRequired,
	}
	declaration, ok := decodeCompletionDeclaration(
		result.Metadata[tool.MetadataCompletionDeclaration],
	)
	if ok {
		candidate.DeclarationValid = true
		candidate.Status = declaration.Status
		candidate.Summary = declaration.Summary
		candidate.PendingActions = append(
			[]string(nil),
			declaration.PendingActions...,
		)
	}
	currentEvidence := make(map[string]struct{}, len(e.verificationInputs))
	for _, evidence := range e.verificationInputs {
		if evidence.MutationRevision == mutationRevision && evidence.CallID != "" {
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
	if decision.Accepted {
		declaration, ok := decodeCompletionDeclaration(
			result.Metadata[tool.MetadataCompletionDeclaration],
		)
		if ok {
			declaration.ChangedPaths = append(
				[]string(nil),
				decision.ChangedPaths...,
			)
			declaration.VerificationCallIDs = append(
				[]string(nil),
				decision.QualityCalls...,
			)
			declaration.MutationRevision = decision.Mutation
			declaration.CallID = decision.CompletionCall
			result.Metadata[tool.MetadataCompletionDeclaration] = declaration
		}
	}
	result.Content = completionDecisionContent(
		decision.Accepted,
		decision.Reason,
		decision.RequiredAction,
	)
}

func completionDecisionContent(
	accepted bool,
	reason string,
	requiredAction string,
) string {
	status := "rejected"
	if accepted {
		status = "accepted"
	}
	content, err := json.Marshal(map[string]any{
		"status":          status,
		"accepted":        accepted,
		"reason":          reason,
		"required_action": requiredAction,
	})
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

func decodeCompletionDeclaration(value any) (tool.CompletionDeclaration, bool) {
	if declaration, ok := value.(tool.CompletionDeclaration); ok {
		return declaration, true
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return tool.CompletionDeclaration{}, false
	}
	var declaration tool.CompletionDeclaration
	if err := json.Unmarshal(raw, &declaration); err != nil {
		return tool.CompletionDeclaration{}, false
	}
	return declaration, true
}

func (e *Engine) completionProgressKey(mutationRevision uint64) string {
	callIDs := make([]string, 0, len(e.verificationInputs))
	for _, evidence := range e.verificationInputs {
		if evidence.MutationRevision == mutationRevision && evidence.CallID != "" {
			callIDs = append(callIDs, evidence.CallID)
		}
	}
	slices.Sort(callIDs)
	return strings.Join(append(
		[]string{strconv.FormatUint(mutationRevision, 10)},
		callIDs...,
	), "\x00")
}
