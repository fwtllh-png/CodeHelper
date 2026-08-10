package engine

import (
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func (e *Engine) clearCompletionDeclaration() {
	e.completionDeclaration = nil
}

func (e *Engine) bindCompletionDeclaration(
	call provider.ToolCall,
	result *tool.Result,
	batchMutated bool,
	batchSize int,
) {
	if call.Name != "turn_complete" || result == nil || result.Metadata == nil {
		return
	}
	result.Metadata = maps.Clone(result.Metadata)
	reject := func(reason string) {
		result.Metadata["completion_declaration_accepted"] = false
		result.Metadata["completion_declaration_rejection"] = reason
	}
	if batchMutated {
		reject("same_batch_mutation")
		return
	}
	if batchSize != 1 {
		reject("declaration_must_be_only_call")
		return
	}
	declaration, ok := decodeCompletionDeclaration(
		result.Metadata[tool.MetadataCompletionDeclaration],
	)
	if !ok {
		reject("invalid_declaration")
		return
	}
	if result.IsError || declaration.Status != "complete" ||
		strings.TrimSpace(declaration.Summary) == "" ||
		len(declaration.PendingActions) != 0 {
		reject("incomplete_declaration")
		return
	}
	observed := changedPaths(e.TurnDiff())
	if len(observed) == 0 {
		reject("no_observed_changes")
		return
	}
	currentEvidence := make(map[string]struct{}, len(e.verificationEvidence))
	for _, evidence := range e.verificationEvidence {
		if evidence.MutationRevision == e.mutationRevision && evidence.CallID != "" {
			currentEvidence[evidence.CallID] = struct{}{}
		}
	}
	if e.qualityEvidenceRequired && len(currentEvidence) == 0 {
		reject("quality_verification_required")
		return
	}
	declaration.ChangedPaths = observed
	declaration.VerificationCallIDs = sortedMapKeys(currentEvidence)
	declaration.MutationRevision = e.mutationRevision
	declaration.CallID = call.ID
	e.completionDeclaration = &declaration
	result.Metadata[tool.MetadataCompletionDeclaration] = declaration
	result.Metadata["completion_declaration_accepted"] = true
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

func (e *Engine) completionProgressKey() string {
	callIDs := make([]string, 0, len(e.verificationEvidence))
	for _, evidence := range e.verificationEvidence {
		if evidence.MutationRevision == e.mutationRevision && evidence.CallID != "" {
			callIDs = append(callIDs, evidence.CallID)
		}
	}
	slices.Sort(callIDs)
	return strings.Join(append(
		[]string{strconv.FormatUint(e.mutationRevision, 10)},
		callIDs...,
	), "\x00")
}

func (e *Engine) hasCurrentCompletionDeclaration() bool {
	declaration := e.completionDeclaration
	return declaration != nil &&
		declaration.Status == "complete" &&
		len(declaration.PendingActions) == 0 &&
		declaration.MutationRevision == e.mutationRevision &&
		slices.Equal(declaration.ChangedPaths, changedPaths(e.TurnDiff()))
}
