// Command securitygovernancebaseline records and checks the security-governance surface.
package main

import (
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
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/controlplane"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	schemaVersion = 1
	stageSG0      = "SG0"
)

type report struct {
	SchemaVersion int               `json:"schema_version"`
	Stage         string            `json:"stage"`
	Status        string            `json:"status"`
	BaseCommit    string            `json:"base_commit"`
	Reference     reference         `json:"reference"`
	Controls      controls          `json:"controls"`
	AttackCases   []attackCase      `json:"attack_cases"`
	KnownGaps     []string          `json:"known_gaps"`
	Commands      map[string]string `json:"commands"`
}

type reference struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

// Every field is monotonic: false may become true, while true may not regress.
type controls struct {
	GuardValidatesBeforePolicy     bool `json:"guard_validates_before_policy"`
	StrongSandboxFailsClosed       bool `json:"strong_sandbox_fails_closed"`
	PreparedPolicyIdentityVerified bool `json:"prepared_policy_identity_verified"`
	ApprovalFingerprintBound       bool `json:"approval_fingerprint_bound"`
	ExactWorkspaceWritesBounded    bool `json:"exact_workspace_writes_bounded"`
	TeardownReceiptOwned           bool `json:"teardown_receipt_owned"`
	ControlPlaneProtected          bool `json:"control_plane_protected"`
	AuthorityStoreOutsideWorkspace bool `json:"authority_store_outside_workspace"`
	UnifiedPermissionProfile       bool `json:"unified_permission_profile"`
	RestrictedProcessEgress        bool `json:"restricted_process_egress"`
	ManagedNetworkProxy            bool `json:"managed_network_proxy"`
	TypedSandboxDenialProducer     bool `json:"typed_sandbox_denial_producer"`
	CoherentSandboxEscalation      bool `json:"coherent_sandbox_escalation"`
	LinuxSyscallFilter             bool `json:"linux_syscall_filter"`
	AttemptPermissionDigest        bool `json:"attempt_permission_digest"`
}

type attackCase struct {
	ID              string `json:"id"`
	Threat          string `json:"threat"`
	CurrentOutcome  string `json:"current_outcome"`
	RequiredOutcome string `json:"required_outcome"`
	Stage           string `json:"target_stage"`
}

var gapNames = map[string]string{
	"ControlPlaneProtected":          "workspace_control_plane_is_model_writable",
	"AuthorityStoreOutsideWorkspace": "durable_authority_is_stored_inside_workspace",
	"UnifiedPermissionProfile":       "effective_authority_is_split_across_independent_flags",
	"RestrictedProcessEgress":        "approved_process_receives_broad_network_access",
	"ManagedNetworkProxy":            "process_egress_has_no_managed_domain_policy",
	"TypedSandboxDenialProducer":     "real_process_path_has_no_typed_sandbox_denial_producer",
	"CoherentSandboxEscalation":      "sandbox_none_retry_conflicts_with_process_restrictions",
	"LinuxSyscallFilter":             "linux_sandbox_has_no_no_new_privs_seccomp_layer",
	"AttemptPermissionDigest":        "attempt_receipt_does_not_bind_effective_permissions",
}

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String(
		"baseline",
		"docs/security-governance-sg0-baseline.json",
		"SG0 baseline JSON",
	)
	reportPath := flag.String("report", "", "optional measured report path")
	writeBaseline := flag.Bool("write-baseline", false, "replace the baseline with current metrics")
	baseCommit := flag.String("base-commit", "", "baseline source commit")
	flag.Parse()

	measured, err := measure(*root, *baseCommit)
	if err == nil && *writeBaseline {
		err = updateBaseline(filepath.Join(*root, *baselinePath), measured)
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
		"security governance baseline passed: controls=%d/%d known_gaps=%d\n",
		countEnabled(measured.Controls),
		reflect.TypeOf(measured.Controls).NumField(),
		len(measured.KnownGaps),
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
	measured, err := measureControls(absolute)
	if err != nil {
		return report{}, err
	}
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageSG0,
		Status:        "baseline_frozen",
		BaseCommit:    baseCommit,
		Reference: reference{
			Repository: "../codex",
			Commit:     "3bbf1fe75701c97fb190e0867002ba2d9dbda5db",
		},
		Controls:    measured,
		AttackCases: securityAttackCases(measured),
		KnownGaps:   knownGaps(measured),
		Commands: map[string]string{
			"baseline": "make security-governance-sg0",
			"update":   "make security-governance-sg0-update",
		},
	}, nil
}

