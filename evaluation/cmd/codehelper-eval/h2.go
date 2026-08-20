package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/admission"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/source"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func runAdmission(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "codehelper-eval: admission requires h1, h2, h3, or h4")
		return 2
	}
	switch args[0] {
	case "h1":
		return runH1Admission(ctx, args, stdout, stderr)
	case "h2":
		return runH2Admission(ctx, args, stdout, stderr)
	case "h3":
		return runH3Admission(ctx, args, stdout, stderr)
	case "h4":
		return runH4Admission(ctx, args, stdout, stderr)
	default:
		fmt.Fprintln(stderr, "codehelper-eval: admission requires h1, h2, h3, or h4")
		return 2
	}
}

func runH2Admission(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "h2" {
		fmt.Fprintln(stderr, "codehelper-eval: expected admission h2")
		return 2
	}
	flags := flag.NewFlagSet("admission h2", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable H2 admission round id")
	lockPath := flags.String("lock", "", "frozen Harness Lock")
	catalogPath := flags.String(
		"catalog",
		"evaluation/spec/h2-execution.json",
		"H2 execution catalog",
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
	output := flags.String("output", "", "private H2 output directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*lockPath) == "" ||
		strings.TrimSpace(*output) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: admission h2 requires --id, --lock, and --output",
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
		fmt.Fprintln(stderr, "codehelper-eval: H2 output already exists")
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := os.MkdirAll(outputPath, 0o700); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := os.MkdirAll(
		filepath.Join(outputPath, "live-evidence"),
		0o700,
	); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	lock, err := freeze.Read(resolve(*lockPath))
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
	catalog, err := admission.LoadH2(absoluteRoot, *catalogPath)
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
	tasks := []qualification.Task{{
		ID: "identity-before", Check: identityCheck,
	}}
	for _, scenario := range catalog.Scenarios {
		for sample := 1; sample <= scenario.Repetitions; sample++ {
			tasks = append(tasks, h2LiveTask(
				absoluteRoot,
				outputPath,
				*id,
				lock,
				scenario,
				sample,
				runtimePath,
			))
		}
	}
	tasks = append(tasks,
		qualification.Task{
			ID: "aggregate-live-evidence",
			Check: func(context.Context) (string, error) {
				summary, aggregateErr := admission.AggregateH2(
					outputPath,
					catalog,
					*id,
					lock.SourceDigest,
					lock.LockIdentity,
				)
				return summary.EvidenceDigest, aggregateErr
			},
		},
		qualification.Task{
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
		qualification.Task{ID: "identity-after", Check: identityCheck},
	)
	report, err := qualification.Run(ctx, qualification.Request{
		ID:               *id,
		Kind:             "live",
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
		ID      string      `json:"id"`
		Status  spec.Status `json:"status"`
		Total   int         `json:"total"`
		Passed  int         `json:"passed"`
		Failed  int         `json:"failed"`
		Invalid int         `json:"invalid"`
		Output  string      `json:"output"`
	}{
		ID: *id, Status: report.Status,
		Total: report.Scheduled, Passed: report.Passed,
		Failed: report.Failed, Invalid: report.Invalid,
		Output: outputPath,
	})
	fmt.Fprintln(stdout, "H2_RESULT="+string(summary))
	if report.Status != spec.StatusPassed {
		return 1
	}
	return 0
}

func h2LiveTask(
	root, output, qualificationID string,
	lock freeze.Lock,
	scenario admission.H2Scenario,
	sample int,
	runtimeBinary string,
) qualification.Task {
	return qualification.Task{
		ID:        fmt.Sprintf("%s-%02d", scenario.ID, sample),
		Directory: root,
		Timeout:   10 * time.Minute,
		Command:   append([]string(nil), scenario.Command...),
		Env: []string{
			"CODEHELPER_STAGE=h2_live",
			"CODEHELPER_STAGE_RUN_ID=" + qualificationID,
			"CODEHELPER_STAGE_SOURCE_DIGEST=" + lock.SourceDigest,
			"CODEHELPER_STAGE_LOCK_IDENTITY=" + lock.LockIdentity,
			"CODEHELPER_H2_SCENARIO_ID=" + scenario.ID,
			"CODEHELPER_H2_SAMPLE_INDEX=" + strconv.Itoa(sample),
			"CODEHELPER_STAGE_EVIDENCE_PATH=" +
				admission.H2EvidencePath(output, scenario.ID, sample),
			"CODEHELPER_LIVE_BINARY=" + runtimeBinary,
		},
	}
}
