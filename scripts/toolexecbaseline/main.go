// Command toolexecbaseline records and checks the local tool-execution surface.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	shelltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/shell"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	schemaVersion = 1
	stageEX0      = "EX0"
)

type report struct {
	SchemaVersion int               `json:"schema_version"`
	Stage         string            `json:"stage"`
	Status        string            `json:"status"`
	BaseCommit    string            `json:"base_commit"`
	Catalog       catalogMetrics    `json:"catalog"`
	Contracts     contractMetrics   `json:"contracts"`
	Risks         riskMetrics       `json:"risks"`
	Hotspots      map[string]int    `json:"hotspots"`
	KnownGaps     []string          `json:"known_gaps"`
	Commands      map[string]string `json:"commands"`
}

type catalogMetrics struct {
	ModelVisibleExecutionTools int      `json:"model_visible_execution_tools"`
	InputSchemaBytes           int      `json:"input_schema_bytes"`
	SerialExecutionTools       int      `json:"serial_execution_tools"`
	Names                      []string `json:"names"`
}

type contractMetrics struct {
	CatalogAuthorityBound  bool `json:"catalog_authority_bound"`
	ResourceClaimsEnforced bool `json:"resource_claims_enforced"`
	ResultHandlesAvailable bool `json:"result_handles_available"`
	TypedAdapterAvailable  bool `json:"typed_adapter_available"`
}

type riskMetrics struct {
	ForegroundOutputBounded      bool `json:"foreground_output_bounded"`
	ApprovalWaitHoldsAdmission   bool `json:"approval_wait_holds_admission"`
	SecurityReadsResultMetadata  bool `json:"security_reads_result_metadata"`
	CancellationLacksDisposition bool `json:"cancellation_lacks_disposition"`
	SessionOwnerEnforced         bool `json:"session_owner_enforced"`
	EventDrivenSessionWait       bool `json:"event_driven_session_wait"`
	UnifiedProcessProtocol       bool `json:"unified_process_protocol"`
	FairBudgetAdmission          bool `json:"fair_budget_admission"`
	FairResourceClaims           bool `json:"fair_resource_claims"`
	TerminalOutcomeOwned         bool `json:"terminal_outcome_owned"`
	TeardownObserved             bool `json:"teardown_observed"`
	DetachedCancelCleanup        bool `json:"detached_cancel_cleanup"`
}

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String(
		"baseline",
		"docs/tool-execution-ex0-baseline.json",
		"EX0 baseline JSON",
	)
	reportPath := flag.String("report", "", "optional measured report path")
	writeBaseline := flag.Bool("write-baseline", false, "replace the baseline with current metrics")
	baseCommit := flag.String("base-commit", "", "baseline source commit")
	flag.Parse()

	measured, err := measure(*root, *baseCommit)
	if err == nil && *writeBaseline {
		err = writeJSON(filepath.Join(*root, *baselinePath), measured)
	} else if err == nil {
		var baseline report
		baseline, err = readReport(filepath.Join(*root, *baselinePath))
		if err == nil {
			err = validateCandidate(baseline, measured)
		}
	}
	if reportErr := writeOptionalReport(*root, *reportPath, measured); err == nil {
		err = reportErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf(
		"tool execution baseline passed: tools=%d schema_bytes=%d\n",
		measured.Catalog.ModelVisibleExecutionTools,
		measured.Catalog.InputSchemaBytes,
	)
}

