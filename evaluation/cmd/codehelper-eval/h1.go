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

func runAdmission(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "h1" {
		fmt.Fprintln(stderr, "codehelper-eval: expected admission h1")
		return 2
	}
	flags := flag.NewFlagSet("admission h1", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable H1 admission round id")
	lockPath := flags.String("lock", "", "frozen Harness Lock")
	catalogPath := flags.String(
		"catalog",
		"evaluation/spec/h1-execution.json",
		"H1 execution catalog",
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
	output := flags.String("output", "", "private H1 output directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*lockPath) == "" ||
		strings.TrimSpace(*output) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: admission h1 requires --id, --lock, and --output",
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
		fmt.Fprintln(stderr, "codehelper-eval: H1 output already exists")
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := os.MkdirAll(outputPath, 0o700); err != nil {
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
	catalog, err := admission.LoadH1(absoluteRoot, *catalogPath)
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
	for _, item := range catalog.Cases {
		task := h1Task(
			absoluteRoot,
			outputPath,
			*id,
			item,
			evaluationPath,
			runtimePath,
		)
		tasks = append(tasks, task)
	}
	tasks = append(tasks,
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
		Kind:             "chaos",
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
	fmt.Fprintln(stdout, "H1_RESULT="+string(summary))
	if report.Status != spec.StatusPassed {
		return 1
	}
	return 0
}

func h1Task(
	root, output, qualificationID string,
	item admission.H1Case,
	evaluationBinary, runtimeBinary string,
) qualification.Task {
	command := append([]string(nil), item.Command...)
	if item.Kind == "go_test" {
		command = proofGoTestCommand(
			evaluationBinary,
			max(1, item.MinimumTests),
			command,
		)
	}
	task := qualification.Task{
		ID:        item.ID,
		Directory: root,
		Timeout:   15 * time.Minute,
		Command:   command,
		Env:       append([]string(nil), item.Environment...),
	}
	switch item.Kind {
	case "electron":
		task.Timeout = 60 * time.Minute
		task.Env = append(task.Env,
			"CODEHELPER_VSCODE_BINARY="+runtimeBinary,
			"CODEHELPER_VSCODE_SELECTION_FIXTURE="+filepath.Join(
				root,
				"testdata/providers/selection-commands",
			),
			"CODEHELPER_VSCODE_APPROVAL_FIXTURE="+filepath.Join(
				root,
				"testdata/providers/vscode-approval-focus",
			),
			"CODEHELPER_VSCODE_SUBAGENT_FIXTURE="+filepath.Join(
				root,
				"testdata/providers/vscode-subagent",
			),
			"CODEHELPER_APPROVAL_EVIDENCE_DIR="+filepath.Join(
				output,
				"approval-evidence",
			),
			"CODEHELPER_ELECTRON_SCENARIOS="+strings.Join([]string{
				"empty",
				"workspace",
				"accessibility",
				"approval",
				"native",
				"multi",
				"subagent",
			}, ","),
		)
	case "vscode_runtime":
		task.Timeout = 20 * time.Minute
		task.CleanupReport = filepath.Join(
			output,
			"vscode-runtime-cleanup.json",
		)
		task.Env = append(task.Env,
			"CODEHELPER_VSCODE_BINARY="+runtimeBinary,
			"CODEHELPER_VSCODE_FIXTURE="+filepath.Join(
				root,
				"testdata/providers/tools",
			),
			"CODEHELPER_VSCODE_CONTEXT_FIXTURE="+filepath.Join(
				root,
				"testdata/providers/editor-context",
			),
			"CODEHELPER_Q1_CLEANUP_REPORT="+task.CleanupReport,
			"CODEHELPER_Q1_QUALIFICATION_ID="+qualificationID,
			"CODEHELPER_Q1_TASK_ID="+task.ID,
		)
	}
	return task
}
