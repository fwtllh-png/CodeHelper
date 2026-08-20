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

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/report"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/source"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

const defaultManifest = "evaluation/manifest.json"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "contract":
		return runContract(args[1:], stdout, stderr)
	case "foundation":
		return runFoundation(ctx, args[1:], stdout, stderr)
	case "qualification":
		return runQualification(ctx, args[1:], stdout, stderr)
	case "discovery":
		return runDiscovery(ctx, args[1:], stdout, stderr)
	case "admission":
		return runAdmission(ctx, args[1:], stdout, stderr)
	case "proof":
		return runProof(ctx, args[1:], stdout, stderr)
	case "run":
		return runScenario(ctx, args[1:], stdout, stderr)
	case "capture":
		return runCapture(ctx, args[1:], stdout, stderr)
	case "replay":
		return runReplay(args[1:], stdout, stderr)
	case "oracle":
		return runOracle(args[1:], stdout, stderr)
	case "impact":
		return runImpact(args[1:], stdout, stderr)
	case "source":
		return runSource(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "codehelper-eval: unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runSource(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "identity" {
		fmt.Fprintln(stderr, "codehelper-eval: source requires the identity command")
		return 2
	}
	flags := flag.NewFlagSet("source identity", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "codehelper-eval: source identity accepts no arguments")
		return 2
	}
	identity, err := source.Resolve(ctx, *root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	encoded, err := json.Marshal(identity)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(encoded))
	return 0
}

func runContract(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, "codehelper-eval: contract requires the check command")
		return 2
	}
	flags := flag.NewFlagSet("contract check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	manifest := flags.String("manifest", defaultManifest, "evaluation manifest path")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "codehelper-eval: contract check accepts no arguments")
		return 2
	}
	bundle, err := spec.Check(*root, *manifest, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	fmt.Fprintf(
		stdout,
		"evaluation contract valid: %d suites, %d scenarios\n",
		len(bundle.Manifest.Suites),
		len(bundle.Scenarios),
	)
	return 0
}

func runScenario(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	manifest := flags.String("manifest", defaultManifest, "evaluation manifest path")
	suiteID := flags.String("suite", "", "suite id")
	scenarioID := flags.String("scenario", "", "scenario id")
	runID := flags.String("run-id", "", "stable run id")
	variant := flags.String("variant", "default", "scenario variant")
	output := flags.String("output", "", "report output directory")
	seed := flags.Int64("seed", 1, "initial deterministic seed")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*suiteID) == "" ||
		strings.TrimSpace(*scenarioID) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: run requires --suite and --scenario",
		)
		return 2
	}
	bundle, err := spec.Check(*root, *manifest, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	suite, exists := bundle.Suite(*suiteID)
	if !exists {
		fmt.Fprintf(stderr, "codehelper-eval: unknown suite %q\n", *suiteID)
		return 2
	}
	scenario, exists := bundle.Scenario(*scenarioID)
	if !exists || !suiteContains(bundle, suite, *scenarioID) {
		fmt.Fprintf(
			stderr,
			"codehelper-eval: scenario %q is not in suite %q\n",
			*scenarioID,
			*suiteID,
		)
		return 2
	}
	if *runID == "" {
		*runID = "run-" + strings.ToLower(
			time.Now().UTC().Format("20060102t150405z"),
		)
	}
	if *output == "" {
		*output = filepath.Join(".tmp", "evaluation", *runID)
	}
	outputPath := *output
	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(bundle.Root, filepath.FromSlash(outputPath))
	}
	if mkdirErr := os.MkdirAll(outputPath, 0o755); mkdirErr != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", mkdirErr)
		return 1
	}

	sourceIdentity, err := source.Resolve(ctx, bundle.Root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	foundationBundle, err := foundation.Load(
		bundle.Root,
		"evaluation/spec/foundation.json",
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	artifacts := spec.ArtifactIdentity{
		HarnessDigest:  foundationBundle.HarnessInputHash,
		RuntimeDigest:  spec.DigestString("runtime:" + sourceIdentity.Commit),
		HostDigest:     spec.DigestString("driver:" + string(scenario.Driver)),
		ScenarioDigest: bundle.ScenarioDigests[scenario.ID],
		FixtureDigest:  spec.DigestString("fixture:" + scenario.FixtureID),
		ProviderDigest: spec.DigestString("provider:" + scenario.ProviderMode),
		ModelDigest:    spec.DigestString("model:none"),
		ConfigDigest:   spec.DigestString("lane:" + suite.DefaultLane),
	}
	evaluationRunner := runner.Runner{}
	runs := make([]spec.RunRecord, 0, suite.Repetitions)
	for attempt := 1; attempt <= suite.Repetitions; attempt++ {
		evidencePath := filepath.Join(
			outputPath,
			"attempts",
			fmt.Sprintf("a%03d", attempt),
			"evidence.jsonl",
		)
		record := evaluationRunner.Run(ctx, runner.Request{
			Root:         bundle.Root,
			RunID:        *runID,
			Variant:      *variant,
			Attempt:      attempt,
			Seed:         *seed + int64(attempt-1),
			Suite:        suite,
			Scenario:     scenario,
			Source:       sourceIdentity,
			Artifacts:    artifacts,
			Environment:  source.Environment(""),
			EvidencePath: evidencePath,
		})
		runs = append(runs, record)
	}
	result, err := report.Build(runs)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	result.Admission = spec.Admit(suite, scenario, runs, time.Now())
	if writeErr := report.Write(outputPath, result); writeErr != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", writeErr)
		return 1
	}
	summary, _ := json.Marshal(struct {
		RunID  string      `json:"run_id"`
		Status spec.Status `json:"status"`
		Output string      `json:"output"`
	}{
		RunID: *runID, Status: result.Status, Output: outputPath,
	})
	fmt.Fprintln(stdout, "EVALUATION_RESULT="+string(summary))
	if result.Admission.Blocking && !result.Admission.Allowed {
		return 1
	}
	return 0
}

func suiteContains(bundle spec.Bundle, suite spec.Suite, scenarioID string) bool {
	path, exists := bundle.ScenarioPaths[scenarioID]
	if !exists {
		return false
	}
	for _, candidate := range suite.Scenarios {
		if filepath.Clean(filepath.FromSlash(candidate)) ==
			filepath.Clean(filepath.FromSlash(path)) {
			return true
		}
	}
	return false
}

func printUsage(writer io.Writer) {
	_, _ = fmt.Fprintln(writer, "usage:")
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval contract check [--root DIR] [--manifest FILE]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval foundation check [--root DIR] [--manifest FILE]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval qualification q1 --id ID [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval discovery d1 --id ID --lock FILE --output DIR [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval admission h1 --id ID --lock FILE --output DIR [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval admission h2 --id ID --lock FILE --output DIR [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval admission h3 --id ID --lock FILE --h1-report FILE --h2-report FILE --output DIR [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval admission h4 --id ID --lock FILE --h3-report FILE --h3-release FILE --h3-package DIR --output DIR [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval run --suite ID --scenario ID [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval capture promote --input FILE --format FORMAT --prefix ID --batch ID --review FILE [options]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval replay check [--corpus DIR] [--minimum N]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval oracle check [--minimum 30] [--replay-runs 500]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval impact select --path FILE [--path FILE...]",
	)
	_, _ = fmt.Fprintln(
		writer,
		"  codehelper-eval source identity [--root DIR]",
	)
}
