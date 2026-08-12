package engine

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
)

func (e *Engine) bindVerificationEvidence(
	call provider.ToolCall,
	result *tool.Result,
	batchMutated bool,
	mutationRevision uint64,
) {
	if result == nil || result.Metadata == nil ||
		(call.Name != "quality_test" && call.Name != "quality_verify") {
		return
	}
	evidence, ok := decodeVerificationEvidence(
		result.Metadata[verify.EvidenceMetadataKey],
	)
	if !ok {
		return
	}
	result.Metadata = maps.Clone(result.Metadata)
	if batchMutated {
		result.Metadata["verification_evidence_accepted"] = false
		result.Metadata["verification_evidence_rejection"] = "same_batch_mutation"
		return
	}
	if result.IsError || evidence.Status != verify.StatusPassed ||
		len(evidence.CoveredPaths) == 0 {
		result.Metadata["verification_evidence_accepted"] = false
		return
	}
	covered := make([]string, 0, len(evidence.CoveredPaths))
	for _, path := range evidence.CoveredPaths {
		relative, ok := canonicalEvidencePath(path)
		if !ok {
			result.Metadata["verification_evidence_accepted"] = false
			result.Metadata["verification_evidence_rejection"] = "invalid_covered_path"
			return
		}
		if !slices.Contains(covered, relative) {
			covered = append(covered, relative)
		}
	}
	slices.Sort(covered)
	evidence.CoveredPaths = covered
	evidence.CallID = call.ID
	evidence.MutationRevision = mutationRevision
	result.Metadata[verify.EvidenceMetadataKey] = evidence
	result.Metadata["verification_evidence_accepted"] = true
	scope := e.executionScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.verification = append(scope.state.verification, evidence)
	scope.mu.Unlock()
}

func decodeVerificationEvidence(value any) (verify.Evidence, bool) {
	if value == nil {
		return verify.Evidence{}, false
	}
	if evidence, ok := value.(verify.Evidence); ok {
		return evidence, evidence.SchemaVersion == 1
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return verify.Evidence{}, false
	}
	var evidence verify.Evidence
	if err := json.Unmarshal(raw, &evidence); err != nil ||
		evidence.SchemaVersion != 1 {
		return verify.Evidence{}, false
	}
	return evidence, true
}

func canonicalEvidencePath(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return "", false
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(clean), true
}

func (e *Engine) qualityVerificationReceipt(
	paths []string,
	mutationRevision uint64,
) (verify.Receipt, []string) {
	covered := make(map[string]struct{})
	evidenceInputs := e.verificationEvidence()
	checks := make([]verify.Check, 0, len(evidenceInputs))
	for _, evidence := range evidenceInputs {
		if evidence.Status != verify.StatusPassed ||
			evidence.MutationRevision != mutationRevision {
			continue
		}
		for _, path := range evidence.CoveredPaths {
			covered[path] = struct{}{}
		}
		checks = append(checks, verify.Check{
			Name: evidence.Kind, Command: evidence.CommandDigest,
			Reason: "structured quality evidence covers exact changed paths",
			Status: verify.StatusPassed, ExitCode: evidence.ExitCode,
		})
	}
	uncovered := make([]string, 0)
	for _, path := range paths {
		canonical, ok := canonicalEvidencePath(path)
		if !ok {
			uncovered = append(uncovered, path)
			continue
		}
		if _, ok := covered[canonical]; !ok {
			uncovered = append(uncovered, canonical)
		}
	}
	slices.Sort(uncovered)
	if len(uncovered) != 0 {
		return verify.Receipt{
			Scope: verify.ScopeQuality, Status: verify.StatusUnavailable,
			Checks:  checks,
			Message: "structured quality evidence does not cover every changed path",
		}, uncovered
	}
	return verify.Receipt{
		Scope: verify.ScopeQuality, Status: verify.StatusPassed, Checks: checks,
		Message: "post-mutation structured quality evidence covers every changed path",
	}, nil
}

func (e *Engine) verificationEvidence() []verify.Evidence {
	scope := e.currentScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	return append([]verify.Evidence(nil), scope.state.verification...)
}
