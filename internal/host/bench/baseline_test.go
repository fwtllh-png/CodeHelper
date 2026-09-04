package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestCheckedInBaselineMatchesTaskInventory(t *testing.T) {
	root := filepath.Join("..", "..", "..", "testdata", "benchmarks")
	tasks, err := DiscoverTasks(root)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "baseline-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var baseline struct {
		SchemaVersion int `json:"schema_version"`
		TaskInventory struct {
			Total             int            `json:"total"`
			Categories        map[string]int `json:"categories"`
			ExpectedTerminals map[string]int `json:"expected_terminals"`
			Assertions        map[string]int `json:"tasks_with_assertions"`
		} `json:"task_inventory"`
	}
	if err := json.Unmarshal(data, &baseline); err != nil {
		t.Fatal(err)
	}
	if baseline.SchemaVersion != 1 {
		t.Fatalf("baseline schema version=%d want 1", baseline.SchemaVersion)
	}
	categories := make(map[string]int)
	terminals := make(map[string]int)
	assertions := map[string]int{
		"files": 0, "unchanged": 0, "absent": 0, "tools_used": 0,
		"tools_failed": 0, "output_contains": 0, "receipt_changes": 0,
		"verification": 0, "context": 0, "approval": 0, "compaction": 0,
	}
	for _, task := range tasks {
		categories[task.Category]++
		terminals[task.Expect.Terminal]++
		incrementIf(assertions, "files", len(task.Expect.Files) > 0)
		incrementIf(assertions, "unchanged", len(task.Expect.Unchanged) > 0)
		incrementIf(assertions, "absent", len(task.Expect.Absent) > 0)
		incrementIf(assertions, "tools_used", len(task.Expect.ToolsUsed) > 0)
		incrementIf(assertions, "tools_failed", len(task.Expect.ToolsFailed) > 0)
		incrementIf(assertions, "output_contains", len(task.Expect.OutputContains) > 0)
		incrementIf(assertions, "receipt_changes", len(task.Expect.ReceiptChanges) > 0)
		incrementIf(
			assertions, "verification",
			task.Expect.VerifyStatus != "" || task.Expect.VerifyAction != "",
		)
		incrementIf(
			assertions, "context",
			len(task.Expect.ContextSections) > 0 ||
				len(task.Expect.ContextSelections) > 0,
		)
		incrementIf(assertions, "approval", task.Expect.Approvals != nil)
		incrementIf(assertions, "compaction", task.Expect.Compactions != nil)
	}
	if baseline.TaskInventory.Total != len(tasks) ||
		!reflect.DeepEqual(baseline.TaskInventory.Categories, categories) ||
		!reflect.DeepEqual(baseline.TaskInventory.ExpectedTerminals, terminals) ||
		!reflect.DeepEqual(baseline.TaskInventory.Assertions, assertions) {
		t.Fatalf(
			"baseline inventory=(%d,%v,%v,%v) tasks=(%d,%v,%v,%v)",
			baseline.TaskInventory.Total,
			baseline.TaskInventory.Categories,
			baseline.TaskInventory.ExpectedTerminals,
			baseline.TaskInventory.Assertions,
			len(tasks), categories, terminals, assertions,
		)
	}
}

func incrementIf(counts map[string]int, name string, condition bool) {
	if condition {
		counts[name]++
	}
}

func TestBaselineMetricsExposeRatesAndPercentiles(t *testing.T) {
	results := []Result{
		{
			Passed: true, DurationMS: 100, UsageCalls: 2,
			VerificationApplicable: true, VerificationCovered: true,
		},
		{
			Passed: true, DurationMS: 200, UsageCalls: 2, UnpricedCalls: 1,
			RetryAttempts: 2, VerificationApplicable: true,
			VerificationCovered: true,
		},
		{
			Passed: false, DurationMS: 1000, UsageCalls: 1, UnpricedCalls: 1,
			VerificationApplicable: true,
		},
	}

	got := baselineMetrics(results)
	assertRatio(t, got.TaskSuccessRate, 2, 3)
	assertRatio(t, got.RetryRate, 1, 3)
	assertRatio(t, got.VerificationCoverage, 2, 3)
	assertRatio(t, got.UnknownCostRate, 2, 5)
	assertRatio(t, got.RecoverySuccessRate, 1, 1)
	if got.RetryAttempts != 2 {
		t.Fatalf("retry attempts=%d want 2", got.RetryAttempts)
	}
	if got.Latency.Samples != 3 ||
		got.Latency.P50MS != 200 ||
		got.Latency.P95MS != 1000 {
		t.Fatalf("latency=%+v", got.Latency)
	}
}

func TestBaselineMetricsKeepEmptyDenominatorsUnknown(t *testing.T) {
	got := baselineMetrics(nil)
	for name, ratio := range map[string]Ratio{
		"success":  got.TaskSuccessRate,
		"retry":    got.RetryRate,
		"verify":   got.VerificationCoverage,
		"cost":     got.UnknownCostRate,
		"recovery": got.RecoverySuccessRate,
	} {
		if ratio.Value != nil || ratio.Denominator != 0 {
			t.Fatalf("%s ratio=%+v want unknown", name, ratio)
		}
	}
}

func TestReportJSONCarriesVersionedBaselineMetrics(t *testing.T) {
	report := Report{
		SchemaVersion: 1,
		Total:         1,
		Passed:        1,
		Results:       []Result{{Passed: true, DurationMS: 10}},
		Metrics:       baselineMetrics([]Result{{Passed: true, DurationMS: 10}}),
		GeneratedAt:   time.Date(2026, 8, 6, 0, 0, 0, 0, time.UTC),
	}
	var encoded bytes.Buffer
	if err := report.Encode(&encoded); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["schema_version"] != float64(1) {
		t.Fatalf("schema_version=%v", payload["schema_version"])
	}
	metrics, ok := payload["metrics"].(map[string]any)
	if !ok || metrics["task_success_rate"] == nil ||
		metrics["recovery_success_rate"] == nil {
		t.Fatalf("metrics=%v", payload["metrics"])
	}
}

func assertRatio(t *testing.T, got Ratio, numerator, denominator int) {
	t.Helper()
	if got.Numerator != numerator || got.Denominator != denominator ||
		got.Value == nil {
		t.Fatalf("ratio=%+v want %d/%d", got, numerator, denominator)
	}
	want := float64(numerator) / float64(denominator)
	if *got.Value != want {
		t.Fatalf("ratio value=%f want %f", *got.Value, want)
	}
}
