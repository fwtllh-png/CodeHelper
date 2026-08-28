package quality

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/artifactbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/processbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Tool struct {
	root    string
	kind    string
	run     func(context.Context, process.Options) (process.Result, error)
	sandbox sandbox.Backend
}

type trustedExecutor struct {
	tool.OutcomeExecutor
	disposition tool.ExecutionDisposition
	binding     tool.TrustedBinding
}

func (e *trustedExecutor) ExecutionDisposition() tool.ExecutionDisposition {
	return e.disposition
}

func (e *trustedExecutor) TrustedBinding() tool.TrustedBinding {
	return e.binding
}

func (t *Tool) TrustedBinding() tool.TrustedBinding {
	binding := tool.TrustedBindingFromDescriptor(t.Descriptor())
	binding.ProducesVerificationEvidence =
		t.kind == "quality_test" || t.kind == "quality_verify"
	return binding
}

type input struct {
	Command        string                       `json:"command"`
	CoveredPaths   []string                     `json:"covered_paths"`
	NetworkTargets []tool.DeclaredNetworkTarget `json:"network_targets"`
	AllowLoopback  bool                         `json:"allow_loopback"`
}

type RuntimeDependencies struct {
	ArtifactBroker *artifactbroker.Broker
	ProcessBroker  *processbroker.Broker
}

func RegisterWithBackend(registry *tool.Registry, root string, backend sandbox.Backend) error {
	return RegisterWithBackendAndRuntime(
		registry, root, backend, RuntimeDependencies{},
	)
}

func RegisterWithBackendAndRuntime(
	registry *tool.Registry,
	root string,
	backend sandbox.Backend,
	runtime RuntimeDependencies,
) error {
	if backend == nil {
		return fmt.Errorf("quality tools require an injected sandbox backend")
	}
	backend, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		return err
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	absolute := workspace.Root()
	registry.SetSandboxBackend(backend)
	for _, kind := range []string{"quality_test", "quality_diagnostics", "quality_review", "quality_verify"} {
		instance := &Tool{
			root: absolute, kind: kind, sandbox: backend,
		}
		executor, err := instance.typedExecutor()
		if err != nil {
			return err
		}
		if err := registry.Register(executor); err != nil {
			return err
		}
	}
	sandboxPolicy, ok := sandbox.BackendPolicy(backend)
	if !ok {
		return errors.New("quality process smoke requires an injected sandbox policy")
	}
	return registerProcessSmoke(
		registry, absolute, sandboxPolicy.PrivateTemp, runtime,
	)
}

func (t *Tool) Descriptor() tool.Descriptor {
	description := map[string]string{
		"quality_test":        "Run a test command and return a structured test result. Declare every dependency-download destination in network_targets. Set allow_loopback only for tests that bind or connect to local fixture servers",
		"quality_diagnostics": "Run a static diagnostics command and return a structured diagnostics result",
		"quality_review":      "Run a read-only review command and return a structured review result",
		"quality_verify":      "Run a verifier command and return a structured verifier result. Declare every dependency-download destination in network_targets. Set allow_loopback only for verifiers that use local fixture servers",
	}[t.kind]
	defaultCommand := map[string]string{
		"quality_test":        "go test ./...",
		"quality_diagnostics": "go vet ./...",
		"quality_review":      "git diff --check",
	}[t.kind]
	commandSchema := map[string]any{"type": "string"}
	if defaultCommand != "" {
		commandSchema["default"] = defaultCommand
	}
	properties := map[string]any{"command": commandSchema}
	resolver := tool.ResourceResolver{Templates: []tool.ResourceTemplate{
		{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
		{Kind: "process", ID: "workspace", Access: tool.AccessRead, Tree: true},
	}}
	if t.kind == "quality_test" || t.kind == "quality_verify" {
		properties["covered_paths"] = map[string]any{
			"type": "array", "maxItems": 128,
			"items": map[string]any{"type": "string", "minLength": 1},
		}
		properties["network_targets"] = tool.NetworkTargetsInputSchema()
		properties["allow_loopback"] = map[string]any{
			"type":        "boolean",
			"description": "Permit localhost bind/connect for local test fixtures. Requires approval and does not permit non-loopback direct network.",
		}
		resolver.ReadPathsField = "covered_paths"
		resolver.NetworkTargetsField = "network_targets"
		resolver.LoopbackField = "allow_loopback"
	}
	return tool.Descriptor{
		Name: t.kind, Description: description, Visibility: tool.VisibleModel,
		Capability: tool.CapabilityProcess, AccessMode: tool.AccessTree,
		ResourceResolver:   resolver,
		ParallelPolicy:     tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": properties,
			"additionalProperties": false,
		},
	}
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	executor, err := t.typedExecutor()
	if err != nil {
		return tool.Result{}, err
	}
	return executor.Execute(ctx, raw)
}