func measureControls(root string) (controls, error) {
	classifier, err := controlplane.New(root)
	if err != nil {
		return controls{}, err
	}
	controlPlaneProtected := errors.Is(
		classifier.CheckWrite(".codehelper/permissions.toml", false),
		controlplane.ErrProtected,
	)

	invalidDenied := policy.DefaultRuntime(
		policy.ModeAct,
		policy.PermissionBypass,
	).Evaluate(policy.Invocation{
		CallID: "sg0-invalid", Tool: "file_read",
		Capability: tool.CapabilityRead,
	}).Action == policy.ActionDeny

	first, err := policy.NewApprovalRequest(policy.Invocation{
		CallID: "sg0-approval", Tool: "exec_command",
		Arguments:  json.RawMessage(`{"command":"go test ./..."}`),
		Resources:  []tool.Resource{{Kind: "process", ID: "go test ./...", Access: tool.AccessWrite}},
		Capability: tool.CapabilityProcess,
		Access:     tool.AccessWrite,
		Sandbox:    tool.SandboxStrong,
		Validated:  true,
	}, time.Now().Add(time.Minute))
	if err != nil {
		return controls{}, err
	}
	second, err := policy.NewApprovalRequest(policy.Invocation{
		CallID: "sg0-approval", Tool: "exec_command",
		Arguments:  json.RawMessage(`{"command":"go test ./internal/security/..."}`),
		Resources:  []tool.Resource{{Kind: "process", ID: "go test ./internal/security/...", Access: tool.AccessWrite}},
		Capability: tool.CapabilityProcess,
		Access:     tool.AccessWrite,
		Sandbox:    tool.SandboxStrong,
		Validated:  true,
	}, first.ExpiresAt)
	if err != nil {
		return controls{}, err
	}

	networkBroad, err := hasCompositeBoolField(
		filepath.Join(root, "internal/runtime/app/wire/modules_core.go"),
		"Options",
		"AllowNetwork",
		true,
	)
	if err != nil {
		return controls{}, err
	}
	typedDenial, err := sourcesContainAll(map[string][]string{
		filepath.Join(root, "internal/security/sandbox/denial.go"): {
			"type Denial struct", "type DenialError struct", "func Denied(",
		},
		filepath.Join(root, "internal/platform/process/process.go"): {
			"sandbox.Denied", "ReasonPathWriteNotAuthorized",
			"ReasonNetworkNotAuthorized", "ReasonProcessNotAuthorized",
		},
		filepath.Join(root, "internal/adapter/tool/execution.go"): {
			"SandboxDenied *sandbox.Denial",
		},
	})
	if err != nil {
		return controls{}, err
	}
	escalationCoherent, err := sourcesContainAll(map[string][]string{
		filepath.Join(root, "internal/adapter/tool/guard/pipeline_attempt.go"): {
			"authority.RequestFromDenial", "authority.Amend",
			"retryProfile = &amended",
		},
		filepath.Join(root, "internal/adapter/tool/guard/escalation.go"): {
			"return backend, true",
		},
		filepath.Join(root, "internal/adapter/tool/guard/guard.go"): {
			"opts.AdditionalPermission != nil", "policy.RiskCritical",
		},
	})
	if err != nil {
		return controls{}, err
	}
	if escalationCoherent {
		legacyFallback, readErr := sourceContainsAny(
			filepath.Join(root, "internal/adapter/tool/guard/pipeline_attempt.go"),
			"retrySandbox", "ApprovalReasonSandbox",
		)
		if readErr != nil {
			return controls{}, readErr
		}
		escalationCoherent = !legacyFallback
	}
	permissionProfile, err := hasTypeInTree(
		filepath.Join(root, "internal/security"),
		"EffectivePermissionProfile",
	)
	if err != nil {
		return controls{}, err
	}
	authorityPipeline, err := sourcesContainAll(map[string][]string{
		filepath.Join(root, "internal/adapter/tool/guard/authority.go"): {
			"authority.Compile",
		},
		filepath.Join(root, "internal/adapter/tool/guard/pipeline_attempt.go"): {
			"authority.WithProfile", "sandbox.WithExecutionAuthority",
		},
		filepath.Join(root, "internal/platform/process/process.go"): {
			"ExecutionAuthorityFromContext", "PreparedAuthorityDigest",
		},
		filepath.Join(root, "internal/security/sandbox/backend.go"): {
			"PreparedAuthorityDigest", "AuthorityDigest",
		},
	})
	if err != nil {
		return controls{}, err
	}
	managedProxy, err := hasTypeInTree(
		filepath.Join(root, "internal/security"),
		"ManagedNetworkProxy",
	)
	if err != nil {
		return controls{}, err
	}
	seccomp, err := sourcesContainAll(map[string][]string{
		filepath.Join(root, "internal/security/sandbox/seccomp_linux.go"): {
			"PR_SET_NO_NEW_PRIVS", "PR_SET_SECCOMP", "SECCOMP_MODE_FILTER",
			"SYS_PTRACE", "SYS_PROCESS_VM_READV", "SYS_IO_URING_SETUP",
			"SYS_CLONE3", "SYS_SOCKET",
		},
		filepath.Join(root, "internal/security/sandbox/landlock_helper_linux.go"): {
			"runtime.LockOSThread", "applyLinuxSyscallPolicy",
			"landlock.V3.RestrictPaths", "syscall.Exec",
		},
	})
	if err != nil {
		return controls{}, err
	}
	attemptDigest, err := structHasField(
		filepath.Join(root, "internal/adapter/tool/execution.go"),
		"AttemptReceipt",
		"PermissionDigest",
	)
	if err != nil {
		return controls{}, err
	}
	preparedIdentity, err := sourceContainsAll(
		filepath.Join(root, "internal/platform/process/process.go"),
		"PreparedPolicyID",
		"PreparedStrength",
		"unverified prepared policy",
	)
	if err != nil {
		return controls{}, err
	}
	teardownOwned, err := structHasFields(
		filepath.Join(root, "internal/adapter/tool/execution.go"),
		"AttemptReceipt",
		"TerminalOwner",
		"Teardown",
	)
	if err != nil {
		return controls{}, err
	}

	identityRoot, err := os.MkdirTemp("", "codehelper-sg0-authority-")
	if err != nil {
		return controls{}, err
	}
	defer os.RemoveAll(identityRoot)
	workspace := filepath.Join(identityRoot, "workspace")
	dataDir := filepath.Join(identityRoot, "state")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		return controls{}, err
	}
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		return controls{}, err
	}
	permissionPath, err := permissions.Path(dataDir, workspace)
	if err != nil {
		return controls{}, err
	}
	authorityOutside := !pathWithin(workspace, permissionPath)
	return controls{
		GuardValidatesBeforePolicy:     invalidDenied,
		StrongSandboxFailsClosed:       sandbox.RequireStrong(nil) != nil,
		PreparedPolicyIdentityVerified: preparedIdentity,
		ApprovalFingerprintBound:       first.Fingerprint != second.Fingerprint,
		ExactWorkspaceWritesBounded:    sandbox.MaxExactWorkspaceWritePaths > 0,
		TeardownReceiptOwned:           teardownOwned,
		ControlPlaneProtected:          controlPlaneProtected,
		AuthorityStoreOutsideWorkspace: authorityOutside,
		UnifiedPermissionProfile:       permissionProfile && authorityPipeline,
		RestrictedProcessEgress:        !networkBroad,
		ManagedNetworkProxy:            managedProxy,
		TypedSandboxDenialProducer:     typedDenial,
		CoherentSandboxEscalation:      escalationCoherent,
		LinuxSyscallFilter:             seccomp,
		AttemptPermissionDigest:        attemptDigest,
	}, nil
}

