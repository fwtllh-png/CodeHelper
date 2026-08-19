package report

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func TestBuildRejectsEmptyDenominator(t *testing.T) {
	if _, err := Build(nil); err == nil ||
		!strings.Contains(err.Error(), "empty denominator") {
		t.Fatalf("empty report error = %v", err)
	}
}

func TestReportIsDeterministicAndPreservesFirstAttempt(t *testing.T) {
	first := reportRun("run-test", 1, spec.StatusFailed)
	second := reportRun("run-test", 2, spec.StatusPassed)
	result, err := Build([]spec.RunRecord{second, first})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != spec.StatusFailed ||
		result.Summary.FirstAttemptTotal != 1 ||
		result.Summary.FirstAttemptPassed != 0 ||
		result.Summary.RecoveredPassed != 1 {
		t.Fatalf("report summary = %+v status=%s", result.Summary, result.Status)
	}
	if result.Runs[0].Attempt != 1 || result.Runs[1].Attempt != 2 {
		t.Fatalf("run ordering = %+v", result.Runs)
	}
	firstJSON, err := MarshalJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := MarshalJSON(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("JSON report is not byte-stable")
	}
	if firstMarkdown, secondMarkdown := RenderMarkdown(result), RenderMarkdown(result); string(firstMarkdown) != string(secondMarkdown) {
		t.Fatal("Markdown report is not byte-stable")
	}
}

func TestBuildRejectsMixedSourceIdentities(t *testing.T) {
	first := reportRun("run-test", 1, spec.StatusPassed)
	second := reportRun("run-test", 2, spec.StatusPassed)
	second.Source.DirtyDigest = "sha256:" + strings.Repeat("c", 64)
	second.RunPartition, _ = spec.BuildRunPartition(
		second.Source,
		second.Artifacts,
		second.Seed,
		second.Attempt,
	)
	second.Evidence[0].RunPartition = second.RunPartition
	if _, err := Build([]spec.RunRecord{first, second}); err == nil ||
		!strings.Contains(err.Error(), "different source") {
		t.Fatalf("mixed source error = %v", err)
	}
}

func TestWriteCreatesPrivateAtomicArtifacts(t *testing.T) {
	run := reportRun("run-test", 1, spec.StatusPassed)
	result, err := Build([]spec.RunRecord{run})
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := Write(directory, result); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"report.json", "report.md", "run.json",
	} {
		path := filepath.Join(directory, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	foundAttempt := false
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "run-scenario-default-a001-") {
			foundAttempt = true
		}
	}
	if !foundAttempt {
		t.Fatalf("attempt artifact is missing: %v", entries)
	}
	if err := Write(directory, result); err == nil ||
		!strings.Contains(err.Error(), "refuse to overwrite") {
		t.Fatalf("second write error = %v", err)
	}
}

func TestBuildRejectsDuplicateAttemptAndMixedArtifacts(t *testing.T) {
	first := reportRun("run-test", 1, spec.StatusPassed)
	duplicate := first
	if _, err := Build([]spec.RunRecord{first, duplicate}); err == nil ||
		!strings.Contains(err.Error(), "duplicate attempt") {
		t.Fatalf("duplicate attempt error = %v", err)
	}
	mixed := reportRun("run-test", 2, spec.StatusPassed)
	mixed.Artifacts.HostDigest = spec.DigestString("other-host")
	mixed.RunPartition, _ = spec.BuildRunPartition(
		mixed.Source,
		mixed.Artifacts,
		mixed.Seed,
		mixed.Attempt,
	)
	mixed.Evidence[0].RunPartition = mixed.RunPartition
	if _, err := Build([]spec.RunRecord{first, mixed}); err == nil ||
		!strings.Contains(err.Error(), "run identity partitions") {
		t.Fatalf("mixed artifact error = %v", err)
	}
}

func reportRun(runID string, attempt int, status spec.Status) spec.RunRecord {
	started := time.Date(2026, 8, 19, 0, 0, attempt, 0, time.UTC)
	exitCode := 0
	reason := "command_passed"
	if status == spec.StatusFailed {
		exitCode = 1
		reason = "command_failed"
	}
	run := spec.RunRecord{
		SchemaVersion: spec.SchemaVersion,
		RunID:         runID,
		SuiteID:       "suite",
		ScenarioID:    "scenario",
		Variant:       "default",
		Attempt:       attempt,
		Seed:          int64(attempt),
		Status:        status,
		StartedAt:     started,
		EndedAt:       started.Add(time.Second),
		DurationMS:    1000,
		Source: spec.SourceIdentity{
			Commit: "commit", DirtyDigest: "sha256:" + strings.Repeat("b", 64),
		},
		Artifacts: spec.ArtifactIdentity{
			HarnessDigest:  spec.DigestString("harness"),
			RuntimeDigest:  spec.DigestString("runtime"),
			HostDigest:     spec.DigestString("host"),
			ScenarioDigest: spec.DigestString("scenario"),
			FixtureDigest:  spec.DigestString("fixture"),
			ProviderDigest: spec.DigestString("provider"),
			ModelDigest:    spec.DigestString("model"),
			ConfigDigest:   spec.DigestString("config"),
		},
		Environment: spec.Environment{
			Host: "test", OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(),
		},
		Execution: spec.ExecutionResult{
			Command: []string{"test"}, Directory: ".", ExitCode: &exitCode,
			ReasonCode:   reason,
			StdoutDigest: spec.DigestString("stdout"),
			StderrDigest: spec.DigestString("stderr"),
		},
		OracleResults: []spec.OracleResult{{
			ID: "command_result", Status: status, Severity: spec.RiskP0,
			Summary: "test result", Evidence: []string{"command_result"},
		}},
		Evidence: []spec.EvidenceRecord{{
			SchemaVersion: spec.SchemaVersion,
			RunID:         runID, ScenarioID: "scenario", Attempt: attempt,
			Producer: "runner", Kind: "command_result",
			Digest: spec.DigestString("command-result"),
		}},
	}
	run.RunPartition, _ = spec.BuildRunPartition(
		run.Source,
		run.Artifacts,
		run.Seed,
		run.Attempt,
	)
	run.Evidence[0].RunPartition = run.RunPartition
	return run
}
