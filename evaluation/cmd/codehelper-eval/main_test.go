package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
)

func TestContractCheckCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"contract",
			"check",
			"--root",
			filepath.Join("..", "..", ".."),
		},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "3 suites, 3 scenarios") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestUnknownCommandIsUsageFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{"unknown"},
		&stdout,
		&stderr,
	)
	if code != 2 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestFoundationCheckCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"foundation",
			"check",
			"--root",
			filepath.Join("..", "..", ".."),
		},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("code = %d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"oracles":9`) ||
		!strings.Contains(stdout.String(), `"mutation_runs":7`) ||
		!strings.Contains(stdout.String(), `"production_replay":["provider","runtime","host"]`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestImpactSelectCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"impact",
			"select",
			"--root",
			filepath.Join("..", "..", ".."),
			"--path",
			"internal/security/policy/policy.go",
		},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "security-guard") ||
		!strings.Contains(stdout.String(), "tool-consequential-once") {
		t.Fatalf("impact output = %s", stdout.String())
	}
}

func TestSourceIdentityCommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		context.Background(),
		[]string{
			"source",
			"identity",
			"--root",
			filepath.Join("..", "..", ".."),
		},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var identity struct {
		Commit      string `json:"commit"`
		DirtyDigest string `json:"dirty_digest"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Commit == "" ||
		!strings.HasPrefix(identity.DirtyDigest, "sha256:") {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestQ1VSCodeTaskRequiresIdentityBoundCleanupEvidence(t *testing.T) {
	output := t.TempDir()
	tasks := q1IntegrationTasks(
		".",
		freeze.Lock{},
		"codehelper-eval",
		"codehelper",
		"codehelper.vsix",
		nil,
		"qualification-test",
		output,
	)
	for _, task := range tasks {
		if task.ID != "vscode-runtime" {
			continue
		}
		wantReport := filepath.Join(output, "vscode-runtime-cleanup.json")
		if task.CleanupReport != wantReport {
			t.Fatalf("CleanupReport = %q, want %q", task.CleanupReport, wantReport)
		}
		environment := strings.Join(task.Env, "\n")
		for _, expected := range []string{
			"CODEHELPER_Q1_CLEANUP_REPORT=" + wantReport,
			"CODEHELPER_Q1_QUALIFICATION_ID=qualification-test",
			"CODEHELPER_Q1_TASK_ID=vscode-runtime",
		} {
			if !strings.Contains(environment, expected) {
				t.Fatalf("task environment %q does not contain %q", environment, expected)
			}
		}
		return
	}
	t.Fatal("vscode-runtime task is missing")
}

func TestQ1InputRootsCoverExecutableInputsAndExcludeGovernanceOutputs(
	t *testing.T,
) {
	roots := q1InputRoots()
	for _, required := range []string{
		"cmd",
		"internal",
		"go.mod",
		"go.sum",
		"evaluation/internal",
		"evaluation/schema",
		"extensions/vscode/src",
		"scripts",
		"testdata",
	} {
		if !slices.Contains(roots, required) {
			t.Fatalf("Q1 input roots do not contain %q", required)
		}
	}
	for _, governance := range []string{
		"docs",
		"evaluation/assessments",
		"evaluation/README.md",
		"evaluation/README.zh-CN.md",
	} {
		if slices.Contains(roots, governance) {
			t.Fatalf("Q1 input roots contain governance output %q", governance)
		}
	}
}
