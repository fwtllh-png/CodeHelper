package verify

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

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
