package engine

import (
	"maps"
	"slices"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func (e *Engine) bindVerificationEvidence(
	call provider.ToolCall,
	result *tool.Result,
	batchMutated bool,
	mutationRevision uint64,
) {
	if result == nil || result.Outcome == nil || result.Outcome.Facts == nil ||
		result.Execution == nil ||
		!result.Execution.VerificationEvidenceAuthorized {
		return
	}
	source := result.Outcome.Facts.Verification
	if source == nil || source.SchemaVersion != 1 {
		return
	}
	evidence := *source
	evidence.CoveredPaths = append([]string(nil), source.CoveredPaths...)
	result.Metadata = maps.Clone(result.Metadata)
	if result.Metadata == nil {
		result.Metadata = make(map[string]any)
	}
	if batchMutated {
		result.Metadata["verification_evidence_accepted"] = false
		result.Metadata["verification_evidence_rejection"] = "same_batch_mutation"
		return
	}
	switch evidence.Status {
	case verify.StatusPassed, verify.StatusFailed, verify.StatusUnavailable:
	default:
		result.Metadata["verification_evidence_accepted"] = false
		result.Metadata["verification_evidence_rejection"] = "invalid_status"
		return
	}
	if len(evidence.CoveredPaths) == 0 {
		result.Metadata["verification_evidence_accepted"] = false
		result.Metadata["verification_evidence_rejection"] = "missing_covered_paths"
		return
	}
	covered := make([]string, 0, len(evidence.CoveredPaths))
	for _, path := range evidence.CoveredPaths {
		relative, ok := verify.CanonicalEvidencePath(path)
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
	evidence = evidence.Bind(call.ID, e.sessionRevision, mutationRevision)
	result.Outcome.Facts.Verification = &evidence
	result.Metadata["verification_evidence_accepted"] = true
	scope := e.executionScope()
	if scope == nil {
		return
	}
	scope.mu.Lock()
	scope.state.verification = append(scope.state.verification, evidence)
	scope.mu.Unlock()
}

func (e *Engine) qualityVerificationReceipt(
	paths []string,
	mutationRevision uint64,
) (verify.Receipt, []string) {
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		if relative, ok := agentcontext.WorkspaceRelative(
			e.options.Workspace,
			path,
		); ok {
			path = relative
		}
		if !slices.Contains(canonical, path) {
			canonical = append(canonical, path)
		}
	}
	return verify.QualityEvidenceReceipt(
		canonical, mutationRevision, e.verificationEvidence(),
	)
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