func (t *Tool) typedExecutor() (tool.Executor, error) {
	binding := t.TrustedBinding()
	executor, err := typed.Define(typed.Spec[input, tool.Result]{
		Descriptor:  t.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Run:         t.runTyped,
		Encode: func(value tool.Result) (tool.Result, error) {
			return value, nil
		},
		Outcome: verificationOutcome,
	})
	if err != nil {
		return nil, err
	}
	runtime, ok := executor.(tool.OutcomeExecutor)
	if !ok {
		return nil, errors.New("quality typed runtime is incomplete")
	}
	return &trustedExecutor{
		OutcomeExecutor: runtime,
		disposition:     tool.DispositionFor(executor),
		binding:         binding,
	}, nil
}

func verificationOutcome(value tool.Result) tool.Outcome {
	outcome := tool.OutcomeFromResult(value)
	if evidence, ok := value.Metadata[verify.EvidenceMetadataKey].(verify.Evidence); ok {
		copy := evidence
		outcome.Facts = &tool.OutcomeFacts{Verification: &copy}
	}
	return outcome
}

func (t *Tool) runTyped(ctx context.Context, value input) (tool.Result, error) {
	if err := tool.ValidateDeclaredNetworkTargets(value.NetworkTargets); err != nil {
		return tool.Result{}, err
	}
	coveredPaths, err := t.canonicalCoveredPaths(value.CoveredPaths)
	if err != nil {
		return tool.Result{}, err
	}
	if t.kind == "quality_verify" {
		return t.executeVerifier(ctx, value.Command, coveredPaths)
	}
	command := value.Command
	if command == "" {
		command = map[string]string{
			"quality_test":        "go test ./...",
			"quality_diagnostics": "go vet ./...",
			"quality_review":      "git diff --check",
		}[t.kind]
	}
	return t.executeSingle(ctx, command, coveredPaths)
}

func (t *Tool) executeSingle(
	ctx context.Context,
	command string,
	coveredPaths []string,
) (tool.Result, error) {
	result, err := t.runProcess(ctx, command)
	if err != nil {
		return tool.Result{}, err
	}
	status, reason := verify.CommandResultStatus(command, result)
	kind := strings.TrimPrefix(t.kind, "quality_")
	payload := map[string]any{
		"schema_version": 1,
		"kind":           kind,
		"status":         status,
		"exit_code":      result.ExitCode,
		"stdout":         result.Stdout,
		"stderr":         result.Stderr,
	}
	if reason != "" {
		payload["message"] = reason
		payload["error_category"] = verify.ErrorCategoryDependencyUnavailable
	}
	switch t.kind {
	case "quality_test":
		payload["summary"] = map[string]any{"passed": status == "passed"}
	case "quality_diagnostics":
		payload["diagnostics"] = parseDiagnostics(result.Stdout, result.Stderr)
	case "quality_review":
		payload["findings"] = parseFindings(result.Stdout, result.Stderr)
	}
	return encodeResult(
		payload, kind, status, result.ExitCode, command, coveredPaths,
	)
}

func (t *Tool) executeVerifier(
	ctx context.Context,
	command string,
	coveredPaths []string,
) (tool.Result, error) {
	checks := []verify.Command{{Name: "custom", Command: command}}
	if command == "" {
		checks = verify.Detect(t.root)
	}
	results := make([]map[string]any, 0, len(checks))
	status := "passed"
	exitCode := 0
	var stdout, stderr strings.Builder
	var unavailableReason string
	for _, check := range checks {
		result, err := t.runProcess(ctx, check.Command)
		if err != nil {
			return tool.Result{}, err
		}
		checkStatus, reason := verify.CommandResultStatus(check.Command, result)
		switch checkStatus {
		case verify.StatusFailed:
			status = "failed"
		case verify.StatusUnavailable:
			if status == "passed" {
				status = "unavailable"
			}
			if unavailableReason == "" {
				unavailableReason = reason
			}
		}
		if result.ExitCode != 0 && exitCode == 0 {
			exitCode = result.ExitCode
		}
		checkResult := map[string]any{
			"name": check.Name, "command": check.Command, "status": checkStatus,
			"exit_code": result.ExitCode, "stdout": result.Stdout, "stderr": result.Stderr,
		}
		if reason != "" {
			checkResult["message"] = reason
			checkResult["error_category"] = verify.ErrorCategoryDependencyUnavailable
		}
		results = append(results, checkResult)
		appendCheckOutput(&stdout, check.Name, result.Stdout)
		appendCheckOutput(&stderr, check.Name, result.Stderr)
	}
	payload := map[string]any{
		"schema_version": 1,
		"kind":           "verify",
		"status":         status,
		"exit_code":      exitCode,
		"stdout":         stdout.String(),
		"stderr":         stderr.String(),
		"checks":         results,
	}
	if unavailableReason != "" && status != "failed" {
		payload["message"] = unavailableReason
		payload["error_category"] = verify.ErrorCategoryDependencyUnavailable
	}
	commands := make([]string, 0, len(checks))
	for _, check := range checks {
		commands = append(commands, check.Command)
	}
	return encodeResult(
		payload, "verify", status, exitCode, strings.Join(commands, "\n"), coveredPaths,
	)
}

