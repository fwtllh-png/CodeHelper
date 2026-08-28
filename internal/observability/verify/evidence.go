package verify

import (
	"path/filepath"
	"slices"
	"strings"
)

const EvidenceMetadataKey = "verification_evidence"

// Evidence is a successful or failed structured quality command together with
// the exact workspace paths it claims to cover. The engine adds CallID and
// MutationRevision after execution; tool arguments cannot choose either value.
type Evidence struct {
	SchemaVersion     int      `json:"schema_version"`
	Kind              string   `json:"kind"`
	Status            string   `json:"status"`
	CoveredPaths      []string `json:"covered_paths"`
	CommandDigest     string   `json:"command_digest"`
	InputDigest       string   `json:"input_digest,omitempty"`
	CallID            string   `json:"call_id,omitempty"`
	ExitCode          int      `json:"exit_code"`
	WorkspaceRevision uint64   `json:"workspace_revision,omitempty"`
	MutationRevision  uint64   `json:"mutation_revision,omitempty"`
}

func (e Evidence) Bind(callID string, workspaceRevision, mutationRevision uint64) Evidence {
	e.CallID, e.WorkspaceRevision, e.MutationRevision =
		callID, workspaceRevision, mutationRevision
	return e
}

// CanonicalEvidencePath normalizes one workspace-relative evidence path.
func CanonicalEvidencePath(path string) (string, bool) {
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

// QualityEvidenceReceipt reduces current-revision quality evidence into a
// verification verdict. Failed commands remain visible but never cover paths.
func QualityEvidenceReceipt(
	paths []string,
	mutationRevision uint64,
	inputs []Evidence,
) (Receipt, []string) {
	covered := make(map[string]struct{})
	latest := make(map[string]int, len(inputs))
	for index, evidence := range inputs {
		if evidence.MutationRevision == mutationRevision {
			latest[evidence.Kind+"\x00"+evidence.CommandDigest] = index
		}
	}
	keys := make([]string, 0, len(latest))
	for key := range latest {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	checks := make([]Check, 0, len(keys))
	failed := 0
	for _, key := range keys {
		index := latest[key]
		evidence := inputs[index]
		category := ""
		reason := "structured quality evidence covers exact changed paths"
		switch evidence.Status {
		case StatusPassed:
			for _, path := range evidence.CoveredPaths {
				covered[path] = struct{}{}
			}
		case StatusFailed:
			if failedEvidenceSuperseded(evidence, index, latest, inputs) {
				continue
			}
			failed++
			category, reason = "test_failure", "structured quality command failed"
		case StatusUnavailable:
			category = ErrorCategoryDependencyUnavailable
			reason = "structured quality command was unavailable"
		}
		checks = append(checks, Check{
			Name: evidence.Kind, Command: evidence.CommandDigest,
			Reason: reason, Category: category,
			Status: evidence.Status, ExitCode: evidence.ExitCode,
		})
	}
	uncovered := uncoveredEvidencePaths(paths, covered)
	if failed != 0 {
		message := "structured quality command failed"
		if len(uncovered) != 0 {
			message += " and does not cover every changed path"
		}
		return Receipt{
			Scope: ScopeQuality, Status: StatusFailed,
			Checks: checks, Errors: failed, Message: message,
		}, uncovered
	}
	if len(uncovered) != 0 {
		return Receipt{
			Scope: ScopeQuality, Status: StatusUnavailable, Checks: checks,
			Message: "structured quality evidence does not cover every changed path",
		}, uncovered
	}
	return Receipt{
		Scope: ScopeQuality, Status: StatusPassed, Checks: checks,
		Message: "post-mutation structured quality evidence covers every changed path",
	}, nil
}

func failedEvidenceSuperseded(
	failed Evidence,
	index int,
	latest map[string]int,
	inputs []Evidence,
) bool {
	remaining := make(map[string]struct{}, len(failed.CoveredPaths))
	for _, path := range failed.CoveredPaths {
		remaining[path] = struct{}{}
	}
	for _, candidateIndex := range latest {
		candidate := inputs[candidateIndex]
		if candidateIndex <= index || candidate.Kind != failed.Kind ||
			candidate.Status != StatusPassed {
			continue
		}
		for _, path := range candidate.CoveredPaths {
			delete(remaining, path)
		}
	}
	return len(remaining) == 0
}

func uncoveredEvidencePaths(paths []string, covered map[string]struct{}) []string {
	uncovered := make([]string, 0)
	for _, path := range paths {
		canonical, ok := CanonicalEvidencePath(path)
		if !ok {
			uncovered = append(uncovered, path)
		} else if _, ok := covered[canonical]; !ok {
			uncovered = append(uncovered, canonical)
		}
	}
	slices.Sort(uncovered)
	return uncovered
}
