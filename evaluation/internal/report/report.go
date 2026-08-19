package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func Build(runs []spec.RunRecord) (spec.Report, error) {
	if len(runs) == 0 {
		return spec.Report{}, errors.New("evaluation report has an empty denominator")
	}
	ordered := append([]spec.RunRecord(nil), runs...)
	slices.SortFunc(ordered, func(left, right spec.RunRecord) int {
		if comparison := strings.Compare(left.RunID, right.RunID); comparison != 0 {
			return comparison
		}
		if comparison := strings.Compare(left.SuiteID, right.SuiteID); comparison != 0 {
			return comparison
		}
		if comparison := strings.Compare(left.ScenarioID, right.ScenarioID); comparison != 0 {
			return comparison
		}
		if comparison := strings.Compare(left.Variant, right.Variant); comparison != 0 {
			return comparison
		}
		if left.Attempt < right.Attempt {
			return -1
		}
		if left.Attempt > right.Attempt {
			return 1
		}
		return strings.Compare(left.RunPartition, right.RunPartition)
	})
	source := ordered[0].Source
	artifacts := ordered[0].Artifacts
	environment := ordered[0].Environment
	runID := ordered[0].RunID
	suiteID := ordered[0].SuiteID
	scenarioID := ordered[0].ScenarioID
	variant := ordered[0].Variant
	seenAttempts := make(map[int]struct{}, len(ordered))
	result := spec.Report{
		SchemaVersion: spec.SchemaVersion,
		Status:        spec.StatusPassed,
		GeneratedAt:   ordered[0].EndedAt,
		Source:        source,
		Runs:          ordered,
	}
	for _, run := range ordered {
		if err := spec.ValidateRun(run); err != nil {
			return spec.Report{}, fmt.Errorf(
				"validate run %q attempt %d: %w",
				run.RunID,
				run.Attempt,
				err,
			)
		}
		if run.Source != source {
			return spec.Report{}, errors.New(
				"evaluation report cannot combine different source identities",
			)
		}
		if run.Artifacts != artifacts || run.Environment != environment ||
			run.RunID != runID || run.SuiteID != suiteID ||
			run.ScenarioID != scenarioID || run.Variant != variant {
			return spec.Report{}, errors.New(
				"evaluation report cannot combine different run identity partitions",
			)
		}
		if _, exists := seenAttempts[run.Attempt]; exists {
			return spec.Report{}, fmt.Errorf(
				"evaluation report has duplicate attempt %d",
				run.Attempt,
			)
		}
		seenAttempts[run.Attempt] = struct{}{}
		if run.EndedAt.After(result.GeneratedAt) {
			result.GeneratedAt = run.EndedAt
		}
		accumulate(&result.Summary, run)
		result.Status = mergeStatus(result.Status, run.Status)
	}
	if result.Summary.Total == 0 {
		return spec.Report{}, errors.New("evaluation report has an empty denominator")
	}
	return result, nil
}

