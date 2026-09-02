package verify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/observability/diagnostics"
	"github.com/fwtllh-png/QCode/internal/platform/process"
)

func TestDetectReadsWorkspaceBuildFiles(t *testing.T) {
	tests := map[string]struct {
		files   []string
		want    []string
		exclude string
	}{
		"go":     {files: []string{"go.mod"}, want: []string{"go test ./..."}},
		"pnpm":   {files: []string{"package.json", "pnpm-lock.yaml"}, want: []string{"pnpm test"}},
		"yarn":   {files: []string{"package.json", "yarn.lock"}, want: []string{"yarn test"}},
		"npm":    {files: []string{"package.json"}, want: []string{"npm test"}},
		"python": {files: []string{"pyproject.toml"}, want: []string{"python3 -m pytest"}},
		"rust":   {files: []string{"Cargo.toml"}, want: []string{"cargo test --workspace"}},
		"cmake": {
			files: []string{"CMakeLists.txt"},
			want:  []string{"cmake -S .", "cmake --build", "ctest --test-dir"},
		},
		"bazel workspace": {files: []string{"WORKSPACE.bazel"}, want: []string{"bazel test //..."}},
		"bazel module":    {files: []string{"MODULE.bazel"}, want: []string{"bazel test //..."}},
		"maven":           {files: []string{"pom.xml"}, want: []string{"mvn test"}},
		"gradle wrapper":  {files: []string{"gradlew"}, want: []string{"./gradlew test"}},
		"gradle system":   {files: []string{"build.gradle.kts"}, want: []string{"gradle test"}},
		"fallback":        {want: []string{"make verify"}},
		"mixed": {
			files: []string{"go.mod", "Cargo.toml"},
			want:  []string{"go test ./...", "cargo test --workspace"},
			// The fallback must not be appended once anything was detected.
			exclude: "make verify",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, file := range test.files {
				if err := os.WriteFile(filepath.Join(root, file), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var commands []string
			for _, command := range Detect(root) {
				commands = append(commands, command.Command)
			}
			joined := strings.Join(commands, "|")
			for _, want := range test.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("Detect() = %v, want it to include %q", commands, want)
				}
			}
			if test.exclude != "" && strings.Contains(joined, test.exclude) {
				t.Fatalf("Detect() = %v, want it to exclude %q", commands, test.exclude)
			}
		})
	}
}

func TestFromDiagnosticsGradesBySeverity(t *testing.T) {
	errorReceipt := diagnostics.Receipt{
		Path: "a.go", Status: "completed", Runner: "gopls",
		Diagnostics: []diagnostics.Diagnostic{{
			Path: "a.go", Severity: "error", Message: "undefined: foo",
			Range: diagnostics.Range{Start: diagnostics.Position{Line: 4, Character: 2}},
		}},
	}
	warningReceipt := diagnostics.Receipt{
		Path: "b.go", Status: "completed", Runner: "gopls",
		Diagnostics: []diagnostics.Diagnostic{{
			Path: "b.go", Severity: "warning", Message: "shadowed variable",
		}},
	}

	tests := map[string]struct {
		receipts     []diagnostics.Receipt
		paths        []string
		wantStatus   string
		wantErrors   int
		wantWarnings int
	}{
		"error fails": {
			receipts: []diagnostics.Receipt{errorReceipt},
			paths:    []string{"a.go"}, wantStatus: StatusFailed, wantErrors: 1,
		},
		"warning passes": {
			receipts: []diagnostics.Receipt{warningReceipt},
			paths:    []string{"b.go"}, wantStatus: StatusPassed, wantWarnings: 1,
		},
		"runner failure fails": {
			receipts: []diagnostics.Receipt{{
				Path: "a.go", Status: "failed", Runner: "gopls", Message: "gopls exited with code 2",
			}},
			paths: []string{"a.go"}, wantStatus: StatusFailed, wantErrors: 1,
		},
		"unavailable is not a green light": {
			receipts: []diagnostics.Receipt{{Path: "a.go", Status: "unavailable"}},
			paths:    []string{"a.go"}, wantStatus: StatusUnavailable,
		},
		"no receipts at all": {paths: []string{"a.go"}, wantStatus: StatusUnavailable},
		"unrelated path is ignored": {
			receipts: []diagnostics.Receipt{errorReceipt},
			paths:    []string{"other.go"}, wantStatus: StatusUnavailable,
		},
		"absolute receipt path matches relative change": {
			receipts: []diagnostics.Receipt{{
				Path: "/workspace/pkg/a.go", Status: "completed", Runner: "gopls",
				Diagnostics: []diagnostics.Diagnostic{{Severity: "error", Message: "boom"}},
			}},
			paths: []string{"pkg/a.go"}, wantStatus: StatusFailed, wantErrors: 1,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			receipt := FromDiagnostics(test.receipts, test.paths)
			if receipt.Scope != ScopeDiagnostics || receipt.Status != test.wantStatus ||
				receipt.Errors != test.wantErrors || receipt.Warnings != test.wantWarnings {
				t.Fatalf("FromDiagnostics() = %+v, want status %q", receipt, test.wantStatus)
			}
			if receipt.Failed() != (test.wantStatus == StatusFailed) {
				t.Fatalf("Failed() = %v for status %q", receipt.Failed(), receipt.Status)
			}
		})
	}
}

