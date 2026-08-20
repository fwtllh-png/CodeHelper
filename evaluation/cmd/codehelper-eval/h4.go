package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/admission"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/source"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func runH4Admission(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "h4" {
		fmt.Fprintln(stderr, "codehelper-eval: expected admission h4")
		return 2
	}
	flags := flag.NewFlagSet("admission h4", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable H4 admission round id")
	lockPath := flags.String("lock", "", "frozen Harness Lock")
	h3Report := flags.String("h3-report", "", "same-lock H3 qualification")
	h3Release := flags.String(
		"h3-release",
		"",
		"same-lock H3 release evidence",
	)
	h3Package := flags.String("h3-package", "", "same-lock H3 package root")
	catalogPath := flags.String(
		"catalog",
		"evaluation/spec/h4-execution.json",
		"H4 execution catalog",
	)
	evaluationBinary := flags.String(
		"evaluation-binary",
		"bin/codehelper-eval",
		"frozen Evaluation binary",
	)
	runtimeBinary := flags.String(
		"runtime",
		"bin/codehelper",
		"frozen Runtime binary",
	)
	vsixPath := flags.String(
		"vsix",
		"extensions/vscode/dist/codehelper-vscode-0.0.1.vsix",
		"frozen VSIX",
	)
	output := flags.String("output", "", "private H4 output directory")
	development := flags.Bool(
		"development",
		false,
		"run a non-authoritative shortened preflight",
	)
	turnsOverride := flags.Int(
		"turns-override",
		0,
		"development-only Turns per slot",
	)
	intervalOverride := flags.Duration(
		"interval-override",
		0,
		"development-only Turn interval",
	)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 ||
		strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*lockPath) == "" ||
		strings.TrimSpace(*h3Report) == "" ||
		strings.TrimSpace(*h3Release) == "" ||
		strings.TrimSpace(*h3Package) == "" ||
		strings.TrimSpace(*output) == "" ||
		(!*development && (*turnsOverride != 0 || *intervalOverride != 0)) ||
		(*development && (*turnsOverride < 2 || *intervalOverride <= 0)) {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: admission h4 requires --id, --lock, "+
				"--h3-report, --h3-release, --h3-package, and --output; "+
				"overrides require --development",
		)
		return 2
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	resolve := func(path string) string {
		return resolveFromRoot(absoluteRoot, path)
	}
	outputPath := resolve(*output)
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Fprintln(stderr, "codehelper-eval: H4 output already exists")
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := os.MkdirAll(outputPath, 0o700); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	absoluteLockPath := resolve(*lockPath)
	lock, err := freeze.Read(absoluteLockPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if lock.Status != "frozen_qualified" ||
		len(lock.CleanIntegrationRuns) != 3 {
		fmt.Fprintln(stderr, "codehelper-eval: Harness Lock is not frozen qualified")
		return 1
	}
	foundationBundle, err := foundation.Load(
		absoluteRoot,
		"evaluation/spec/foundation.json",
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	catalog, err := admission.LoadH4(absoluteRoot, *catalogPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	evaluationPath := resolve(*evaluationBinary)
	runtimePath := resolve(*runtimeBinary)
	vsix := resolve(*vsixPath)
	identityCheck := func(checkContext context.Context) (string, error) {
		currentSource, resolveErr := source.Resolve(checkContext, absoluteRoot)
		if resolveErr != nil {
			return "", resolveErr
		}
		return freeze.VerifyIdentity(
			absoluteRoot,
			lock,
			currentSource,
			foundationBundle,
			evaluationPath,
			runtimePath,
			vsix,
		)
	}
	h3ReportPath := resolve(*h3Report)
	h3ReleasePath := resolve(*h3Release)
	h3PackageRoot := resolve(*h3Package)
	packageBinary := filepath.Join(h3PackageRoot, "bin", "codehelper")
	resourceBaseline, err := admission.SnapshotH4Resources(
		ctx,
		admission.DefaultTemporaryRoot(),
		packageBinary,
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	var packageDigest, rollbackDigest string
	tasks := []qualification.Task{
		{ID: "identity-before", Check: identityCheck},
		{
			ID: "same-lock-h3",
			Check: func(context.Context) (string, error) {
				report, release, digest, checkErr :=
					admission.ValidateH4Prerequisite(
						h3ReportPath,
						h3ReleasePath,
						h3PackageRoot,
						lock,
					)
				if checkErr == nil {
					packageDigest = digest
				}
				return spec.DigestString(
					report.EvidenceDigest +
						admission.DigestH3Release(release) +
						digest,
				), checkErr
			},
		},
		{
			ID: "prepare-canary-state",
			Check: func(context.Context) (string, error) {
				return admission.PrepareH4State(outputPath)
			},
		},
		{
			ID:        "controlled-canary",
			DependsOn: []string{"same-lock-h3", "prepare-canary-state"},
			Timeout:   30 * time.Minute,
			Check: func(checkContext context.Context) (string, error) {
				evidence, runErr := admission.RunH4Canary(
					checkContext,
					admission.H4CanaryRequest{
						Root:             absoluteRoot,
						Output:           outputPath,
						QualificationID:  *id,
						SourceDigest:     lock.SourceDigest,
						LockIdentity:     lock.LockIdentity,
						PackageBinary:    packageBinary,
						PackageDigest:    packageDigest,
						Policy:           catalog.Canary,
						Development:      *development,
						TurnsOverride:    *turnsOverride,
						IntervalOverride: *intervalOverride,
					},
				)
				return evidence.EvidenceDigest, runErr
			},
		},
		{
			ID: "rollout-stop-drill",
			Check: func(context.Context) (string, error) {
				evidence, checkErr := admission.RunH4StopDrill(
					outputPath,
					catalog.Canary,
				)
				return evidence.EvidenceDigest, checkErr
			},
		},
		{
			ID:      "managed-rollback-drill",
			Timeout: 30 * time.Minute,
			Check: func(checkContext context.Context) (string, error) {
				result, commandErr := runner.RunOwnedCommand(
					checkContext,
					filepath.Join(absoluteRoot, "extensions", "vscode"),
					catalog.Rollback.Command,
					nil,
					8<<20,
				)
				rollbackDigest = spec.DigestString(strings.Join([]string{
					fmt.Sprint(result.ExitCode),
					fmt.Sprint(result.TimedOut),
					result.StdoutDigest,
					result.StderrDigest,
					fmt.Sprint(result.Truncated),
				}, "\x00"))
				if commandErr != nil {
					return rollbackDigest, commandErr
				}
				if result.ExitCode != 0 || result.TimedOut || result.Truncated {
					return rollbackDigest, fmt.Errorf(
						"H4 managed rollback proof is incomplete",
					)
				}
				return rollbackDigest, nil
			},
		},
		{
			ID:        "incident-to-corpus",
			DependsOn: []string{"rollout-stop-drill"},
			Check: func(context.Context) (string, error) {
				stop, readErr := admission.ReadH4StopEvidence(
					filepath.Join(
						outputPath,
						"rollout-stop-evidence.json",
					),
				)
				if readErr != nil {
					return "", readErr
				}
				evidence, closureErr := admission.RunH4IncidentClosure(
					outputPath,
					admission.H4IncidentSourceDigest(stop),
					catalog.Incident,
				)
				return evidence.EvidenceDigest, closureErr
			},
		},
		{
			ID: "h4-aggregate",
			DependsOn: []string{
				"same-lock-h3",
				"controlled-canary",
				"rollout-stop-drill",
				"managed-rollback-drill",
				"incident-to-corpus",
			},
			Check: func(context.Context) (string, error) {
				evidence, aggregateErr := admission.AggregateH4(
					admission.H4AdmissionRequest{
						Output:          outputPath,
						QualificationID: *id,
						Lock:            lock,
						H3ReportPath:    h3ReportPath,
						H3ReleasePath:   h3ReleasePath,
						H3PackageRoot:   h3PackageRoot,
						RollbackDigest:  rollbackDigest,
						Catalog:         catalog,
						Development:     *development,
					},
				)
				return admission.DigestH4Admission(evidence), aggregateErr
			},
		},
		{
			ID: "owned-resource-cleanup",
			Check: func(checkContext context.Context) (string, error) {
				after, snapshotErr := admission.SnapshotH4Resources(
					checkContext,
					admission.DefaultTemporaryRoot(),
					packageBinary,
				)
				if snapshotErr != nil {
					return "", snapshotErr
				}
				return admission.VerifyResourceCleanup(resourceBaseline, after)
			},
		},
		{ID: "identity-after", Check: identityCheck},
	}
	report, err := qualification.Run(ctx, qualification.Request{
		ID:               *id,
		Kind:             "canary",
		Root:             absoluteRoot,
		FoundationDigest: lock.FoundationDigest,
		SourceDigest:     lock.SourceDigest,
		RuntimeDigest:    lock.RuntimeDigest,
		VSIXDigest:       lock.VSIXDigest,
		LockIdentity:     lock.LockIdentity,
		Tasks:            tasks,
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := qualification.Write(outputPath, report); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	summary, _ := json.Marshal(struct {
		ID          string      `json:"id"`
		Status      spec.Status `json:"status"`
		Development bool        `json:"development"`
		Total       int         `json:"total"`
		Passed      int         `json:"passed"`
		Failed      int         `json:"failed"`
		Invalid     int         `json:"invalid"`
		Output      string      `json:"output"`
	}{
		ID:          *id,
		Status:      report.Status,
		Development: *development,
		Total:       report.Scheduled,
		Passed:      report.Passed,
		Failed:      report.Failed,
		Invalid:     report.Invalid,
		Output:      outputPath,
	})
	fmt.Fprintln(stdout, "H4_RESULT="+string(summary))
	if report.Status != spec.StatusPassed {
		return 1
	}
	return 0
}
