package engine

import (
	"encoding/json"
	"maps"
	"slices"
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
	declared, ok := canonicalCompletionPaths(declaration.ChangedPaths)
	if !ok {
		reject("invalid_changed_path")
		return
	}
	observed := changedPaths(e.TurnDiff())
	if !slices.Equal(declared, observed) {
		result.Metadata["completion_expected_paths"] = observed
		reject("changed_paths_mismatch")
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
	for _, callID := range declaration.VerificationCallIDs {
		if _, exists := currentEvidence[callID]; !exists {
			reject("unknown_verification_call_id")
			return
		}
	}
	if len(declaration.VerificationCallIDs) != len(currentEvidence) {
		result.Metadata["completion_expected_verification_call_ids"] =
			sortedMapKeys(currentEvidence)
		reject("verification_call_ids_mismatch")
		return
	}
	declaration.ChangedPaths = observed
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

func canonicalCompletionPaths(paths []string) ([]string, bool) {
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		value, ok := canonicalEvidencePath(path)
		if !ok {
			return nil, false
		}
		if !slices.Contains(canonical, value) {
			canonical = append(canonical, value)
		}
	}
	slices.Sort(canonical)
	return canonical, len(canonical) != 0
}

func (e *Engine) hasCurrentCompletionDeclaration() bool {
	declaration := e.completionDeclaration
	return declaration != nil &&
		declaration.Status == "complete" &&
		len(declaration.PendingActions) == 0 &&
		declaration.MutationRevision == e.mutationRevision &&
		slices.Equal(declaration.ChangedPaths, changedPaths(e.TurnDiff()))
}