func TestFromDiagnosticsFeedbackLocatesTheError(t *testing.T) {
	receipt := FromDiagnostics([]diagnostics.Receipt{{
		Path: "a.go", Status: "completed", Runner: "gopls",
		Diagnostics: []diagnostics.Diagnostic{{
			Path: "a.go", Severity: "error", Message: "undefined: foo",
			Range: diagnostics.Range{Start: diagnostics.Position{Line: 4, Character: 2}},
		}},
	}}, nil)

	feedback := receipt.Feedback(0)
	if !strings.Contains(feedback, "a.go:5:3: undefined: foo") {
		t.Fatalf("Feedback() = %q", feedback)
	}
	if truncated := receipt.Feedback(20); !strings.HasSuffix(truncated, "truncated]") {
		t.Fatalf("Feedback(20) = %q, want truncation", truncated)
	}
}

func TestCommandRunnerRunsDetectedCommands(t *testing.T) {
	root := t.TempDir()
	var commands []string
	runner := &CommandRunner{
		Root: root,
		Commands: []Command{
			{Name: "unit", Command: "go test ./..."},
			{Name: "lint", Command: "golangci-lint run"},
		},
		Run: func(_ context.Context, options process.Options) (process.Result, error) {
			commands = append(commands, options.Command)
			if !options.RequireSandbox {
				t.Fatal("verification ran without requiring a strong sandbox")
			}
			if options.Command == "golangci-lint run" {
				return process.Result{ExitCode: 1, Stdout: "a.go:1:1: unused"}, nil
			}
			return process.Result{}, nil
		},
	}

	receipt, err := runner.Verify(context.Background(), Request{Scope: ScopeRepository})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != StatusFailed || receipt.Errors != 1 || len(receipt.Checks) != 2 {
		t.Fatalf("Verify() = %+v", receipt)
	}
	if receipt.Checks[0].Status != StatusPassed || receipt.Checks[1].Status != StatusFailed {
		t.Fatalf("checks = %+v", receipt.Checks)
	}
	if len(commands) != 2 {
		t.Fatalf("ran commands %v, want both", commands)
	}
	feedback := receipt.Feedback(0)
	if !strings.Contains(feedback, "golangci-lint run") || strings.Contains(feedback, "go test") {
		t.Fatalf("Feedback() = %q, want only the failed check", feedback)
	}
}

func TestCommandRunnerDoesNotInferFailureKindFromCommandOutput(t *testing.T) {
	runner := &CommandRunner{
		Root:     t.TempDir(),
		Commands: []Command{{Name: "go", Command: "go test ./..."}},
		Run: func(context.Context, process.Options) (process.Result, error) {
			return process.Result{
				ExitCode: 1,
				Stderr: `go: example.org/dependency@v1.0.0: Get ` +
					`"https://proxy.internal.example/example.org/dependency/@v/v1.0.0.info": ` +
					`context deadline exceeded`,
			}, nil
		},
	}

	receipt, err := runner.Verify(context.Background(), Request{Scope: ScopeRepository})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != StatusFailed || !receipt.Failed() || receipt.Errors != 1 {
		t.Fatalf("Verify() = %+v", receipt)
	}
	if len(receipt.Checks) != 1 || receipt.Checks[0].Status != StatusFailed {
		t.Fatalf("receipt = %+v", receipt)
	}
}

func TestCommandRunnerDoesNotHideOrdinaryTestTimeout(t *testing.T) {
	status, _ := CommandResultStatus("go test ./...", process.Result{
		ExitCode: 1, Stderr: "--- FAIL: TestWorker\ncontext deadline exceeded",
	})
	if status != StatusFailed {
		t.Fatalf("status = %q, want %q", status, StatusFailed)
	}
}

// The sandbox pins the workspace by its symlink-resolved path, so a runner that
// kept the caller's spelling of the root would have every command rejected as
// outside the workspace.
func TestCommandRunnerRunsFromTheResolvedRoot(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	var observed process.Options
	runner := &CommandRunner{
		Root:     link,
		Commands: []Command{{Name: "unit", Command: "true"}},
		Run: func(_ context.Context, options process.Options) (process.Result, error) {
			observed = options
			return process.Result{}, nil
		},
	}
	if _, err := runner.Verify(context.Background(), Request{Scope: ScopeRepository}); err != nil {
		t.Fatal(err)
	}
	if observed.Dir != resolved {
		t.Fatalf("ran in %q, want the resolved root %q", observed.Dir, resolved)
	}
	if !observed.RequireSandbox || !observed.WorkspaceReadOnly {
		t.Fatalf("verification process authority = %+v", observed)
	}
}

func TestVerifyRejectsUnknownScope(t *testing.T) {
	runner := &CommandRunner{Root: t.TempDir()}
	if _, err := runner.Verify(
		context.Background(), Request{Scope: Scope("packages")},
	); err == nil {
		t.Fatal("Verify() accepted an unimplemented scope")
	}
}

func TestDiagnosticsScopeReadsTheRequestReceipts(t *testing.T) {
	runner := &CommandRunner{
		Root: t.TempDir(),
		Run: func(context.Context, process.Options) (process.Result, error) {
			t.Fatal("diagnostics scope started a process")
			return process.Result{}, nil
		},
	}
	receipt, err := runner.Verify(context.Background(), Request{
		Scope: ScopeDiagnostics, Paths: []string{"a.go"},
		Diagnostics: []diagnostics.Receipt{{
			Path: "a.go", Status: "completed", Runner: "gopls",
			Diagnostics: []diagnostics.Diagnostic{{Severity: "error", Message: "boom"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Failed() || receipt.Scope != ScopeDiagnostics {
		t.Fatalf("Verify() = %+v", receipt)
	}
}

func TestUnavailableRunnerNeverFails(t *testing.T) {
	receipt, err := UnavailableRunner{}.Verify(
		context.Background(), Request{Scope: ScopeRepository},
	)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != StatusUnavailable || receipt.Failed() {
		t.Fatalf("Verify() = %+v", receipt)
	}
}