func MarshalJSON(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func RenderMarkdown(report spec.Report) []byte {
	var output strings.Builder
	output.WriteString("# Production Evaluation Report\n\n")
	output.WriteString("| Field | Value |\n")
	output.WriteString("| --- | --- |\n")
	fmt.Fprintf(&output, "| Status | `%s` |\n", report.Status)
	fmt.Fprintf(&output, "| Source commit | `%s` |\n", report.Source.Commit)
	fmt.Fprintf(&output, "| Dirty | `%t` |\n", report.Source.Dirty)
	fmt.Fprintf(&output, "| Dirty digest | `%s` |\n", report.Source.DirtyDigest)
	fmt.Fprintf(
		&output,
		"| Generated at | `%s` |\n",
		report.GeneratedAt.UTC().Format("2006-01-02T15:04:05Z"),
	)
	fmt.Fprintf(&output, "| Admission allowed | `%t` |\n", report.Admission.Allowed)
	fmt.Fprintf(&output, "| Admission blocking | `%t` |\n\n", report.Admission.Blocking)
	output.WriteString("## Summary\n\n")
	output.WriteString("| Total | Passed | Failed | Unavailable | Not evaluated | Invalid |\n")
	output.WriteString("| ---: | ---: | ---: | ---: | ---: | ---: |\n")
	fmt.Fprintf(
		&output,
		"| %d | %d | %d | %d | %d | %d |\n\n",
		report.Summary.Total,
		report.Summary.Passed,
		report.Summary.Failed,
		report.Summary.Unavailable,
		report.Summary.NotEvaluated,
		report.Summary.Invalid,
	)
	output.WriteString("## Attempts\n\n")
	output.WriteString("| Run | Scenario | Attempt | Status | Duration (ms) | Reason |\n")
	output.WriteString("| --- | --- | ---: | --- | ---: | --- |\n")
	for _, run := range report.Runs {
		fmt.Fprintf(
			&output,
			"| `%s` | `%s` | %d | `%s` | %d | `%s` |\n",
			escapeCell(run.RunID),
			escapeCell(run.ScenarioID),
			run.Attempt,
			run.Status,
			run.DurationMS,
			escapeCell(run.Execution.ReasonCode),
		)
	}
	return []byte(output.String())
}

func Write(directory string, value spec.Report) error {
	if strings.TrimSpace(directory) == "" {
		return errors.New("evaluation report directory is required")
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	reportJSON, err := MarshalJSON(value)
	if err != nil {
		return err
	}
	if err := writeAtomicExclusive(
		filepath.Join(directory, "report.json"),
		reportJSON,
		0o600,
	); err != nil {
		return err
	}
	if err := writeAtomicExclusive(
		filepath.Join(directory, "report.md"),
		RenderMarkdown(value),
		0o600,
	); err != nil {
		return err
	}
	for _, run := range value.Runs {
		encoded, err := MarshalJSON(run)
		if err != nil {
			return err
		}
		name := fmt.Sprintf(
			"run-%s-%s-a%03d-%s.json",
			run.ScenarioID,
			run.Variant,
			run.Attempt,
			strings.TrimPrefix(run.RunPartition, "sha256:")[:12],
		)
		if err := writeAtomicExclusive(
			filepath.Join(directory, name),
			encoded,
			0o600,
		); err != nil {
			return err
		}
	}
	if len(value.Runs) == 1 {
		encoded, err := MarshalJSON(value.Runs[0])
		if err != nil {
			return err
		}
		if err := writeAtomicExclusive(
			filepath.Join(directory, "run.json"),
			encoded,
			0o600,
		); err != nil {
			return err
		}
	}
	return nil
}

func accumulate(summary *spec.Summary, run spec.RunRecord) {
	summary.Total++
	switch run.Status {
	case spec.StatusPassed:
		summary.Passed++
	case spec.StatusFailed:
		summary.Failed++
	case spec.StatusUnavailable:
		summary.Unavailable++
	case spec.StatusNotEvaluated:
		summary.NotEvaluated++
	case spec.StatusInvalid:
		summary.Invalid++
	}
	if run.Attempt == 1 {
		summary.FirstAttemptTotal++
		if run.Status == spec.StatusPassed {
			summary.FirstAttemptPassed++
		}
	} else {
		summary.RecoveredAttemptTotal++
		if run.Status == spec.StatusPassed {
			summary.RecoveredPassed++
		}
	}
}

func mergeStatus(current, next spec.Status) spec.Status {
	if statusRank(next) > statusRank(current) {
		return next
	}
	return current
}

func statusRank(status spec.Status) int {
	switch status {
	case spec.StatusInvalid:
		return 5
	case spec.StatusFailed:
		return 4
	case spec.StatusUnavailable:
		return 3
	case spec.StatusNotEvaluated:
		return 2
	case spec.StatusPassed:
		return 1
	default:
		return 6
	}
}

func writeAtomicExclusive(path string, content []byte, mode os.FileMode) error {
	parent := filepath.Dir(path)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("refuse to overwrite evaluation artifact %q", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(parent, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return err
	}
	if _, err := bytes.NewReader(content).WriteTo(temporary); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, path); err != nil {
		return err
	}
	return os.Remove(temporaryPath)
}

func escapeCell(value string) string {
	value = strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(value, "\n", " ")
}
