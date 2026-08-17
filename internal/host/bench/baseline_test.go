package bench

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

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