func measure(root, baseCommit string) (report, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return report{}, err
	}
	if baseCommit == "" {
		baseCommit = gitCommit(absolute)
	}
	catalog, contracts, err := measureCatalog()
	if err != nil {
		return report{}, err
	}
	risks, err := measureRisks(absolute, catalog)
	if err != nil {
		return report{}, err
	}
	hotspots, err := measureHotspots(absolute)
	if err != nil {
		return report{}, err
	}
	typedPath := filepath.Join(absolute, "internal/adapter/tool/typed/typed.go")
	contracts.TypedAdapterAvailable = regularFile(typedPath)
	knownGaps := []string{"tool_outputs_depend_on_stringly_typed_metadata"}
	if !risks.UnifiedProcessProtocol {
		knownGaps = append(
			knownGaps,
			"process_lifecycle_is_split_across_multiple_model_visible_protocols",
		)
	}
	if !risks.SessionOwnerEnforced {
		knownGaps = append(
			knownGaps,
			"session_manager_records_but_does_not_enforce_thread_ownership",
		)
	}
	if !risks.EventDrivenSessionWait {
		knownGaps = append(
			knownGaps,
			"session_wait_polls_on_a_ten_millisecond_ticker",
		)
	}
	if !risks.ForegroundOutputBounded {
		knownGaps = append(
			[]string{"foreground_process_output_is_accumulated_before_result_admission"},
			knownGaps...,
		)
	}
	if risks.ApprovalWaitHoldsAdmission {
		knownGaps = append(
			[]string{"approval_wait_holds_the_engine_parallel_policy_gate"},
			knownGaps...,
		)
	}
	if risks.CancellationLacksDisposition {
		knownGaps = append(
			knownGaps,
			"tool_cancellation_has_no_explicit_teardown_disposition",
		)
	}
	if !risks.FairBudgetAdmission || !risks.FairResourceClaims {
		knownGaps = append(knownGaps, "execution_admission_or_claims_are_not_fair")
	}
	if !risks.TerminalOutcomeOwned || !risks.TeardownObserved {
		knownGaps = append(knownGaps, "cancellation_terminal_or_teardown_is_not_observable")
	}
	if !risks.DetachedCancelCleanup {
		knownGaps = append(knownGaps, "canceled_detached_launch_can_leave_an_orphan")
	}
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageEX0,
		Status:        "baseline_frozen",
		BaseCommit:    baseCommit,
		Catalog:       catalog,
		Contracts:     contracts,
		Risks:         risks,
		Hotspots:      hotspots,
		KnownGaps:     knownGaps,
		Commands: map[string]string{
			"baseline": "make tool-execution-ex0",
			"update":   "make tool-execution-ex0-update",
		},
	}, nil
}

func measureCatalog() (catalogMetrics, contractMetrics, error) {
	root, err := os.MkdirTemp("", "codehelper-tool-exec-baseline-workspace-")
	if err != nil {
		return catalogMetrics{}, contractMetrics{}, err
	}
	defer os.RemoveAll(root)
	manager := process.NewSessionManager(4096)
	defer manager.CloseAll()
	registry := tool.NewRegistry(nil, tool.NewResultStore(32<<10))
	if err := shelltool.RegisterWithManagerAndBackend(
		registry,
		root,
		manager,
		baselineBackend{},
	); err != nil {
		return catalogMetrics{}, contractMetrics{}, err
	}
	if closer, ok := registry.SandboxBackend().(interface{ Close() error }); ok {
		defer closer.Close()
	}

	descriptors := registry.Descriptors(tool.VisibleModel)
	metrics := catalogMetrics{}
	for _, descriptor := range descriptors {
		if descriptor.Name == "result_get" {
			continue
		}
		encoded, err := json.Marshal(descriptor.InputSchema)
		if err != nil {
			return catalogMetrics{}, contractMetrics{}, err
		}
		metrics.ModelVisibleExecutionTools++
		metrics.InputSchemaBytes += len(encoded)
		if descriptor.ParallelPolicy == tool.ParallelSerial {
			metrics.SerialExecutionTools++
		}
		metrics.Names = append(metrics.Names, descriptor.Name)
	}
	sort.Strings(metrics.Names)

	snapshot, err := registry.Snapshot()
	if err != nil {
		return catalogMetrics{}, contractMetrics{}, err
	}
	entry, found := snapshot.Lookup("exec_command")
	authorityBound := false
	if found {
		binding, bound := snapshot.Binding(entry.Name)
		_, _, _, resolveErr := registry.ResolveBound(
			entry.Name,
			binding,
		)
		authorityBound = bound && resolveErr == nil
	}
	large := tool.Result{Content: strings.Repeat("x", 64<<10)}
	admitted, _ := registry.AdmitResult("exec_command", large)
	contracts := contractMetrics{
		CatalogAuthorityBound:  authorityBound,
		ResourceClaimsEnforced: probeClaims(),
		ResultHandlesAvailable: admitted.Truncated && admitted.Handle != "",
	}
	return metrics, contracts, nil
}

