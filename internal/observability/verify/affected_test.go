package verify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/platform/process"
)

type topologyFixture struct {
	RootFiles   []string            `json:"root_files"`
	Paths       []string            `json:"paths"`
	Related     map[string][]string `json:"related"`
	WantCommand string              `json:"want_command"`
	WantReason  string              `json:"want_reason"`
}

type stubMapper struct {
	related map[string][]string
	err     error
	asked   []string
}

func (m *stubMapper) RelatedTests(
	_ context.Context, paths []string,
) (map[string][]string, error) {
	m.asked = append(m.asked, paths...)
	return m.related, m.err
}

func TestAffectedTopologyFixturesExplainEveryLanguageCommand(t *testing.T) {
	fixtures, err := filepath.Glob(filepath.Join("testdata", "topology", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 4 {
		t.Fatalf("topology fixtures = %v, want Go, JS/TS, Python, and Rust", fixtures)
	}
	for _, fixturePath := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fixturePath), filepath.Ext(fixturePath))
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatal(err)
			}
			var fixture topologyFixture
			if err := json.Unmarshal(data, &fixture); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			for _, path := range fixture.RootFiles {
				full := filepath.Join(root, path)
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			commands, unmapped := AffectedCommands(root, fixture.Paths, fixture.Related)
			if len(unmapped) != 0 || len(commands) != 1 {
				t.Fatalf("commands = %+v, unmapped = %v", commands, unmapped)
			}
			if commands[0].Command != fixture.WantCommand ||
				!strings.Contains(commands[0].Reason, fixture.WantReason) {
				t.Fatalf("command = %+v, want %q because %q",
					commands[0], fixture.WantCommand, fixture.WantReason)
			}
		})
	}
}

