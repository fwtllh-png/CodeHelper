//go:build capability

package bench_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/bench"
)

func suiteRoot() string {
	return filepath.Join("..", "..", "..", "testdata", "benchmarks")
}

// TestCodingBenchmarkSuite is the release gate: every checked-in task must pass
// with fixture models and no network or API key. Verify cases still require the
// platform's real strong sandbox, so this file is outside the default lane.
func TestCodingBenchmarkSuite(t *testing.T) {
	report, err := bench.RunSuite(t.Context(), suiteRoot())
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := report.Encode(&encoded); err != nil {
		t.Fatal(err)
	}
	t.Logf("benchmark report:\n%s", encoded.String())
	if path := os.Getenv("CODEHELPER_BENCH_REPORT"); path != "" {
		if err := os.WriteFile(path, encoded.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, result := range report.Results {
		if !result.Passed {
			t.Errorf("task %s failed: %v", result.Task, result.Failures)
		}
	}
	if !report.OK() {
		t.Fatalf("suite passed %d/%d", report.Passed, report.Total)
	}
	if report.InputTokens == 0 || report.OutputTokens == 0 {
		t.Fatalf("suite recorded no token usage: %+v", report)
	}
	// Fixtures report prompt_tokens_details.cached_tokens, so a zero here means
	// the provider→protocol usage path regressed rather than the model changing.
	if report.CachedTokens == 0 {
		t.Fatalf("suite recorded no cached tokens: %+v", report)
	}
}

// TestBenchmarkAssertionsFail guards the harness itself: a task whose
// expectations do not match reality must be reported as failed, otherwise the
// gate would pass vacuously.
func TestBenchmarkAssertionsFail(t *testing.T) {
	source := filepath.Join(suiteRoot(), "single-file-fix")
	dir := filepath.Join(t.TempDir(), "single-file-fix")
	if err := copyTree(source, dir); err != nil {
		t.Fatal(err)
	}
	definition := filepath.Join(dir, bench.TaskFile)
	raw, err := os.ReadFile(definition)
	if err != nil {
		t.Fatal(err)
	}
	var task map[string]any
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	expect, _ := task["expect"].(map[string]any)
	expect["files"] = map[string]any{"calc.py": "def add(a, b):\n    return a * b\n"}
	expect["tools_used"] = []any{"file_read", "file_edit", "shell_run"}
	mutated, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definition, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := bench.LoadTask(dir)
	if err != nil {
		t.Fatal(err)
	}
	result := bench.RunTask(t.Context(), loaded)
	if result.Passed {
		t.Fatal("mutated expectations passed; harness assertions are not enforced")
	}
	if result.Error != "" {
		t.Fatalf("expected assertion failures, got harness error: %s", result.Error)
	}
	if len(result.Failures) != 2 {
		t.Fatalf("failures = %v, want one content and one tool failure", result.Failures)
	}
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