func probeClaims() bool {
	claims := tool.NewClaims()
	resource := tool.Resource{
		Kind: "file", Path: "/baseline/file", Access: tool.AccessWrite,
	}
	release, err := claims.AcquireResources(context.Background(), []tool.Resource{resource})
	if err != nil {
		return false
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = claims.AcquireResources(ctx, []tool.Resource{{
		Kind: "file", Path: resource.Path, Access: tool.AccessRead,
	}})
	return errors.Is(err, context.DeadlineExceeded)
}

func measureRisks(root string, catalog catalogMetrics) (riskMetrics, error) {
	processPath := filepath.Join(root, "internal/platform/process/process.go")
	sessionPath := filepath.Join(root, "internal/platform/process/session.go")
	handlerPath := filepath.Join(root, "internal/runtime/agent/engine/tool_handler.go")
	guardPath := filepath.Join(root, "internal/adapter/tool/guard/guard.go")
	escalationPath := filepath.Join(root, "internal/adapter/tool/guard/escalation.go")
	executionPath := filepath.Join(root, "internal/adapter/tool/execution.go")
	schedulerPath := filepath.Join(root, "internal/adapter/tool/execution_budget.go")
	toolPath := filepath.Join(root, "internal/adapter/tool/tool.go")
	protocolPath := filepath.Join(root, "internal/adapter/tool/shell/protocol.go")
	foregroundBounded, err := foregroundCollectorIsBounded(processPath)
	if err != nil {
		return riskMetrics{}, err
	}
	eventDriven, ownerEnforced, err := inspectSessionManager(sessionPath)
	if err != nil {
		return riskMetrics{}, err
	}
	handlerSource, err := os.ReadFile(handlerPath)
	if err != nil {
		return riskMetrics{}, err
	}
	guardSource, err := os.ReadFile(guardPath)
	if err != nil {
		return riskMetrics{}, err
	}
	escalationSource, err := os.ReadFile(escalationPath)
	if err != nil {
		return riskMetrics{}, err
	}
	executionSource, err := os.ReadFile(executionPath)
	if err != nil {
		return riskMetrics{}, err
	}
	schedulerSource, err := os.ReadFile(schedulerPath)
	if err != nil {
		return riskMetrics{}, err
	}
	toolSource, err := os.ReadFile(toolPath)
	if err != nil {
		return riskMetrics{}, err
	}
	protocolSource, err := os.ReadFile(protocolPath)
	if err != nil {
		return riskMetrics{}, err
	}
	processSource, err := os.ReadFile(processPath)
	if err != nil {
		return riskMetrics{}, err
	}
	securityReadsMetadata := bytes.Contains(
		guardSource,
		[]byte(`Metadata["error_category"]`),
	) || bytes.Contains(escalationSource, []byte(`Metadata["sandbox_denied"]`))
	return riskMetrics{
		ForegroundOutputBounded: foregroundBounded,
		ApprovalWaitHoldsAdmission: bytes.Contains(
			handlerSource,
			[]byte("release, err := sched.Admit"),
		),
		SecurityReadsResultMetadata: securityReadsMetadata,
		CancellationLacksDisposition: !bytes.Contains(
			executionSource,
			[]byte("DispositionWaitForTeardown"),
		),
		SessionOwnerEnforced:   ownerEnforced,
		EventDrivenSessionWait: eventDriven,
		UnifiedProcessProtocol: catalog.ModelVisibleExecutionTools <= 3,
		FairBudgetAdmission: bytes.Contains(
			schedulerSource,
			[]byte("[]*budgetWaiter"),
		) && !bytes.Contains(schedulerSource, []byte("sync.RWMutex")),
		FairResourceClaims: bytes.Contains(
			toolSource,
			[]byte("conflictsEarlierQueued"),
		),
		TerminalOutcomeOwned: bytes.Contains(
			executionSource,
			[]byte("TerminalStatus"),
		) && bytes.Contains(executionSource, []byte("TerminalOwner")),
		TeardownObserved: bytes.Contains(
			processSource,
			[]byte("OnTeardown"),
		) && bytes.Contains(executionSource, []byte("TeardownMS")),
		DetachedCancelCleanup: bytes.Contains(
			protocolSource,
			[]byte("p.manager.Close(id, threadID)"),
		),
	}, nil
}

func foregroundCollectorIsBounded(path string) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false, err
	}
	unbounded := false
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.TypeSpec)
		if !ok || spec.Name.Name != "observedBuffer" {
			return true
		}
		structure, ok := spec.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range structure.Fields.List {
			selector, ok := field.Type.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			packageName, packageOK := selector.X.(*ast.Ident)
			if packageOK && packageName.Name == "bytes" && selector.Sel.Name == "Buffer" {
				unbounded = true
			}
		}
		return false
	})
	return !unbounded, nil
}

