package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func ValidateRun(run RunRecord) error {
	if run.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"run schema_version = %d, want %d",
			run.SchemaVersion,
			SchemaVersion,
		)
	}
	for name, value := range map[string]string{
		"run_id":      run.RunID,
		"suite_id":    run.SuiteID,
		"scenario_id": run.ScenarioID,
		"variant":     run.Variant,
	} {
		if !validID(value) {
			return fmt.Errorf("run %s %q is invalid", name, value)
		}
	}
	if run.Attempt < 1 {
		return errors.New("run attempt must be positive")
	}
	expectedPartition, err := BuildRunPartition(
		run.Source,
		run.Artifacts,
		run.Seed,
		run.Attempt,
	)
	if err != nil {
		return fmt.Errorf("run partition inputs: %w", err)
	}
	if run.RunPartition != expectedPartition {
		return errors.New("run partition does not match source and artifact identities")
	}
	if !run.Status.Valid() {
		return fmt.Errorf("run status %q is invalid", run.Status)
	}
	if run.StartedAt.IsZero() || run.EndedAt.IsZero() ||
		run.EndedAt.Before(run.StartedAt) || run.DurationMS < 0 {
		return errors.New("run timestamps or duration are invalid")
	}
	if strings.TrimSpace(run.Source.Commit) == "" ||
		!digestPattern.MatchString(run.Source.DirtyDigest) {
		return errors.New("run source identity is invalid")
	}
	if strings.TrimSpace(run.Environment.Host) == "" ||
		strings.TrimSpace(run.Environment.OS) == "" ||
		strings.TrimSpace(run.Environment.Arch) == "" ||
		strings.TrimSpace(run.Environment.GoVersion) == "" {
		return errors.New("run environment identity is incomplete")
	}
	if len(run.Execution.Command) == 0 ||
		strings.TrimSpace(run.Execution.Directory) == "" ||
		strings.TrimSpace(run.Execution.ReasonCode) == "" {
		return errors.New("run execution result is incomplete")
	}
	if !digestPattern.MatchString(run.Execution.StdoutDigest) ||
		!digestPattern.MatchString(run.Execution.StderrDigest) ||
		run.Execution.StdoutBytes < 0 || run.Execution.StderrBytes < 0 {
		return errors.New("run execution output identity is invalid")
	}
	if len(run.OracleResults) == 0 {
		return errors.New("run requires at least one oracle result")
	}
	seenOracles := make(map[string]struct{}, len(run.OracleResults))
	for _, oracle := range run.OracleResults {
		if !validID(oracle.ID) || !oracle.Status.Valid() ||
			!oracle.Severity.Valid() || strings.TrimSpace(oracle.Summary) == "" {
			return fmt.Errorf("run oracle %q is invalid", oracle.ID)
		}
		if _, exists := seenOracles[oracle.ID]; exists {
			return fmt.Errorf("duplicate run oracle %q", oracle.ID)
		}
		if oracle.Status == StatusPassed && len(oracle.Evidence) == 0 {
			return fmt.Errorf("passed run oracle %q has no evidence", oracle.ID)
		}
		seenOracles[oracle.ID] = struct{}{}
	}
	seenEvidence := make(map[string]struct{}, len(run.Evidence))
	for _, evidence := range run.Evidence {
		if evidence.SchemaVersion != SchemaVersion ||
			evidence.RunPartition != run.RunPartition ||
			evidence.RunID != run.RunID ||
			evidence.ScenarioID != run.ScenarioID ||
			evidence.Attempt != run.Attempt ||
			!validID(evidence.Producer) ||
			!validID(evidence.Kind) ||
			!digestPattern.MatchString(evidence.Digest) {
			return fmt.Errorf("run evidence %q is invalid", evidence.Kind)
		}
		if evidence.Ref != "" &&
			(filepath.IsAbs(evidence.Ref) ||
				filepath.Clean(evidence.Ref) != filepath.FromSlash(evidence.Ref) ||
				evidence.Ref == ".." ||
				strings.HasPrefix(filepath.ToSlash(evidence.Ref), "../")) {
			return fmt.Errorf("run evidence %q ref is not repository-relative", evidence.Kind)
		}
		if _, exists := seenEvidence[evidence.Kind]; exists {
			return fmt.Errorf("duplicate run evidence kind %q", evidence.Kind)
		}
		seenEvidence[evidence.Kind] = struct{}{}
	}
	for _, oracle := range run.OracleResults {
		for _, kind := range oracle.Evidence {
			if _, exists := seenEvidence[kind]; !exists {
				return fmt.Errorf(
					"run oracle %q references missing evidence %q",
					oracle.ID,
					kind,
				)
			}
		}
	}
	return nil
}