func securityAttackCases(value controls) []attackCase {
	outcome := func(control bool) string {
		if control {
			return "blocked"
		}
		return "exposed"
	}
	return []attackCase{
		{
			ID: "SG-ATTACK-001", Threat: "模型通过普通文件工具改写持久授权或 Constitution",
			CurrentOutcome: outcome(value.ControlPlaneProtected), RequiredOutcome: "blocked",
			Stage: "SG1",
		},
		{
			ID: "SG-ATTACK-002", Threat: "获批进程绕过工具层 Egress Gate 直连任意目标",
			CurrentOutcome:  outcome(value.RestrictedProcessEgress && value.ManagedNetworkProxy),
			RequiredOutcome: "blocked", Stage: "SG4",
		},
		{
			ID: "SG-ATTACK-003", Threat: "sandbox denial 无法形成 typed signal 并申请最小增权",
			CurrentOutcome:  outcome(value.TypedSandboxDenialProducer),
			RequiredOutcome: "blocked", Stage: "SG3",
		},
		{
			ID: "SG-ATTACK-004", Threat: "sandbox none 重试仍携带 backend-only 进程约束",
			CurrentOutcome:  outcome(value.CoherentSandboxEscalation),
			RequiredOutcome: "blocked", Stage: "SG3",
		},
		{
			ID: "SG-ATTACK-005", Threat: "Linux 子进程使用 ptrace、process_vm 或 io_uring 扩大攻击面",
			CurrentOutcome:  outcome(value.LinuxSyscallFilter),
			RequiredOutcome: "blocked", Stage: "SG5",
		},
	}
}

