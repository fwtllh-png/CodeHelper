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
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/source"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func runH3Admission(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "h3" {
		fmt.Fprintln(stderr, "codehelper-eval: expected admission h3")
		return 2
	}
	flags := flag.NewFlagSet("admission h3", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable H3 admission round id")
	lockPath := flags.String("lock", "", "frozen Harness Lock")
	h1Report := flags.String("h1-report", "", "same-lock H1 qualification")
	h2Report := flags.String("h2-report", "", "same-lock H2 qualification")
	catalogPath := flags.String(
		"catalog",
		"evaluation/spec/h3-execution.json",
		"H3 execution catalog",
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
	runtimeVersion := flags.String(
		"runtime-version",
		"0.0.1",
		"frozen Runtime and package version",
	)
	buildDate := flags.String(
		"build-date",
		"2026-08-19T00:00:00Z",
		"reproducible Runtime build date",
	)
	output := flags.String("output", "", "private H3 output directory")
	development := flags.Bool(
		"development",
		false,
		"run a non-authoritative shortened preflight",
	)
	durationOverride := flags.Duration(
		"duration-override",
		0,
		"development-only Endurance duration",
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
		strings.TrimSpace(*h1Report) == "" ||
		strings.TrimSpace(*h2Report) == "" ||
		strings.TrimSpace(*output) == "" ||
		(!*development &&
			(*durationOverride != 0 || *intervalOverride != 0)) ||
		*development &&
			(*durationOverride <= 0 || *intervalOverride <= 0) {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: admission h3 requires --id, --lock, "+
				"--h1-report, --h2-report, and --output; overrides require --development",
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
		fmt.Fprintln(stderr, "codehelper-eval: H3 output already exists")
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
	catalog, err := admission.LoadH3(absoluteRoot, *catalogPath)
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
	resourceBaseline, err := admission.SnapshotOwnedResources(
		ctx,
		admission.DefaultTemporaryRoot(),
		runtimePath,
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	h1Path := resolve(*h1Report)
	h2Path := resolve(*h2Report)
	releaseReportPath := filepath.Join(outputPath, "test-lanes", "release.json")
	rcReportPath := filepath.Join(
		absoluteRoot,
		"extensions",
		"vscode",
		"dist",
		"rc",
		"report.json",
	)
	packageRoot := filepath.Join(outputPath, "package")
	buildEnv := []string{
		"VERSION=" + *runtimeVersion,
		"COMMIT=" + lock.SourceCommit,
		"BUILD_DATE=" + *buildDate,
		"CODEHELPER_VSCODE_DISABLE_GPU=1",
	}
	releaseEnv := append(
		append([]string(nil), buildEnv...),
		"TEST_LANE_REPORT_DIR="+filepath.Join(outputPath, "test-lanes"),
		"BENCH_REPORT="+filepath.Join(outputPath, "benchmark-v2.json"),
	)
	packageEnv := []string{
		"VERSION=" + *runtimeVersion,
		"RELEASE_STAGE=candidate",
		"PACKAGE_OUT=" + packageRoot,
	}
	enduranceTimeout := time.Duration(catalog.Endurance.DurationSeconds)*time.Second +
		20*time.Minute
	if *development {
		enduranceTimeout = *durationOverride + 10*time.Minute
	}
	tasks := []qualification.Task{
		{ID: "identity-before", Check: identityCheck},
		{
			ID: "same-lock-h1",
			Check: func(context.Context) (string, error) {
				report, checkErr := admission.ValidateH3Prerequisite(
					h1Path, "chaos", lock,
				)
				return report.EvidenceDigest, checkErr
			},
		},
		{
			ID: "same-lock-h2",
			Check: func(context.Context) (string, error) {
				report, checkErr := admission.ValidateH3Prerequisite(
					h2Path, "live", lock,
				)
				return report.EvidenceDigest, checkErr
			},
		},
		{
			ID: "prepare-release-state",
			Check: func(context.Context) (string, error) {
				return admission.PrepareH3ReleaseState(
					absoluteRoot,
					outputPath,
				)
			},
		},
		{
			ID:      "endurance",
			Timeout: enduranceTimeout,
			Check: func(checkContext context.Context) (string, error) {
				evidence, runErr := admission.RunH3Endurance(
					checkContext,
					admission.H3EnduranceRequest{
						Root: absoluteRoot, Output: outputPath,
						QualificationID:  *id,
						SourceDigest:     lock.SourceDigest,
						LockIdentity:     lock.LockIdentity,
						RuntimeDigest:    lock.RuntimeDigest,
						RuntimeBinary:    runtimePath,
						Policy:           catalog.Endurance,
						DurationOverride: *durationOverride,
						IntervalOverride: *intervalOverride,
					},
				)
				return evidence.EvidenceDigest, runErr
			},
		},
		{
			ID:        "release-gate",
			DependsOn: []string{"prepare-release-state"},
			Timeout:   2 * time.Hour,
			Directory: absoluteRoot,
			Command:   append([]string(nil), catalog.Release.ReleaseCommand...),
			Env:       releaseEnv,
		},
		{
			ID:        "release-lane-evidence",
			DependsOn: []string{"release-gate"},
			Check: func(context.Context) (string, error) {
				return admission.ValidateH3ReleaseLane(releaseReportPath)
			},
		},
		{
			ID:        "vscode-rc",
			DependsOn: []string{"prepare-release-state"},
			Timeout:   2 * time.Hour,
			Directory: absoluteRoot,
			Command:   append([]string(nil), catalog.Release.VSCodeRCCommand...),
			Env:       buildEnv,
		},
		{
			ID:        "vscode-rc-evidence",
			DependsOn: []string{"vscode-rc"},
			Check: func(context.Context) (string, error) {
				return admission.ValidateH3VSCodeRC(rcReportPath)
			},
		},
		{
			ID:        "package-candidate",
			DependsOn: []string{"prepare-release-state"},
			Timeout:   30 * time.Minute,
			Directory: absoluteRoot,
			Command:   append([]string(nil), catalog.Release.PackageCommand...),
			Env:       packageEnv,
		},
		{
			ID:        "package-evidence",
			DependsOn: []string{"package-candidate"},
			Check: func(context.Context) (string, error) {
				evidence, checkErr := admission.ValidateH3Package(
					packageRoot,
					*id,
					*runtimeVersion,
					lock.SourceCommit,
					catalog.Release.PackageTargets,
				)
				if checkErr == nil {
					checkErr = admission.WriteH3PackageEvidence(
						outputPath,
						evidence,
					)
				}
				return evidence.EvidenceDigest, checkErr
			},
		},
		{
			ID: "rc-aggregate",
			DependsOn: []string{
				"same-lock-h1",
				"same-lock-h2",
				"endurance",
				"release-lane-evidence",
				"vscode-rc-evidence",
				"package-evidence",
			},
			Check: func(context.Context) (string, error) {
				release, aggregateErr := admission.AggregateH3Release(
					admission.H3ReleaseRequest{
						Root: absoluteRoot, Output: outputPath,
						QualificationID: *id,
						Lock:            lock, LockPath: absoluteLockPath,
						H1ReportPath:      h1Path,
						H2ReportPath:      h2Path,
						ReleaseReportPath: releaseReportPath,
						VSCodeRCPath:      rcReportPath,
						PackageRoot:       packageRoot,
						Catalog:           catalog,
						Development:       *development,
					},
				)
				return admission.DigestH3Release(release), aggregateErr
			},
		},
		{
			ID: "owned-resource-cleanup",
			Check: func(checkContext context.Context) (string, error) {
				after, snapshotErr := admission.SnapshotOwnedResources(
					checkContext,
					admission.DefaultTemporaryRoot(),
					runtimePath,
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
		ID: *id, Kind: "endurance", Root: absoluteRoot,
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
		ID: *id, Status: report.Status, Development: *development,
		Total: report.Scheduled, Passed: report.Passed,
		Failed: report.Failed, Invalid: report.Invalid,
		Output: outputPath,
	})
	fmt.Fprintln(stdout, "H3_RESULT="+string(summary))
	if report.Status != spec.StatusPassed {
		return 1
	}
	return 0
}
