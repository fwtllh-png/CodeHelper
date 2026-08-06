package protocol

import "testing"

func TestReadinessAggregationAndExitCode(t *testing.T) {
	tests := []struct {
		name   string
		checks []ReadinessCheck
		status ReadinessStatus
		code   int
	}{
		{name: "ready", checks: []ReadinessCheck{
			{ID: "runtime", Status: ReadinessReady, Reason: "available"},
		}, status: ReadinessReady, code: 0},
		{name: "degraded", checks: []ReadinessCheck{
			{ID: "optional", Status: ReadinessDegraded, Reason: "binary missing"},
			{ID: "runtime", Status: ReadinessReady, Reason: "available"},
		}, status: ReadinessDegraded, code: 1},
		{name: "blocked wins", checks: []ReadinessCheck{
			{ID: "optional", Status: ReadinessDegraded, Reason: "binary missing"},
			{ID: "runtime", Status: ReadinessBlocked, Reason: "sandbox missing"},
		}, status: ReadinessBlocked, code: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, err := NewReadiness(test.checks...)
			if err != nil {
				t.Fatal(err)
			}
			if report.Status != test.status || report.ExitCode() != test.code {
				t.Fatalf("report=%+v code=%d", report, report.ExitCode())
			}
			for index := 1; index < len(report.Checks); index++ {
				if report.Checks[index-1].ID > report.Checks[index].ID {
					t.Fatalf("checks are not sorted: %+v", report.Checks)
				}
			}
		})
	}
}

func TestReadinessRejectsInvalidChecks(t *testing.T) {
	for _, checks := range [][]ReadinessCheck{
		{{Status: ReadinessReady, Reason: "available"}},
		{{ID: "runtime", Status: ReadinessReady}},
		{{ID: "runtime", Status: "unknown", Reason: "bad"}},
		{
			{ID: "runtime", Status: ReadinessReady, Reason: "available"},
			{ID: "runtime", Status: ReadinessReady, Reason: "duplicate"},
		},
	} {
		if _, err := NewReadiness(checks...); err == nil {
			t.Fatalf("NewReadiness(%+v) succeeded", checks)
		}
	}
}