func (t *Tool) runProcess(ctx context.Context, command string) (process.Result, error) {
	options := process.Options{
		Command: failFastShellCommand(command), Dir: t.root, Sandbox: t.sandbox,
		RequireStrongSandbox: true, WorkspaceReadOnly: true,
	}
	if t.run != nil {
		return t.run(ctx, options)
	}
	directory, err := process.OpenPinnedDirectory(t.sandbox, t.root)
	if err != nil {
		return process.Result{}, err
	}
	defer directory.Close()
	options.DirFile = directory
	return process.Run(ctx, options)
}

func failFastShellCommand(command string) string {
	return "set -e\n" + command
}

func appendCheckOutput(target *strings.Builder, name, output string) {
	if output == "" {
		return
	}
	fmt.Fprintf(target, "[%s]\n%s", name, output)
	if !strings.HasSuffix(output, "\n") {
		target.WriteByte('\n')
	}
}

var locationPattern = regexp.MustCompile(`^(.+?):([0-9]+)(?::([0-9]+))?:\s*(?:(critical|high|medium|low|info|error|warning|warn|note|hint)\s*:\s*)?(.+)$`)

func parseDiagnostics(stdout, stderr string) []map[string]any {
	items := make([]map[string]any, 0)
	for _, line := range outputLines(stdout, stderr) {
		match := locationPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		item := map[string]any{
			"file": match[1], "line": mustPositiveInt(match[2]),
			"severity": diagnosticSeverity(match[4]), "message": strings.TrimSpace(match[5]),
		}
		if match[3] != "" {
			item["column"] = mustPositiveInt(match[3])
		}
		items = append(items, item)
	}
	return items
}

func parseFindings(stdout, stderr string) []map[string]any {
	items := make([]map[string]any, 0)
	for _, line := range outputLines(stdout, stderr) {
		match := locationPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		items = append(items, map[string]any{
			"severity": reviewSeverity(match[4]), "file": match[1],
			"line": mustPositiveInt(match[2]), "message": strings.TrimSpace(match[5]),
		})
	}
	return items
}

func outputLines(outputs ...string) []string {
	var lines []string
	for _, output := range outputs {
		for line := range strings.SplitSeq(output, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
	}
	return lines
}

func mustPositiveInt(value string) int {
	parsed, _ := strconv.Atoi(value)
	return max(1, parsed)
}

func diagnosticSeverity(value string) string {
	switch value {
	case "warning", "warn", "medium", "low":
		return "warning"
	case "info", "note":
		return "info"
	case "hint":
		return "hint"
	default:
		return "error"
	}
}

func reviewSeverity(value string) string {
	switch value {
	case "critical", "high", "medium", "low", "info":
		return value
	case "error":
		return "high"
	case "warning", "warn":
		return "medium"
	default:
		return "medium"
	}
}

func encodeResult(
	payload map[string]any,
	kind, status string,
	exitCode int,
	command string,
	coveredPaths []string,
) (tool.Result, error) {
	if len(coveredPaths) != 0 {
		payload["covered_paths"] = append([]string(nil), coveredPaths...)
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	metadata := map[string]any{
		"schema_version": 1, "result_kind": kind, "status": status, "exit_code": exitCode,
	}
	if status == verify.StatusUnavailable {
		metadata["error_category"] = verify.ErrorCategoryDependencyUnavailable
	}
	if (kind == "test" || kind == "verify") && len(coveredPaths) != 0 {
		metadata[verify.EvidenceMetadataKey] = verify.Evidence{
			SchemaVersion: 1,
			Kind:          kind,
			Status:        status,
			CoveredPaths:  append([]string(nil), coveredPaths...),
			CommandDigest: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(command))),
			ExitCode:      exitCode,
		}
	}
	return tool.Result{
		Content: string(content), IsError: status == verify.StatusFailed,
		Metadata: metadata,
	}, nil
}

func (t *Tool) canonicalCoveredPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > 128 {
		return nil, errors.New("covered_paths exceeds 128 entries")
	}
	workspace, err := sandbox.NewWorkspace(t.root)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(paths))
	canonical := make([]string, 0, len(paths))
	for _, path := range paths {
		resolved, err := workspace.Resolve(strings.TrimSpace(path), sandbox.AllowMissing)
		if err != nil {
			return nil, fmt.Errorf("covered path %q: %w", path, err)
		}
		relative, err := filepath.Rel(workspace.Root(), resolved)
		if err != nil {
			return nil, err
		}
		relative = filepath.ToSlash(relative)
		if _, exists := seen[relative]; exists {
			continue
		}
		seen[relative] = struct{}{}
		canonical = append(canonical, relative)
	}
	sort.Strings(canonical)
	return canonical, nil
}
