package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func TestRunnerClassifiesCommandOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		action     string
		status     spec.Status
		reasonCode string
		exitCode   int
		hasExit    bool
		timeoutMS  int
	}{
		{
			name: "passed", action: "pass", status: spec.StatusPassed,
			reasonCode: "command_passed", exitCode: 0, hasExit: true, timeoutMS: 10000,
		},
		{
			name: "failed", action: "fail", status: spec.StatusFailed,
			reasonCode: "command_failed", exitCode: 7, hasExit: true, timeoutMS: 10000,
		},
		{
			name: "timeout", action: "sleep", status: spec.StatusFailed,
			reasonCode: "command_timeout", timeoutMS: 20,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scenario := runnerScenario(test.action, test.timeoutMS)
			run := (Runner{Now: fixedClock()}).Run(
				context.Background(),
				runnerRequest(t, scenario, ""),
			)
			if run.Status != test.status ||
				run.Execution.ReasonCode != test.reasonCode {
				t.Fatalf("run = %+v", run)
			}
			if test.hasExit {
				if run.Execution.ExitCode == nil ||
					*run.Execution.ExitCode != test.exitCode {
					t.Fatalf("exit code = %v, want %d", run.Execution.ExitCode, test.exitCode)
				}
			}
			if test.action == "sleep" && !run.Execution.TimedOut {
				t.Fatal("timeout run did not retain TimedOut")
			}
		})
	}
}

func TestRunnerClassifiesSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("signal process-state semantics differ on windows")
	}
	run := (Runner{Now: fixedClock()}).Run(
		context.Background(),
		runnerRequest(t, runnerScenario("signal", 10000), ""),
	)
	if run.Status != spec.StatusFailed ||
		run.Execution.ReasonCode != "command_signaled" ||
		run.Execution.Signal == "" {
		t.Fatalf("signal run = %+v", run)
	}
}

func TestRunnerReportsUnavailablePrerequisite(t *testing.T) {
	scenario := runnerScenario("pass", 10000)
	scenario.Requirements.Commands = []string{"codehelper-command-that-does-not-exist"}
	run := (Runner{Now: fixedClock()}).Run(
		context.Background(),
		runnerRequest(t, scenario, ""),
	)
	if run.Status != spec.StatusUnavailable ||
		run.Execution.ReasonCode != "prerequisite_unavailable" {
		t.Fatalf("unavailable run = %+v", run)
	}
}

func TestRunnerEnforcesSuiteOnlyPrerequisite(t *testing.T) {
	scenario := runnerScenario("pass", 10000)
	request := runnerRequest(t, scenario, "")
	request.Suite.Requirements.Commands = append(
		request.Suite.Requirements.Commands,
		"codehelper-suite-command-that-does-not-exist",
	)
	run := (Runner{Now: fixedClock()}).Run(context.Background(), request)
	if run.Status != spec.StatusUnavailable ||
		run.Execution.ReasonCode != "prerequisite_unavailable" {
		t.Fatalf("suite prerequisite run = %+v", run)
	}
}

func TestRunnerRejectsEmptyAndPreexistingEvidence(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		evidencePath := filepath.Join(t.TempDir(), "evidence.jsonl")
		scenario := runnerScenario("empty-evidence", 10000)
		scenario.RequiredEvidence = append(
			scenario.RequiredEvidence,
			"helper_evidence",
		)
		run := (Runner{Now: fixedClock()}).Run(
			context.Background(),
			runnerRequest(t, scenario, evidencePath),
		)
		if run.Status != spec.StatusInvalid {
			t.Fatalf("empty evidence run = %+v", run)
		}
	})
	t.Run("preexisting", func(t *testing.T) {
		evidencePath := filepath.Join(t.TempDir(), "evidence.jsonl")
		if err := os.WriteFile(evidencePath, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		run := (Runner{Now: fixedClock()}).Run(
			context.Background(),
			runnerRequest(t, runnerScenario("pass", 10000), evidencePath),
		)
		if run.Status != spec.StatusInvalid ||
			run.Execution.ReasonCode != "evidence_preexisting" {
			t.Fatalf("preexisting evidence run = %+v", run)
		}
	})
}

func TestRunnerTimeoutKillsDescendantProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell descendant fixture is Unix-specific")
	}
	marker := filepath.Join(t.TempDir(), "survived")
	release := filepath.Join(t.TempDir(), "release")
	t.Setenv("CODEHELPER_DESCENDANT_MARKER", marker)
	t.Setenv("CODEHELPER_DESCENDANT_RELEASE", release)
	run := (Runner{Now: fixedClock()}).Run(
		context.Background(),
		runnerRequest(t, runnerScenario("descendant", 20), ""),
	)
	if run.Status != spec.StatusFailed || !run.Execution.TimedOut {
		t.Fatalf("descendant timeout run = %+v", run)
	}
	if err := os.WriteFile(release, []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived timeout: %v", err)
	}
}

func TestRunnerPreservesValidEvidenceBeforePartialTail(t *testing.T) {
	evidencePath := t.TempDir() + "/evidence.jsonl"
	scenario := runnerScenario("partial-evidence", 10000)
	scenario.RequiredEvidence = append(
		scenario.RequiredEvidence,
		"helper_evidence",
	)
	run := (Runner{Now: fixedClock()}).Run(
		context.Background(),
		runnerRequest(t, scenario, evidencePath),
	)
	if run.Status != spec.StatusInvalid {
		t.Fatalf("partial evidence status = %s, want invalid", run.Status)
	}
	if len(run.Evidence) != 2 || run.Evidence[1].Kind != "helper_evidence" {
		t.Fatalf("preserved evidence = %+v", run.Evidence)
	}
	if last := run.OracleResults[len(run.OracleResults)-1]; last.ID != "evidence_integrity" ||
		!strings.Contains(last.Summary, "incomplete or invalid") {
		t.Fatalf("evidence oracle = %+v", last)
	}
}

func TestRunnerBoundsCombinedOutput(t *testing.T) {
	scenario := runnerScenario("large-output", 10000)
	scenario.Budgets.MaxOutputBytes = 32
	run := (Runner{Now: fixedClock()}).Run(
		context.Background(),
		runnerRequest(t, scenario, ""),
	)
	if !run.Execution.Truncated ||
		run.Execution.StdoutBytes+run.Execution.StderrBytes <= 32 {
		t.Fatalf(
			"bounded output stdout=%d stderr=%d truncated=%t",
			run.Execution.StdoutBytes,
			run.Execution.StderrBytes,
			run.Execution.Truncated,
		)
	}
	if run.Execution.StdoutDigest == "" || run.Execution.StderrDigest == "" {
		t.Fatal("bounded output digests are missing")
	}
}

func TestRunnerDoesNotPersistRawCommandOutput(t *testing.T) {
	run := (Runner{Now: fixedClock()}).Run(
		context.Background(),
		runnerRequest(t, runnerScenario("secret-output", 10000), ""),
	)
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk_foundation_secret") {
		t.Fatalf("run persisted raw command output: %s", raw)
	}
	if run.Execution.StdoutBytes == 0 ||
		run.Execution.StdoutDigest == spec.DigestString("") {
		t.Fatalf("run did not retain bounded output identity: %+v", run.Execution)
	}
}

