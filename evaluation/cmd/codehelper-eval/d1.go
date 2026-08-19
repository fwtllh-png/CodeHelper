package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corepack"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/discovery"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/oracle"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/source"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func runDiscovery(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "d1" {
		fmt.Fprintln(stderr, "codehelper-eval: expected discovery d1")
		return 2
	}
	flags := flag.NewFlagSet("discovery d1", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable Discovery round id")
	lockPath := flags.String("lock", "", "frozen Harness Lock")
	catalogPath := flags.String(
		"catalog",
		"evaluation/spec/d1-execution.json",
		"D1 execution catalog",
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
	output := flags.String("output", "", "private Discovery output directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*lockPath) == "" ||
		strings.TrimSpace(*output) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: discovery d1 requires --id, --lock, and --output",
		)
		return 2
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(absoluteRoot, filepath.FromSlash(path))
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
	coreBundle, err := corepack.Load(
		absoluteRoot,
		"evaluation/scenarios/core/pack.json",
		"evaluation/impact-map.json",
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	catalog, err := discovery.Load(
		absoluteRoot,
		*catalogPath,
		coreBundle.Pack,
	)
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
	tasks := []qualification.Task{{
		ID: "identity-before", Check: identityCheck,
	}}
	for _, binding := range catalog.Scenarios {
		minimum := max(1, binding.MinimumTests)
		tasks = append(tasks, qualification.Task{
			ID:        "scenario-" + binding.ID,
			Directory: absoluteRoot,
			Timeout:   10 * time.Minute,
			Command: proofGoTestCommand(
				evaluationPath,
				minimum,
				binding.Command,
			),
		})
	}
	for _, fault := range coreBundle.Pack.FaultCases {
		fault := fault
		tasks = append(tasks, qualification.Task{
			ID: "fault-" + fault.ID,
			Check: func(context.Context) (string, error) {
				return checkDiscoveryFault(fault)
			},
		})
	}
	outputPath := resolve(*output)
	for _, host := range catalog.Hosts {
		command := append([]string(nil), host.Command...)
		if len(command) >= 2 && command[0] == "go" && command[1] == "test" {
			command = proofGoTestCommand(
				evaluationPath,
				max(1, host.MinimumTests),
				command,
			)
		}
		task := qualification.Task{
			ID:        host.ID,
			Directory: absoluteRoot,
			Timeout:   15 * time.Minute,
			Command:   command,
		}
		switch host.Host {
		case "acp":
			task.Env = []string{
				"CODEHELPER_ACP_BINARY=" + runtimePath,
			}
		case "vscode":
			task.CleanupReport = filepath.Join(
				outputPath,
				"vscode-runtime-cleanup.json",
			)
			task.Env = []string{
				"CODEHELPER_VSCODE_BINARY=" + runtimePath,
				"CODEHELPER_VSCODE_FIXTURE=" + filepath.Join(
					absoluteRoot,
					"testdata/providers/tools",
				),
				"CODEHELPER_VSCODE_CONTEXT_FIXTURE=" + filepath.Join(
					absoluteRoot,
					"testdata/providers/editor-context",
				),
				"CODEHELPER_Q1_CLEANUP_REPORT=" + task.CleanupReport,
				"CODEHELPER_Q1_QUALIFICATION_ID=" + *id,
				"CODEHELPER_Q1_TASK_ID=" + task.ID,
			}
		}
		tasks = append(tasks, task)
	}
	tasks = append(tasks, qualification.Task{
		ID: "identity-after", Check: identityCheck,
	})
	report, err := qualification.Run(ctx, qualification.Request{
		ID:               *id,
		Kind:             "discovery",
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
	fmt.Fprintln(stdout, "DISCOVERY_RESULT="+string(summary))
	if report.Status != spec.StatusPassed {
		return 1
	}
	return 0
}

func proofGoTestCommand(
	evaluationBinary string,
	minimum int,
	command []string,
) []string {
	result := []string{
		evaluationBinary,
		"proof",
		"go-test",
		"--minimum",
		fmt.Sprint(minimum),
		"--",
	}
	return append(result, command...)
}

func checkDiscoveryFault(fault corepack.FaultCase) (string, error) {
	input := oracle.NewBaseline(fault.ID, fault.ID+"-fixture")
	injected, err := oracle.Inject(input, fault.Fault)
	if err != nil {
		return "", err
	}
	report := oracle.Evaluate(injected, oracle.AllIDs, fault.Risk)
	if report.Status != fault.ExpectedStatus ||
		report.FailureSignature != fault.ExpectedSignature ||
		report.Primary == nil ||
		report.Primary.Domain != fault.ExpectedDomain {
		return "", fmt.Errorf(
			"fault %s produced status=%s signature=%s domain=%v",
			fault.ID,
			report.Status,
			report.FailureSignature,
			report.Primary,
		)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", errors.New("fault report is empty")
	}
	return spec.DigestString(string(raw)), nil
}
