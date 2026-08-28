package quality

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/controlmatrix"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
	"github.com/fwtllh-png/CodeHelper/internal/testutil/tooltest"
)

func TestQualityToolsReturnIndependentStructuredResults(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithBackend(registry, t.TempDir(), qualityTestBackend{}); err != nil {
		t.Fatal(err)
	}
	expectations := map[string]string{
		"quality_test":        "summary",
		"quality_diagnostics": "diagnostics",
		"quality_review":      "findings",
		"quality_verify":      "checks",
	}
	for name, field := range expectations {
		t.Run(name, func(t *testing.T) {
			result, err := tooltest.Execute(t.Context(), registry, tool.Call{
				Name: name, Arguments: json.RawMessage(`{"command":"printf fixture"}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
				t.Fatal(err)
			}
			if payload["schema_version"] != float64(1) || payload["status"] != "passed" {
				t.Fatalf("payload = %+v", payload)
			}
			if _, exists := payload[field]; !exists {
				t.Fatalf("payload missing %q: %+v", field, payload)
			}
		})
	}
}

func TestQualityVerificationToolsDeclareGuardedNetworkTargets(t *testing.T) {
	for _, kind := range []string{"quality_test", "quality_verify"} {
		descriptor := (&Tool{kind: kind}).Descriptor()
		if descriptor.ResourceResolver.NetworkTargetsField != "network_targets" {
			t.Fatalf("%s network target field = %q", kind,
				descriptor.ResourceResolver.NetworkTargetsField)
		}
		if descriptor.ResourceResolver.LoopbackField != "allow_loopback" {
			t.Fatalf("%s loopback field = %q", kind,
				descriptor.ResourceResolver.LoopbackField)
		}
		properties, _ := descriptor.InputSchema["properties"].(map[string]any)
		network, ok := properties["network_targets"].(map[string]any)
		if !ok || network["maxItems"] != 32 {
			t.Fatalf("%s network schema = %#v", kind, properties["network_targets"])
		}
		loopback, ok := properties["allow_loopback"].(map[string]any)
		if !ok || loopback["type"] != "boolean" {
			t.Fatalf("%s loopback schema = %#v", kind, properties["allow_loopback"])
		}
		if descriptor.ResourceResolver.ReadPathsField != "covered_paths" {
			t.Fatalf("%s covered paths field = %q", kind,
				descriptor.ResourceResolver.ReadPathsField)
		}
	}
}

func TestProcessSmokeIsUnavailableUntilDesktopBrokerExists(t *testing.T) {
	descriptor := (&processSmokeTool{}).Descriptor()
	if descriptor.Availability != tool.AvailabilityUnavailable ||
		descriptor.UnavailableReason != ProcessSmokeUnavailableReason {
		t.Fatalf("process smoke descriptor = %+v", descriptor)
	}
}

func TestQualityDiagnosticsAndReviewParseCommandOutput(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithBackend(registry, t.TempDir(), qualityTestBackend{}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		command    string
		field      string
		wantValues map[string]any
	}{
		{
			name:    "quality_diagnostics",
			command: `printf 'src/main.go:4:7: error: undefined name\n' >&2; exit 1`,
			field:   "diagnostics",
			wantValues: map[string]any{
				"file": "src/main.go", "line": float64(4), "column": float64(7),
				"severity": "error", "message": "undefined name",
			},
		},
		{
			name:    "quality_review",
			command: `printf 'src/review.go:12: high: unchecked error\n'; exit 1`,
			field:   "findings",
			wantValues: map[string]any{
				"file": "src/review.go", "line": float64(12),
				"severity": "high", "message": "unchecked error",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(map[string]string{"command": test.command})
			if err != nil {
				t.Fatal(err)
			}
			result, err := tooltest.Execute(t.Context(), registry, tool.Call{
				Name: test.name, Arguments: raw,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !result.IsError {
				t.Fatalf("result = %+v, want command failure", result)
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
				t.Fatal(err)
			}
			items, ok := payload[test.field].([]any)
			if !ok || len(items) != 1 {
				t.Fatalf("%s = %#v", test.field, payload[test.field])
			}
			item := items[0].(map[string]any)
			for key, want := range test.wantValues {
				if item[key] != want {
					t.Fatalf("%s[%q] = %#v, want %#v", test.field, key, item[key], want)
				}
			}
		})
	}
}

func TestQualityCommandsUseFailFastShellSemantics(t *testing.T) {
	var executed string
	quality := &Tool{
		root: t.TempDir(), kind: "quality_test",
		run: func(_ context.Context, options process.Options) (process.Result, error) {
			executed = options.Command
			return process.Result{}, nil
		},
	}
	if _, err := quality.Execute(
		t.Context(),
		json.RawMessage(`{"command":"false; echo masked"}`),
	); err != nil {
		t.Fatal(err)
	}
	if executed != "set -e\nfalse; echo masked" {
		t.Fatalf("executed command = %q", executed)
	}
}

func TestProcessSmokeExpandsPrivateHomeButRequiresBrokerGrant(t *testing.T) {
	root := t.TempDir()
	privateHome := t.TempDir()
	path := filepath.Join(privateHome, "target", "fixture-app")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	path, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := sandbox.NewTrustedHostPathResolver(root, privateHome)
	if err != nil {
		t.Fatal(err)
	}
	smoke := &processSmokeTool{
		root: root, resolver: resolver,
	}
	executor, err := smoke.typedExecutor()
	if err != nil {
		t.Fatal(err)
	}
	expander, ok := executor.(tool.ArgumentExpander)
	if !ok {
		t.Fatal("process smoke executor does not expose argument expansion")
	}
	expanded, err := expander.ExpandArguments(t.Context(), json.RawMessage(
		`{"path":"target/fixture-app","covered_paths":["src/main.go"],"minimum_runtime_ms":1}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	var expandedInput processSmokeInput
	if err := json.Unmarshal(expanded, &expandedInput); err != nil {
		t.Fatal(err)
	}
	if expandedInput.Path != path {
		t.Fatalf("expanded path = %q, want %q", expandedInput.Path, path)
	}
	if _, err := executor.Execute(t.Context(), expanded); err == nil ||
		!strings.Contains(err.Error(), "Process Broker grant") {
		t.Fatalf("direct process smoke error = %v", err)
	}
}

type qualityTestBackend struct{}

func (qualityTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "passthrough",
		Available: true,
		Effective: controlmatrix.Matrix{
			FilesystemRead:  controlmatrix.FilesystemReadDeclaredRoots,
			FilesystemWrite: controlmatrix.FilesystemWriteExactPaths,
			Network:         controlmatrix.NetworkDenied,
			ProcessTree:     controlmatrix.ProcessTreeGroupKill,
			CrossProcess:    controlmatrix.CrossProcessRestricted,
			Syscall:         controlmatrix.SyscallDenyDangerous,
			IPC:             controlmatrix.IPCUnixOnly,
			PathIdentity:    controlmatrix.PathIdentityDescriptorRelative,
			ArtifactOrigin:  controlmatrix.ArtifactOriginVerifiedManifest,
			DurableRecovery: controlmatrix.DurableRecoveryExternalJournal,
		},
	}
}

func (qualityTestBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedControls = sandbox.CommandControls(
		qualityTestBackend{}.Capability(),
		sandbox.Policy{},
		command,
	)
	return command, nil
}

func TestQualityVerifierDetectsAndAuditsMixedEcosystems(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"go.mod", "Cargo.toml", "package.json", "pyproject.toml"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	quality := &Tool{
		root: root, kind: "quality_verify",
		run: func(_ context.Context, options process.Options) (process.Result, error) {
			return process.Result{Stdout: options.Command + " ok\n"}, nil
		},
	}
	result, err := quality.Execute(t.Context(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("result = %+v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	checks, ok := payload["checks"].([]any)
	if !ok || len(checks) != 4 {
		t.Fatalf("checks = %#v", payload["checks"])
	}
	for index, wantName := range []string{"go", "rust", "node", "python"} {
		check := checks[index].(map[string]any)
		if check["name"] != wantName || check["status"] != "passed" {
			t.Fatalf("check[%d] = %#v", index, check)
		}
		for _, field := range []string{"command", "exit_code", "stdout", "stderr"} {
			if _, exists := check[field]; !exists {
				t.Fatalf("check[%d] missing %s: %#v", index, field, check)
			}
		}
	}
}

func TestQualityVerifierDoesNotInferFailureKindFromOutput(t *testing.T) {
	quality := &Tool{
		root: t.TempDir(), kind: "quality_verify",
		run: func(context.Context, process.Options) (process.Result, error) {
			return process.Result{
				ExitCode: 1,
				Stderr: `go: example.org/dependency@v1.0.0: Get ` +
					`"https://proxy.internal.example/example.org/dependency/@v/v1.0.0.info": ` +
					`context deadline exceeded`,
			}, nil
		},
	}
	result, err := quality.Execute(
		t.Context(), json.RawMessage(`{"command":"go test ./..."}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || result.Metadata["status"] != "failed" {
		t.Fatalf("result = %+v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	checks, _ := payload["checks"].([]any)
	if payload["status"] != "failed" || len(checks) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	check, _ := checks[0].(map[string]any)
	if check["status"] != "failed" {
		t.Fatalf("check = %+v", check)
	}
}

func TestQualityVerifierEmitsCanonicalCoverageEvidence(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "a.md"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	quality := &Tool{
		root: root, kind: "quality_verify",
		run: func(_ context.Context, options process.Options) (process.Result, error) {
			if !options.WorkspaceReadOnly {
				t.Fatal("quality process was not workspace read-only")
			}
			return process.Result{}, nil
		},
	}
	result, err := quality.Execute(t.Context(), json.RawMessage(
		`{"command":"verify","covered_paths":["docs/a.md","docs/a.md"]}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := result.Metadata[verify.EvidenceMetadataKey].(verify.Evidence)
	if !ok {
		t.Fatalf("verification evidence = %#v", result.Metadata)
	}
	if evidence.Status != verify.StatusPassed ||
		len(evidence.CoveredPaths) != 1 ||
		evidence.CoveredPaths[0] != "docs/a.md" ||
		evidence.CommandDigest == "" {
		t.Fatalf("verification evidence = %+v", evidence)
	}
}

func TestQualityVerifierRejectsCoverageOutsideWorkspace(t *testing.T) {
	quality := &Tool{
		root: t.TempDir(), kind: "quality_verify",
		run: func(context.Context, process.Options) (process.Result, error) {
			t.Fatal("invalid coverage executed a command")
			return process.Result{}, nil
		},
	}
	for _, path := range []string{"../outside", "/tmp/outside"} {
		raw, err := json.Marshal(map[string]any{
			"command": "verify", "covered_paths": []string{path},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := quality.Execute(t.Context(), raw); err == nil {
			t.Fatalf("covered path %q was accepted", path)
		}
	}
}