func TestHelperProcess(t *testing.T) {
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(os.Args) {
		return
	}
	switch os.Args[separator+1] {
	case "pass":
		fmt.Fprint(os.Stdout, "passed")
	case "fail":
		os.Exit(7)
	case "sleep":
		time.Sleep(2 * time.Second)
	case "signal":
		process, _ := os.FindProcess(os.Getpid())
		_ = process.Kill()
		time.Sleep(time.Second)
	case "partial-evidence":
		path := os.Getenv("CODEHELPER_EVALUATION_EVIDENCE_PATH")
		content := fmt.Sprintf(
			`{"schema_version":2,"run_partition":%q,"run_id":%q,`+
				`"scenario_id":%q,"attempt":1,"producer":"helper",`+
				`"kind":"helper_evidence","digest":"sha256:%s"}`,
			os.Getenv("CODEHELPER_EVALUATION_RUN_PARTITION"),
			os.Getenv("CODEHELPER_EVALUATION_RUN_ID"),
			os.Getenv("CODEHELPER_EVALUATION_SCENARIO_ID"),
			strings.Repeat("a", 64),
		) + "\n{"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			os.Exit(8)
		}
	case "empty-evidence":
		path := os.Getenv("CODEHELPER_EVALUATION_EVIDENCE_PATH")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			os.Exit(8)
		}
	case "descendant":
		marker := os.Getenv("CODEHELPER_DESCENDANT_MARKER")
		release := os.Getenv("CODEHELPER_DESCENDANT_RELEASE")
		process := exec.Command(
			"sh",
			"-c",
			`while test ! -f "$CODEHELPER_DESCENDANT_RELEASE"; do sleep 0.02; done; printf survived > "$CODEHELPER_DESCENDANT_MARKER"`,
		)
		process.Env = append(
			os.Environ(),
			"CODEHELPER_DESCENDANT_MARKER="+marker,
			"CODEHELPER_DESCENDANT_RELEASE="+release,
		)
		if err := process.Start(); err != nil {
			os.Exit(8)
		}
		time.Sleep(2 * time.Second)
	case "large-output":
		fmt.Fprint(os.Stdout, strings.Repeat("o", 64))
		fmt.Fprint(os.Stderr, strings.Repeat("e", 64))
	case "secret-output":
		fmt.Fprint(os.Stdout, "sk_foundation_secret")
	default:
		os.Exit(9)
	}
	os.Exit(0)
}

func runnerScenario(action string, timeoutMS int) spec.Scenario {
	return spec.Scenario{
		SchemaVersion:    spec.SchemaVersion,
		ID:               "runner-test",
		Title:            "Runner test",
		Family:           "runner-test",
		Owner:            "evaluation",
		Risk:             spec.RiskP0,
		Driver:           "command",
		ProviderMode:     "none",
		Workspace:        ".",
		FixtureID:        "runner-test-v2",
		ExpectedFacts:    []string{"command_executed"},
		Oracles:          []string{"command_verification"},
		RequiredEvidence: []string{"command_result"},
		CleanupContract:  "command-process-tree-v2",
		RunPlan: spec.RunPlan{
			Attempts: 1, CollectAllGroup: "runner-test",
		},
		Budgets: spec.Budgets{
			WallTimeMS: timeoutMS, MaxAttempts: 1, MaxOutputBytes: 4096,
		},
		Requirements: spec.Requirements{
			Platforms: []string{runtime.GOOS},
		},
		Execution: spec.ScenarioExecution{
			Command: []string{
				os.Args[0],
				"-test.run=^TestHelperProcess$",
				"--",
				action,
			},
			WorkingDirectory: ".",
		},
	}
}

func runnerRequest(
	t *testing.T,
	scenario spec.Scenario,
	evidencePath string,
) Request {
	t.Helper()
	return Request{
		Root:    t.TempDir(),
		RunID:   "runner-test",
		Variant: "default",
		Attempt: 1,
		Seed:    1,
		Suite: spec.Suite{
			ID: "runner-suite", Risk: spec.RiskP0, Repetitions: 1,
			Requirements: scenario.Requirements,
			Budgets:      scenario.Budgets,
			ReleasePolicy: spec.ReleasePolicy{
				Blocking: true, AllowedStatuses: []spec.Status{spec.StatusPassed},
				MinimumValidRuns: 1,
			},
		},
		Scenario: scenario,
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
		EvidencePath: evidencePath,
	}
}

func fixedClock() func() time.Time {
	return func() time.Time {
		return time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	}
}
