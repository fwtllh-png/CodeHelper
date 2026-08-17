package turnkernel

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalMeasurementSnapshotSealsUsageAndLatency(t *testing.T) {
	snapshot, err := NewTerminalMeasurementSnapshot(
		time.Unix(10, 0),
		&TerminalLatencyMeasurement{
			Turn: DurationMeasurement{
				Recorded: true, Milliseconds: 1200,
			},
			Provider: DurationMeasurement{
				Recorded: true, Milliseconds: 700,
			},
			Tool: DurationMeasurement{Recorded: true},
		},
		UsageState{
			InputTokens: 10, OutputTokens: 4,
			CostMicrounits: 25, CostKnown: true, Frozen: true,
		},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Recorded() ||
		!strings.HasPrefix(snapshot.Digest, "sha256:") ||
		!strings.HasPrefix(snapshot.UsageDigest, "sha256:") {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if err := ValidateTerminalMeasurement(snapshot); err != nil {
		t.Fatal(err)
	}
}

func TestTerminalMeasurementRejectsTampering(t *testing.T) {
	snapshot, err := NewTerminalMeasurementSnapshot(
		time.Unix(10, 0),
		nil,
		UsageState{Frozen: true},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Usage.InputTokens++
	if err := ValidateTerminalMeasurement(snapshot); err == nil {
		t.Fatal("tampered usage was accepted")
	}
}

func TestTerminalMeasurementPreservesMissingLatency(t *testing.T) {
	snapshot, err := NewTerminalMeasurementSnapshot(
		time.Unix(10, 0),
		nil,
		UsageState{Frozen: true},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Recorded() || snapshot.Latency.Turn.Recorded {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Latency.Turn.Milliseconds != 0 {
		t.Fatal("missing latency was converted into a measured zero")
	}
}

func BenchmarkSO4TerminalMeasurementSeal(b *testing.B) {
	latency := &TerminalLatencyMeasurement{
		Turn: DurationMeasurement{
			Recorded: true, Milliseconds: 1200,
		},
		Provider: DurationMeasurement{
			Recorded: true, Milliseconds: 700,
		},
		Tool: DurationMeasurement{Recorded: true, Milliseconds: 200},
	}
	usage := UsageState{
		Calls: 2, InputTokens: 100, OutputTokens: 20,
		CostMicrounits: 300, CostKnown: true, Frozen: true,
	}
	frozenAt := time.Unix(10, 0)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewTerminalMeasurementSnapshot(
			frozenAt,
			latency,
			usage,
			true,
		); err != nil {
			b.Fatal(err)
		}
	}
}