func knownGaps(value controls) []string {
	typ := reflect.TypeOf(value)
	current := reflect.ValueOf(value)
	var gaps []string
	for fieldName, gap := range gapNames {
		field, ok := typ.FieldByName(fieldName)
		if !ok {
			panic("unknown control field " + fieldName)
		}
		if !current.FieldByIndex(field.Index).Bool() {
			gaps = append(gaps, gap)
		}
	}
	sort.Strings(gaps)
	return gaps
}

func countEnabled(value controls) int {
	current := reflect.ValueOf(value)
	count := 0
	for index := 0; index < current.NumField(); index++ {
		if current.Field(index).Bool() {
			count++
		}
	}
	return count
}

func validateCandidate(baseline, candidate report) error {
	if baseline.SchemaVersion != schemaVersion || baseline.Stage != stageSG0 {
		return errors.New("security governance baseline has an unsupported schema or stage")
	}
	if candidate.SchemaVersion != schemaVersion || candidate.Stage != stageSG0 {
		return errors.New("security governance candidate has an unsupported schema or stage")
	}
	baselineValue := reflect.ValueOf(baseline.Controls)
	candidateValue := reflect.ValueOf(candidate.Controls)
	typ := reflect.TypeOf(baseline.Controls)
	var regressions []string
	for index := 0; index < baselineValue.NumField(); index++ {
		if baselineValue.Field(index).Bool() && !candidateValue.Field(index).Bool() {
			regressions = append(regressions, typ.Field(index).Name)
		}
	}
	if len(regressions) != 0 {
		return fmt.Errorf(
			"security governance controls regressed: %s",
			strings.Join(regressions, ", "),
		)
	}
	if len(candidate.AttackCases) != len(baseline.AttackCases) {
		return fmt.Errorf(
			"security attack corpus changed from %d to %d cases",
			len(baseline.AttackCases),
			len(candidate.AttackCases),
		)
	}
	for index, baselineCase := range baseline.AttackCases {
		if candidate.AttackCases[index].ID != baselineCase.ID {
			return fmt.Errorf(
				"security attack corpus case %d changed from %q to %q",
				index,
				baselineCase.ID,
				candidate.AttackCases[index].ID,
			)
		}
	}
	return nil
}

func hasCompositeBoolField(path, typeName, fieldName string, want bool) (bool, error) {
	file, err := parseFile(path)
	if err != nil {
		return false, err
	}
	return fileHasCompositeBoolField(file, typeName, fieldName, want), nil
}

func productionCompositeBoolField(
	root,
	typeName,
	fieldName string,
	want bool,
) (bool, error) {
	found := false
	err := walkProductionGo(root, func(_ string, file *ast.File) {
		if fileHasCompositeBoolField(file, typeName, fieldName, want) {
			found = true
		}
	})
	return found, err
}