func inspectSessionManager(path string) (eventDriven, ownerEnforced bool, err error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return false, false, err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
	if err != nil {
		return false, false, err
	}
	waitPolls := false
	operations := map[string]bool{
		"Write": false, "Read": false, "Wait": false, "Resize": false,
		"Signal": false, "Close": false,
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv == nil {
			continue
		}
		if function.Name.Name == "Wait" {
			waitPolls = functionContainsCall(function, "time", "NewTicker")
		}
		if _, tracked := operations[function.Name.Name]; tracked {
			operations[function.Name.Name] = functionContainsIdent(function, "threadID")
		}
	}
	ownerEnforced = true
	for _, checked := range operations {
		ownerEnforced = ownerEnforced && checked
	}
	return !waitPolls, ownerEnforced, nil
}

func functionContainsCall(function *ast.FuncDecl, packageName, name string) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		identifier, packageOK := selector.X.(*ast.Ident)
		if packageOK && identifier.Name == packageName && selector.Sel.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func functionContainsIdent(function *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(function.Body, func(node ast.Node) bool {
		identifier, ok := node.(*ast.Ident)
		if ok && identifier.Name == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func measureHotspots(root string) (map[string]int, error) {
	files := map[string]string{
		"guard_execute":       "internal/adapter/tool/guard/guard.go",
		"engine_tool_handler": "internal/runtime/agent/engine/tool_handler.go",
		"process_runner":      "internal/platform/process/process.go",
		"session_manager":     "internal/platform/process/session.go",
	}
	result := make(map[string]int, len(files)+1)
	for name, relative := range files {
		count, err := countLines(filepath.Join(root, filepath.FromSlash(relative)))
		if err != nil {
			return nil, err
		}
		result[name] = count
	}
	shellLines, err := countProductionLines(
		filepath.Join(root, "internal/adapter/tool/shell"),
	)
	if err != nil {
		return nil, err
	}
	result["shell_package"] = shellLines
	return result, nil
}

func countProductionLines(directory string) (int, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		count, err := countLines(filepath.Join(directory, entry.Name()))
		if err != nil {
			return 0, err
		}
		total += count
	}
	return total, nil
}

func countLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	return bytes.Count(data, []byte{'\n'}) + 1, nil
}

func validateCandidate(baseline, candidate report) error {
	if baseline.SchemaVersion != schemaVersion || baseline.Stage != stageEX0 {
		return errors.New("tool execution baseline has an unsupported schema or stage")
	}
	if candidate.Catalog.ModelVisibleExecutionTools >
		baseline.Catalog.ModelVisibleExecutionTools {
		return fmt.Errorf(
			"model-visible execution tools regressed: baseline=%d candidate=%d",
			baseline.Catalog.ModelVisibleExecutionTools,
			candidate.Catalog.ModelVisibleExecutionTools,
		)
	}
	if candidate.Catalog.InputSchemaBytes > baseline.Catalog.InputSchemaBytes {
		return fmt.Errorf(
			"execution schema bytes regressed: baseline=%d candidate=%d",
			baseline.Catalog.InputSchemaBytes,
			candidate.Catalog.InputSchemaBytes,
		)
	}
	if candidate.Catalog.SerialExecutionTools >
		baseline.Catalog.SerialExecutionTools {
		return fmt.Errorf(
			"serial execution tools regressed: baseline=%d candidate=%d",
			baseline.Catalog.SerialExecutionTools,
			candidate.Catalog.SerialExecutionTools,
		)
	}
	if !candidate.Contracts.CatalogAuthorityBound ||
		!candidate.Contracts.ResourceClaimsEnforced ||
		!candidate.Contracts.ResultHandlesAvailable ||
		!candidate.Contracts.TypedAdapterAvailable {
		return errors.New("tool execution safety contract regressed")
	}
	return nil
}

func readReport(path string) (report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report{}, err
	}
	var value report
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return report{}, err
	}
	return value, nil
}

func writeOptionalReport(root, path string, value report) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return writeJSON(filepath.Join(root, path), value)
}

func writeJSON(path string, value report) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func gitCommit(root string) string {
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

type baselineBackend struct{}

func (baselineBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "baseline", Backend: "baseline",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (baselineBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedWritePaths = append([]string(nil), command.WorkspaceWritePaths...)
	command.PreparedNetworkDenied = command.DenyNetwork
	return command, nil
}
