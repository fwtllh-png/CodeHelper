package quality

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
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
			result, err := registry.Execute(t.Context(), tool.Call{
				Name: name, Arguments: json.RawMessage(`{"command":"printf fixture"}`), Authorized: true,
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
			result, err := registry.Execute(t.Context(), tool.Call{
				Name: test.name, Arguments: raw, Authorized: true,
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

type qualityTestBackend struct{}

func (qualityTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (qualityTestBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
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

func TestQualityVerifierReportsModuleProxyTimeoutAsUnavailable(t *testing.T) {
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
	if result.IsError ||
		result.Metadata["status"] != "unavailable" ||
		result.Metadata["error_category"] != verify.ErrorCategoryDependencyUnavailable {
		t.Fatalf("result = %+v", result)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	checks, _ := payload["checks"].([]any)
	if payload["status"] != "unavailable" || len(checks) != 1 {
		t.Fatalf("payload = %+v", payload)
	}
	check, _ := checks[0].(map[string]any)
	if check["status"] != "unavailable" ||
		check["error_category"] != verify.ErrorCategoryDependencyUnavailable {
		t.Fatalf("check = %+v", check)
	}
}
