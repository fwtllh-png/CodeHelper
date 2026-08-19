package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/source"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func runQualification(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "q1" {
		fmt.Fprintln(stderr, "codehelper-eval: qualification requires the q1 command")
		return 2
	}
	flags := flag.NewFlagSet("qualification q1", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "Qualification and Harness Lock id")
	foundationPath := flags.String(
		"foundation",
		"evaluation/spec/foundation.json",
		"Foundation manifest",
	)
	evaluationBinary := flags.String(
		"evaluation-binary",
		"bin/codehelper-eval",
		"Evaluation binary",
	)
	runtimeBinary := flags.String("runtime", "bin/codehelper", "Runtime binary")
	vsix := flags.String(
		"vsix",
		"extensions/vscode/dist/codehelper-vscode-0.0.1.vsix",
		"VSIX artifact",
	)
	output := flags.String("output", "", "private Q1 output directory")
	buildDate := flags.String(
		"build-date",
		"2026-08-19T00:00:00Z",
		"reproducible Runtime build date",
	)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *id == "" {
		fmt.Fprintln(stderr, "codehelper-eval: qualification q1 requires --id")
		return 2
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if *output == "" {
		*output = filepath.Join(".tmp", "evaluation", "q1", *id)
	}
	outputPath := resolveFromRoot(absoluteRoot, *output)
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Fprintln(stderr, "codehelper-eval: Q1 output already exists")
		return 1
	} else if !os.IsNotExist(err) {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := os.MkdirAll(outputPath, 0o700); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}

	bundle, err := foundation.Load(absoluteRoot, *foundationPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	sourceIdentity, err := source.Resolve(ctx, absoluteRoot)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	evaluationPath := resolveFromRoot(absoluteRoot, *evaluationBinary)
	runtimePath := resolveFromRoot(absoluteRoot, *runtimeBinary)
	vsixPath := resolveFromRoot(absoluteRoot, *vsix)
	lock, scan, err := freeze.BuildCandidate(freeze.CandidateOptions{
		Root:             absoluteRoot,
		ID:               *id,
		Source:           sourceIdentity,
		Foundation:       bundle,
		EvaluationBinary: evaluationPath,
		RuntimeBinary:    runtimePath,
		VSIX:             vsixPath,
		InputRoots:       q1InputRoots(),
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	lockPath := filepath.Join(outputPath, "harness-lock.json")
	if err := freeze.Write(lockPath, lock); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := writePrivateJSON(filepath.Join(outputPath, "production-scan.json"), scan); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}

	makeArgs := []string{
		"BUILD_DATE=" + *buildDate,
		"VERSION=q1",
		"COMMIT=" + sourceIdentity.Commit,
	}
	epoch, err := qualification.Run(ctx, qualification.Request{
		ID:               *id + "-epoch",
		Kind:             "foundation_epoch",
		Root:             absoluteRoot,
		FoundationDigest: lock.FoundationDigest,
		SourceDigest:     lock.SourceDigest,
		RuntimeDigest:    lock.RuntimeDigest,
		VSIXDigest:       lock.VSIXDigest,
		LockIdentity:     lock.LockIdentity,
		Tasks:            q1EpochTasks(absoluteRoot, scan.Digest),
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := qualification.Write(filepath.Join(outputPath, "epoch"), epoch); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if epoch.Status != spec.StatusPassed {
		fmt.Fprintln(stderr, "codehelper-eval: Foundation Qualification Epoch failed")
		return 1
	}

	var integrationDigests []string
	for index := 1; index <= 3; index++ {
		runID := fmt.Sprintf("%s-integration-%02d", *id, index)
		runDirectory := filepath.Join(
			outputPath,
			fmt.Sprintf("integration-%02d", index),
		)
		report, err := qualification.Run(ctx, qualification.Request{
			ID:               runID,
			Kind:             "integration",
			Root:             absoluteRoot,
			FoundationDigest: lock.FoundationDigest,
			SourceDigest:     lock.SourceDigest,
			RuntimeDigest:    lock.RuntimeDigest,
			VSIXDigest:       lock.VSIXDigest,
			LockIdentity:     lock.LockIdentity,
			Tasks: q1IntegrationTasks(
				absoluteRoot,
				lock,
				evaluationPath,
				runtimePath,
				vsixPath,
				makeArgs,
				runID,
				runDirectory,
			),
		})
		if err != nil {
			fmt.Fprintln(stderr, "codehelper-eval:", err)
			return 1
		}
		if err := qualification.Write(runDirectory, report); err != nil {
			fmt.Fprintln(stderr, "codehelper-eval:", err)
			return 1
		}
		if report.Status != spec.StatusPassed {
			fmt.Fprintf(stderr, "codehelper-eval: Integration run %d failed\n", index)
			return 1
		}
		lock, err = freeze.AppendIntegrationRun(lock, report)
		if err != nil {
			fmt.Fprintln(stderr, "codehelper-eval:", err)
			return 1
		}
		if err := freeze.Write(lockPath, lock); err != nil {
			fmt.Fprintln(stderr, "codehelper-eval:", err)
			return 1
		}
		integrationDigests = append(integrationDigests, report.EvidenceDigest)
	}
	if lock.Status != "frozen_qualified" {
		fmt.Fprintln(stderr, "codehelper-eval: Harness Lock did not freeze")
		return 1
	}
	summary := struct {
		Status             string   `json:"status"`
		LockPath           string   `json:"lock_path"`
		LockIdentity       string   `json:"lock_identity"`
		FoundationDigest   string   `json:"foundation_digest"`
		SourceDigest       string   `json:"source_digest"`
		RuntimeDigest      string   `json:"runtime_digest"`
		VSIXDigest         string   `json:"vsix_digest"`
		IntegrationDigests []string `json:"integration_digests"`
	}{
		Status: lock.Status, LockPath: lockPath,
		LockIdentity:       lock.LockIdentity,
		FoundationDigest:   lock.FoundationDigest,
		SourceDigest:       lock.SourceDigest,
		RuntimeDigest:      lock.RuntimeDigest,
		VSIXDigest:         lock.VSIXDigest,
		IntegrationDigests: integrationDigests,
	}
	if err := writePrivateJSON(filepath.Join(outputPath, "q1-summary.json"), summary); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	raw, _ := json.Marshal(summary)
	fmt.Fprintln(stdout, "Q1_RESULT="+string(raw))
	return 0
}

func q1EpochTasks(root, scanDigest string) []qualification.Task {
	return []qualification.Task{
		commandTask("evaluation-contracts", root, 15*time.Minute,
			"make", "eval-contract-check", "eval-foundation-check", "eval-replay", "eval-oracle"),
		commandTask("evaluation-race", root, 15*time.Minute,
			"go", "test", "-race", "-count=1", "./evaluation/..."),
		commandTask("repository-hermetic", root, 30*time.Minute,
			"make", "test-hermetic"),
		commandTask("architecture-ratchet", root, 10*time.Minute,
			"make", "architecture-ratchet"),
		commandTask("documentation-book", root, 10*time.Minute,
			"make", "docs-check", "book-check"),
		commandTask("vscode-check-test", root, 20*time.Minute,
			"make", "vscode-check", "vscode-test"),
		commandTask("git-diff-check", root, 2*time.Minute,
			"git", "diff", "--check"),
		{
			ID: "production-isolation",
			Check: func(context.Context) (string, error) {
				return scanDigest, nil
			},
		},
	}
}

func q1IntegrationTasks(
	root string,
	lock freeze.Lock,
	evaluationBinary, runtimeBinary, vsixPath string,
	makeArgs []string,
	qualificationID, outputDirectory string,
) []qualification.Task {
	identityCheck := func(ctx context.Context) (string, error) {
		bundle, err := foundation.Load(root, "evaluation/spec/foundation.json")
		if err != nil {
			return "", err
		}
		currentSource, err := source.Resolve(ctx, root)
		if err != nil {
			return "", err
		}
		return freeze.VerifyIdentity(
			root,
			lock,
			currentSource,
			bundle,
			evaluationBinary,
			runtimeBinary,
			vsixPath,
		)
	}
	acpArgs := append([]string{"make"}, makeArgs...)
	acpArgs = append(acpArgs, "acp-interop")
	vscodeArgs := append([]string{"make"}, makeArgs...)
	vscodeArgs = append(vscodeArgs, "vscode-runtime-integration")
	vscodeTask := commandTask(
		"vscode-runtime",
		root,
		15*time.Minute,
		vscodeArgs...,
	)
	vscodeTask.CleanupReport = filepath.Join(
		outputDirectory,
		"vscode-runtime-cleanup.json",
	)
	vscodeTask.Env = []string{
		"CODEHELPER_Q1_CLEANUP_REPORT=" + vscodeTask.CleanupReport,
		"CODEHELPER_Q1_QUALIFICATION_ID=" + qualificationID,
		"CODEHELPER_Q1_TASK_ID=" + vscodeTask.ID,
	}
	return []qualification.Task{
		{ID: "identity-before", Check: identityCheck},
		commandTask("foundation-check", root, 5*time.Minute,
			evaluationBinary, "foundation", "check", "--root", root),
		commandTask("runtime-quickstart", root, 5*time.Minute,
			runtimeBinary, "quickstart", "--json"),
		commandTask("acp-interop", root, 10*time.Minute, acpArgs...),
		vscodeTask,
		{
			ID: "production-scan",
			Check: func(context.Context) (string, error) {
				scan, err := freeze.ScanProductionArtifacts(runtimeBinary, vsixPath)
				return scan.Digest, err
			},
		},
		{ID: "identity-after", Check: identityCheck},
	}
}

func commandTask(
	id, root string,
	timeout time.Duration,
	arguments ...string,
) qualification.Task {
	return qualification.Task{
		ID: id, Directory: root, Timeout: timeout, Command: arguments,
	}
}

func q1InputRoots() []string {
	return []string{
		".github/workflows",
		"Makefile",
		"cmd",
		"evaluation/cmd",
		"evaluation/corpus",
		"evaluation/fixtures",
		"evaluation/impact-map.json",
		"evaluation/internal",
		"evaluation/manifest.json",
		"evaluation/scenarios",
		"evaluation/schema",
		"evaluation/spec",
		"extensions/vscode/package.json",
		"extensions/vscode/package-lock.json",
		"extensions/vscode/scripts",
		"extensions/vscode/src",
		"go.mod",
		"go.sum",
		"internal",
		"scripts",
		"testdata",
	}
}

func resolveFromRoot(root, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}