func TestAffectedScopeRunsOnlyTheChangedGoPackages(t *testing.T) {
	var ran []string
	runner := &CommandRunner{
		Root: t.TempDir(),
		Run: func(_ context.Context, options process.Options) (process.Result, error) {
			ran = append(ran, options.Command)
			return process.Result{}, nil
		},
	}
	receipt, err := runner.Verify(context.Background(), Request{
		Scope: ScopeAffected,
		Paths: []string{
			"internal/agent/engine.go", "internal/agent/verify.go", "main.go",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Scope != ScopeAffected || receipt.Status != StatusPassed {
		t.Fatalf("Verify() = %+v", receipt)
	}
	// One command over both packages, the changed one once: a per-file command
	// would compile the same package twice.
	want := []string{"go test . ./internal/agent/..."}
	if len(ran) != 1 || ran[0] != want[0] {
		t.Fatalf("ran %v, want %v", ran, want)
	}
	if len(receipt.Checks) != 1 ||
		!strings.Contains(receipt.Checks[0].Reason, "package-scoped") {
		t.Fatalf("checks = %+v, want a derivation reason", receipt.Checks)
	}
}

func TestVerificationDAGReusesOnlyPassedNodesWithMatchingInputs(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "changed.go")
	if err := os.WriteFile(path, []byte("package fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runs := 0
	runner := &CommandRunner{
		Root: root,
		Commands: []Command{{
			Name: "focused", Command: "verify changed.go",
		}},
		Run: func(context.Context, process.Options) (process.Result, error) {
			runs++
			return process.Result{}, nil
		},
	}
	request := Request{
		Scope: ScopeAffected, Paths: []string{"changed.go"},
		WorkspaceRevision: 4, MutationRevision: 1,
	}
	first, err := runner.Verify(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runner.Verify(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 1 || len(first.Checks) != 1 || len(second.Checks) != 1 ||
		first.Checks[0].Reused || !second.Checks[0].Reused ||
		first.Checks[0].InputDigest == "" ||
		second.Checks[0].WorkspaceRevision != 4 {
		t.Fatalf("runs=%d first=%+v second=%+v", runs, first, second)
	}
	if err := os.WriteFile(path, []byte("package fixture\n// changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	request.MutationRevision = 2
	request.WorkspaceRevision = 5
	third, err := runner.Verify(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if runs != 2 || third.Checks[0].Reused ||
		third.Checks[0].InputDigest == first.Checks[0].InputDigest {
		t.Fatalf("runs=%d third=%+v", runs, third)
	}
}

func TestAffectedScopeRunsTheMappedPythonTests(t *testing.T) {
	mapper := &stubMapper{related: map[string][]string{
		"app/service.py": {"app/tests/test_service.py"},
		"app/models.py":  {"tests/test_models.py"},
	}}
	var ran []string
	runner := &CommandRunner{
		Root:  t.TempDir(),
		Tests: mapper,
		Run: func(_ context.Context, options process.Options) (process.Result, error) {
			ran = append(ran, options.Command)
			return process.Result{ExitCode: 1, Stdout: "1 failed"}, nil
		},
	}
	receipt, err := runner.Verify(context.Background(), Request{
		Scope: ScopeAffected, Paths: []string{"app/service.py", "app/models.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Failed() {
		t.Fatalf("Verify() = %+v, want the failing suite to fail the pass", receipt)
	}
	want := "python3 -m pytest app/tests/test_service.py tests/test_models.py"
	if len(ran) != 1 || ran[0] != want {
		t.Fatalf("ran %v, want %q", ran, want)
	}
	if len(receipt.Checks) != 1 || receipt.Checks[0].Reason == "" {
		t.Fatalf("checks = %+v, want a derivation reason", receipt.Checks)
	}
	if len(mapper.asked) != 2 {
		t.Fatalf("asked the mapper for %v", mapper.asked)
	}
}

// A Python file whose tests the index cannot name must not be reported as
// verified, and a language with no rule here must say so instead of passing.
func TestAffectedScopeReportsWhatItCannotMap(t *testing.T) {
	tests := map[string]struct {
		paths   []string
		related map[string][]string
		ran     bool
		want    string
	}{
		"python without tests": {
			paths:   []string{"app/service.py"},
			related: map[string][]string{"app/service.py": {}},
			want:    "app/service.py",
		},
		"unknown language": {
			paths: []string{"web/app.rb", "README.md"},
			want:  "README.md, web/app.rb",
		},
		"partially mapped": {
			paths:   []string{"internal/agent/engine.go", "web/app.rb"},
			ran:     true,
			want:    "web/app.rb",
			related: map[string][]string{},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var ran []string
			runner := &CommandRunner{
				Root:  t.TempDir(),
				Tests: &stubMapper{related: test.related},
				Run: func(_ context.Context, options process.Options) (process.Result, error) {
					ran = append(ran, options.Command)
					return process.Result{}, nil
				},
			}
			receipt, err := runner.Verify(context.Background(), Request{
				Scope: ScopeAffected, Paths: test.paths,
			})
			if err != nil {
				t.Fatal(err)
			}
			if test.ran {
				if len(ran) == 0 || receipt.Status != StatusPassed {
					t.Fatalf("Verify() = %+v, ran %v, want the mapped part run", receipt, ran)
				}
			} else {
				if len(ran) != 0 {
					t.Fatalf("ran %v, want nothing", ran)
				}
				if receipt.Status != StatusUnavailable || receipt.Failed() {
					t.Fatalf("Verify() = %+v, want an unavailable pass", receipt)
				}
			}
			if !strings.Contains(receipt.Message, test.want) {
				t.Fatalf("message = %q, want it to name %q", receipt.Message, test.want)
			}
		})
	}
}

func TestAffectedScopeReportsABrokenMappingAsUnavailable(t *testing.T) {
	runner := &CommandRunner{
		Root:  t.TempDir(),
		Tests: &stubMapper{err: errors.New("the repository index is degraded")},
		Run: func(context.Context, process.Options) (process.Result, error) {
			t.Fatal("ran a command without a mapping")
			return process.Result{}, nil
		},
	}
	receipt, err := runner.Verify(context.Background(), Request{
		Scope: ScopeAffected, Paths: []string{"app/service.py"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != StatusUnavailable ||
		!strings.Contains(receipt.Message, "degraded") {
		t.Fatalf("Verify() = %+v", receipt)
	}
}

func TestAffectedScopeSkipsAPassWithNoChangedPaths(t *testing.T) {
	runner := &CommandRunner{Root: t.TempDir()}
	receipt, err := runner.Verify(context.Background(), Request{Scope: ScopeAffected})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != StatusUnavailable {
		t.Fatalf("Verify() = %+v", receipt)
	}
}

// A configured command replaces the derived ones, so an operator whose suite the
// rules here cannot express still gets a scoped run.
func TestAffectedScopeExpandsTheConfiguredCommand(t *testing.T) {
	root := t.TempDir()
	var ran []string
	runner := &CommandRunner{
		Root: root,
		Commands: []Command{
			{Name: "custom", Command: "go test {packages} && ./check.sh {paths}"},
		},
		Tests: &stubMapper{err: errors.New("never asked")},
		Run: func(_ context.Context, options process.Options) (process.Result, error) {
			ran = append(ran, options.Command)
			return process.Result{}, nil
		},
	}
	receipt, err := runner.Verify(context.Background(), Request{
		Scope: ScopeAffected,
		Paths: []string{
			filepath.Join(root, "internal", "agent", "engine.go"), "docs/a b.md",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Scope != ScopeAffected || receipt.Status != StatusPassed {
		t.Fatalf("Verify() = %+v", receipt)
	}
	want := "go test ./internal/agent/... && ./check.sh 'docs/a b.md' internal/agent/engine.go"
	if len(ran) != 1 || ran[0] != want {
		t.Fatalf("ran %v, want %q", ran, want)
	}
}

func TestRelativePathsAcceptsBothSpellingsOfTheRoot(t *testing.T) {
	root := t.TempDir()
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := relativePaths(root, []string{
		filepath.Join(resolved, "a.go"),
		filepath.Join(root, "b.go"),
		"c.go",
		filepath.Join(t.TempDir(), "outside.go"),
		"  ",
	})
	want := []string{"a.go", "b.go", "c.go"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Fatalf("relativePaths() = %v, want %v", paths, want)
	}
}