func fileHasCompositeBoolField(
	file *ast.File,
	typeName,
	fieldName string,
	want bool,
) bool {
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || expressionName(literal.Type) != typeName {
			return true
		}
		for _, element := range literal.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok || expressionName(pair.Key) != fieldName {
				continue
			}
			value, ok := pair.Value.(*ast.Ident)
			if ok && (value.Name == "true") == want {
				found = true
				return false
			}
		}
		return !found
	})
	return found
}

func hasTypeInTree(root, name string) (bool, error) {
	found := false
	err := walkProductionGo(root, func(_ string, file *ast.File) {
		for _, declaration := range file.Decls {
			general, ok := declaration.(*ast.GenDecl)
			if !ok || general.Tok != token.TYPE {
				continue
			}
			for _, specification := range general.Specs {
				typeSpec, ok := specification.(*ast.TypeSpec)
				if ok && typeSpec.Name.Name == name {
					found = true
				}
			}
		}
	})
	return found, err
}

func productionImportsContain(root, fragment string) (bool, error) {
	found := false
	err := walkProductionGo(root, func(_ string, file *ast.File) {
		for _, imported := range file.Imports {
			if strings.Contains(strings.Trim(imported.Path.Value, `"`), fragment) {
				found = true
			}
		}
	})
	return found, err
}

func structHasField(path, typeName, fieldName string) (bool, error) {
	return structHasFields(path, typeName, fieldName)
}

func structHasFields(path, typeName string, fieldNames ...string) (bool, error) {
	file, err := parseFile(path)
	if err != nil {
		return false, err
	}
	wanted := make(map[string]bool, len(fieldNames))
	for _, name := range fieldNames {
		wanted[name] = false
	}
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.TYPE {
			continue
		}
		for _, specification := range general.Specs {
			typeSpec, ok := specification.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != typeName {
				continue
			}
			structure, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				return false, nil
			}
			for _, field := range structure.Fields.List {
				for _, name := range field.Names {
					if _, ok := wanted[name.Name]; ok {
						wanted[name.Name] = true
					}
				}
			}
		}
	}
	for _, present := range wanted {
		if !present {
			return false, nil
		}
	}
	return true, nil
}

func sourceContainsAll(path string, needles ...string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	for _, needle := range needles {
		if !strings.Contains(string(data), needle) {
			return false, nil
		}
	}
	return true, nil
}

func sourceContainsAny(path string, needles ...string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	for _, needle := range needles {
		if strings.Contains(string(data), needle) {
			return true, nil
		}
	}
	return false, nil
}

func sourcesContainAll(paths map[string][]string) (bool, error) {
	for path, needles := range paths {
		present, err := sourceContainsAll(path, needles...)
		if err != nil || !present {
			return false, err
		}
	}
	return true, nil
}

func walkProductionGo(root string, visit func(string, *ast.File)) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parseFile(path)
		if parseErr != nil {
			return parseErr
		}
		visit(path, file)
		return nil
	})
}

func parseFile(path string) (*ast.File, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return file, nil
}

func expressionName(expression ast.Expr) string {
	switch value := expression.(type) {
	case *ast.Ident:
		return value.Name
	case *ast.SelectorExpr:
		return value.Sel.Name
	default:
		return ""
	}
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative != ".." &&
		relative != "." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readReport(path string) (report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report{}, err
	}
	var result report
	if err := json.Unmarshal(data, &result); err != nil {
		return report{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return result, nil
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
	temporary, err := os.CreateTemp(filepath.Dir(path), ".security-governance-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err = temporary.Write(data); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func updateBaseline(path string, candidate report) error {
	baseline, err := readReport(path)
	if err == nil {
		if validationErr := validateCandidate(baseline, candidate); validationErr != nil {
			return fmt.Errorf(
				"refuse to relax security governance baseline: %w",
				validationErr,
			)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeJSON(path, candidate)
}

func writeOptionalReport(root, path string, value report) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return writeJSON(filepath.Join(root, path), value)
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
