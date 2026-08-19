package freeze

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/source"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestHarnessLockRequiresThreeMatchingIntegrationRuns(t *testing.T) {
	root := t.TempDir()
	evaluationBinary := writeFile(t, root, "bin/codehelper-eval", "evaluation")
	runtimeBinary := writeFile(t, root, "bin/codehelper", "runtime")
	vsix := writeVSIX(t, root, "extension.vsix", map[string]string{
		"extension/package.json":      `{"name":"codehelper"}`,
		"extension/dist/extension.js": "production",
	})
	writeFile(t, root, "inputs/fixture.json", `{"fixture":true}`)
	source := spec.SourceIdentity{
		Commit: "commit", Dirty: true, DirtyDigest: spec.DigestString("source"),
	}
	lock, scan, err := BuildCandidate(CandidateOptions{
		Root: root, ID: "harness-q1",
		Source:           source,
		Foundation:       foundation.Bundle{HarnessInputHash: spec.DigestString("foundation")},
		EvaluationBinary: evaluationBinary,
		RuntimeBinary:    runtimeBinary,
		VSIX:             vsix,
		InputRoots:       []string{"inputs"},
		Now: func() time.Time {
			return time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if scan.Status != "passed" || lock.Status != "candidate" {
		t.Fatalf("candidate lock=%+v scan=%+v", lock, scan)
	}
	validateHarnessSchema(t, lock)
	for index := 1; index <= 3; index++ {
		report := matchingReport(lock, index)
		lock, err = AppendIntegrationRun(lock, report)
		if err != nil {
			t.Fatal(err)
		}
	}
	if lock.Status != "frozen_qualified" ||
		len(lock.CleanIntegrationRuns) != 3 {
		t.Fatalf("frozen lock = %+v", lock)
	}
	validateHarnessSchema(t, lock)
}

func validateHarnessSchema(t *testing.T, lock Lock) {
	t.Helper()
	schemaRaw, err := os.ReadFile(filepath.Join(
		"..",
		"..",
		"schema",
		"harness-lock.schema.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var schemaValue any
	if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("harness-lock.schema.json", schemaValue); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("harness-lock.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(lock)
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

func TestHarnessLockRejectsIdentityDriftAndDuplicateRun(t *testing.T) {
	lock := Lock{
		SchemaVersion: SchemaVersion,
		ID:            "harness-q1", Status: "candidate",
		SourceCommit:         "commit",
		SourceDigest:         spec.DigestString("source"),
		FoundationDigest:     spec.DigestString("foundation"),
		HarnessDigest:        spec.DigestString("harness"),
		EvaluationDigest:     spec.DigestString("evaluation"),
		RuntimeDigest:        spec.DigestString("runtime"),
		VSIXDigest:           spec.DigestString("vsix"),
		HostDigest:           spec.DigestString("vsix"),
		ToolchainDigest:      spec.DigestString("toolchain"),
		ProductionScanDigest: spec.DigestString("scan"),
		InputRoots:           []string{"input"},
		Inputs:               []InputDigest{{Path: "input", Digest: spec.DigestString("input")}},
		CreatedAt:            time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC),
	}
	lock.HarnessDigest = digestInputs(
		lock.FoundationDigest,
		lock.InputRoots,
		lock.Inputs,
	)
	lock.LockIdentity = identityDigest(lock)
	report := matchingReport(lock, 1)
	drifted := report
	drifted.RuntimeDigest = spec.DigestString("other")
	if _, err := AppendIntegrationRun(lock, drifted); err == nil {
		t.Fatal("Harness Lock accepted Runtime drift")
	}
	next, err := AppendIntegrationRun(lock, report)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendIntegrationRun(next, report); err == nil {
		t.Fatal("Harness Lock accepted duplicate Integration evidence")
	}
}

func TestVerifyIdentityAllowsGovernancePublicationAndRejectsInputSetDrift(
	t *testing.T,
) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "evaluation@example.invalid")
	runGit(t, root, "config", "user.name", "Evaluation Test")
	evaluationBinary := writeFile(t, root, "bin/codehelper-eval", "evaluation")
	runtimeBinary := writeFile(t, root, "bin/codehelper", "runtime")
	vsix := writeVSIX(t, root, "extension.vsix", map[string]string{
		"extension/package.json": `{"name":"codehelper"}`,
	})
	writeFile(t, root, "inputs/fixture.json", `{"fixture":true}`)
	writeFile(t, root, "governance/status.md", "pending\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "base")

	initialSource, err := source.Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	bundle := foundation.Bundle{
		HarnessInputHash: spec.DigestString("foundation"),
	}
	lock, _, err := BuildCandidate(CandidateOptions{
		Root: root, ID: "harness-q1",
		Source: initialSource, Foundation: bundle,
		EvaluationBinary: evaluationBinary,
		RuntimeBinary:    runtimeBinary,
		VSIX:             vsix,
		InputRoots:       []string{"inputs"},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "governance/status.md", "qualified\n")
	runGit(t, root, "add", "governance/status.md")
	runGit(t, root, "commit", "-m", "publish qualification")
	publishedSource, err := source.Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if publishedSource.Commit == lock.SourceCommit ||
		publishedSource.DirtyDigest == lock.SourceDigest {
		t.Fatal("governance publication did not change Source provenance")
	}
	if _, err := VerifyIdentity(
		root,
		lock,
		publishedSource,
		bundle,
		evaluationBinary,
		runtimeBinary,
		vsix,
	); err != nil {
		t.Fatalf("governance publication invalidated Harness: %v", err)
	}

	writeFile(t, root, "inputs/new.json", "{}")
	if _, err := VerifyIdentity(
		root,
		lock,
		publishedSource,
		bundle,
		evaluationBinary,
		runtimeBinary,
		vsix,
	); err == nil {
		t.Fatal("Harness accepted a new input file")
	}
	if err := os.Remove(filepath.Join(root, "inputs/new.json")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, root, "inputs/fixture.json", `{"fixture":false}`)
	if _, err := VerifyIdentity(
		root,
		lock,
		publishedSource,
		bundle,
		evaluationBinary,
		runtimeBinary,
		vsix,
	); err == nil {
		t.Fatal("Harness accepted modified input content")
	}
}

func TestProductionScanRejectsEvaluationControls(t *testing.T) {
	root := t.TempDir()
	runtimeBinary := writeFile(
		t,
		root,
		"codehelper",
		"CODEHELPER_EVALUATION_CONTROL",
	)
	vsix := writeVSIX(t, root, "extension.vsix", map[string]string{
		"extension/package.json": `{"name":"codehelper"}`,
	})
	result, err := ScanProductionArtifacts(runtimeBinary, vsix)
	if err == nil || result.Status != "failed" || len(result.Forbidden) == 0 {
		t.Fatalf("production scan result=%+v err=%v", result, err)
	}
}

func matchingReport(lock Lock, index int) qualification.Report {
	report := qualification.Report{
		SchemaVersion:    qualification.SchemaVersion,
		ID:               "integration-0" + string(rune('0'+index)),
		Kind:             "integration",
		FoundationDigest: lock.FoundationDigest,
		SourceDigest:     lock.SourceDigest,
		RuntimeDigest:    lock.RuntimeDigest,
		VSIXDigest:       lock.VSIXDigest,
		LockIdentity:     lock.LockIdentity,
		Status:           spec.StatusPassed,
		StartedAt:        time.Date(2026, 8, 19, 0, 0, index, 0, time.UTC),
		EndedAt:          time.Date(2026, 8, 19, 0, 0, index+1, 0, time.UTC),
		Scheduled:        1, Settled: 1, Passed: 1,
		Results: []qualification.TaskResult{{
			ID: "integration", Status: spec.StatusPassed,
			EvidenceDigest:        spec.DigestString("task"),
			CleanupEvidenceDigest: spec.DigestString("cleanup-not-required"),
			ReasonCode:            "passed",
		}},
	}
	report.EvidenceDigest = qualification.Digest(report)
	return report
}

func writeFile(t *testing.T, root, relative, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}

func writeVSIX(
	t *testing.T,
	root, name string,
	files map[string]string,
) string {
	t.Helper()
	path := filepath.Join(root, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
