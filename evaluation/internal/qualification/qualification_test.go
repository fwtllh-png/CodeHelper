package qualification

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCollectAllContinuesIndependentWorkAndBlocksDependents(t *testing.T) {
	executed := make(map[string]bool)
	request := testRequest([]Task{
		{
			ID: "first-fails",
			Check: func(context.Context) (string, error) {
				executed["first-fails"] = true
				return "", errors.New("injected failure")
			},
		},
		{
			ID: "independent-passes",
			Check: func(context.Context) (string, error) {
				executed["independent-passes"] = true
				return spec.DigestString("independent"), nil
			},
		},
		{
			ID:        "dependent-blocked",
			DependsOn: []string{"first-fails"},
			Check: func(context.Context) (string, error) {
				executed["dependent-blocked"] = true
				return spec.DigestString("unexpected"), nil
			},
		},
	})
	report, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != spec.StatusFailed ||
		report.Scheduled != 3 || report.Settled != 3 ||
		report.Passed != 1 || report.Failed != 1 ||
		report.NotEvaluated != 1 {
		t.Fatalf("Qualification report = %+v", report)
	}
	if !executed["first-fails"] || !executed["independent-passes"] ||
		executed["dependent-blocked"] {
		t.Fatalf("executed tasks = %+v", executed)
	}
}

func TestDiscoveryRequestUsesCollectAllSettlement(t *testing.T) {
	secondExecuted := false
	request := testRequest([]Task{
		{
			ID: "scenario-fails",
			Check: func(context.Context) (string, error) {
				return "", errors.New("product failure")
			},
		},
		{
			ID: "later-scenario-passes",
			Check: func(context.Context) (string, error) {
				secondExecuted = true
				return spec.DigestString("later evidence"), nil
			},
		},
	})
	request.Kind = "discovery"
	report, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !secondExecuted || report.Settled != 2 ||
		report.Failed != 1 || report.Passed != 1 {
		t.Fatalf("Discovery collect-all report = %+v", report)
	}
}

func TestChaosRequestUsesCollectAllSettlement(t *testing.T) {
	secondExecuted := false
	request := testRequest([]Task{
		{
			ID: "kill-point-fails",
			Check: func(context.Context) (string, error) {
				return "", errors.New("injected crash failure")
			},
		},
		{
			ID: "later-kill-point-passes",
			Check: func(context.Context) (string, error) {
				secondExecuted = true
				return spec.DigestString("later chaos evidence"), nil
			},
		},
	})
	request.Kind = "chaos"
	report, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if !secondExecuted || report.Settled != 2 ||
		report.Failed != 1 || report.Passed != 1 {
		t.Fatalf("Chaos collect-all report = %+v", report)
	}
}

func TestQualificationRejectsForwardDependency(t *testing.T) {
	request := testRequest([]Task{
		{ID: "first", DependsOn: []string{"second"}},
		{ID: "second"},
	})
	if _, err := Run(context.Background(), request); err == nil {
		t.Fatal("Qualification accepted a forward dependency")
	}
}

func TestQualificationWritesPrivateImmutableReport(t *testing.T) {
	request := testRequest([]Task{{
		ID: "passes",
		Check: func(context.Context) (string, error) {
			return spec.DigestString("passes"), nil
		},
	}})
	report, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	if err := Write(directory, report); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "qualification.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("Qualification mode = %o", info.Mode().Perm())
	}
	if err := Write(directory, report); err == nil {
		t.Fatal("Qualification report was overwritten")
	}
}

func TestQualificationSchemaMatchesReport(t *testing.T) {
	request := testRequest([]Task{{
		ID: "passes",
		Check: func(context.Context) (string, error) {
			return spec.DigestString("passes"), nil
		},
	}})
	report, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	schemaRaw, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"schema",
		"qualification.schema.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var schemaValue any
	if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("qualification.schema.json", schemaValue); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("qualification.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(value); err != nil {
		t.Fatal(err)
	}
}

func TestQualificationBindsCompleteCleanupEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cleanup.json")
	report := cleanupReportFixture(0)
	request := testRequest([]Task{{
		ID:            "cleanup-command",
		Command:       cleanupHelperCommand(),
		Env:           cleanupHelperEnvironment(t, path, report),
		CleanupReport: path,
	}})
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	task := result.Results[0]
	if task.Status != spec.StatusPassed ||
		!task.CleanupRequired ||
		task.OwnedResources != 2 ||
		task.CleanupOutstanding != 0 ||
		!digestValid(task.CleanupEvidenceDigest) {
		t.Fatalf("cleanup result = %+v", task)
	}
}

func TestQualificationRejectsOutstandingCleanupEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cleanup.json")
	report := cleanupReportFixture(1)
	request := testRequest([]Task{{
		ID:            "cleanup-command",
		Command:       cleanupHelperCommand(),
		Env:           cleanupHelperEnvironment(t, path, report),
		CleanupReport: path,
	}})
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	task := result.Results[0]
	if task.Status != spec.StatusInvalid ||
		task.ReasonCode != "cleanup_outstanding" ||
		task.CleanupOutstanding != 1 {
		t.Fatalf("cleanup result = %+v", task)
	}
}

func TestQualificationRejectsMissingCleanupEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cleanup.json")
	request := testRequest([]Task{{
		ID:            "cleanup-command",
		Command:       cleanupHelperCommand(),
		CleanupReport: path,
	}})
	result, err := Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	task := result.Results[0]
	if task.Status != spec.StatusInvalid ||
		task.ReasonCode != "cleanup_evidence_invalid" {
		t.Fatalf("cleanup result = %+v", task)
	}
}

func TestQualificationCleanupEvidenceHelper(t *testing.T) {
	if os.Getenv("QUALIFICATION_CLEANUP_HELPER") != "1" {
		return
	}
	if err := os.WriteFile(
		os.Getenv("QUALIFICATION_CLEANUP_PATH"),
		[]byte(os.Getenv("QUALIFICATION_CLEANUP_CONTENT")),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func cleanupHelperCommand() []string {
	return []string{
		os.Args[0],
		"-test.run=^TestQualificationCleanupEvidenceHelper$",
	}
}

func cleanupHelperEnvironment(
	t *testing.T,
	path string,
	report cleanupReport,
) []string {
	t.Helper()
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	return []string{
		"QUALIFICATION_CLEANUP_HELPER=1",
		"QUALIFICATION_CLEANUP_PATH=" + path,
		"QUALIFICATION_CLEANUP_CONTENT=" + string(raw),
	}
}

func cleanupReportFixture(outstanding int) cleanupReport {
	resources := []cleanupResource{
		{
			Kind: "process", Owner: "fixture-process",
			Identity: spec.DigestString("process"), PID: 123,
			CleanupAttempted: true, CleanupSucceeded: true,
		},
		{
			Kind: "temporary_directory", Owner: "fixture-directory",
			Identity:         spec.DigestString("directory"),
			CleanupAttempted: true, CleanupSucceeded: outstanding == 0,
		},
	}
	return cleanupReport{
		SchemaVersion: 1, QualificationID: "qualification-test",
		TaskID: "cleanup-command", Resources: resources,
		Outstanding: outstanding,
	}
}

func testRequest(tasks []Task) Request {
	digest := spec.DigestString("identity")
	return Request{
		ID:               "qualification-test",
		Kind:             "foundation_epoch",
		Root:             ".",
		FoundationDigest: digest,
		SourceDigest:     digest,
		RuntimeDigest:    digest,
		VSIXDigest:       digest,
		LockIdentity:     digest,
		Tasks:            tasks,
		Now: func() time.Time {
			return time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
		},
	}
}
