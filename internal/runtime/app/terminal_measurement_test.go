package app

import (
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

func TestSO4ReceiptAndTraceUseOneFrozenMeasurement(t *testing.T) {
	firstOutput := 250 * time.Millisecond
	measurement, err := freezeTerminalMeasurement(
		trace.FrozenMeasurement{
			FrozenAt: time.Unix(20, 0),
			Recorded: true,
			Latency: trace.Latency{
				Total: 6 * time.Second, FirstToken: &firstOutput,
				Provider: 1200 * time.Millisecond,
				Tool:     3 * time.Second,
			},
		},
		turnkernel.UsageState{
			Calls: 2, InputTokens: 48, OutputTokens: 6,
			CostMicrounits: 500, CostKnown: true, Frozen: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt := newReceiptRecorder("answer").build(turnObservations{
		measurement: &measurement,
	})
	records := terminalMeasurementTrace(
		measurement,
		turnkernel.TerminalDecision{
			Kind: turnkernel.TerminalCompleted,
		},
	)
	if receipt.MeasurementDigest != measurement.Digest ||
		receipt.UsageDigest != measurement.UsageDigest ||
		receipt.LatencyMS != 6000 ||
		receipt.InputTokens != 48 {
		t.Fatalf("receipt = %+v", receipt)
	}
	if len(records) != 5 ||
		records[0].Duration() != 6*time.Second ||
		records[0].Attributes["measurement_digest"] !=
			receipt.MeasurementDigest ||
		records[0].Attributes["usage_digest"] != receipt.UsageDigest {
		t.Fatalf("trace = %+v", records)
	}
}

func TestSO4MissingLatencyDoesNotProjectMeasuredZero(t *testing.T) {
	measurement, err := turnkernel.NewTerminalMeasurementSnapshot(
		time.Unix(20, 0),
		nil,
		turnkernel.UsageState{Frozen: true},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	receipt := newReceiptRecorder("startup failure").build(
		turnObservations{measurement: &measurement},
	)
	if receipt.MeasurementRecorded ||
		receipt.Latency != nil ||
		len(terminalMeasurementTrace(
			measurement,
			turnkernel.TerminalDecision{
				Kind: turnkernel.TerminalFailed,
			},
		)) != 0 {
		t.Fatalf("receipt=%+v measurement=%+v", receipt, measurement)
	}
}

func BenchmarkSO4MeasurementTraceProjection(b *testing.B) {
	measurement, err := freezeTerminalMeasurement(
		trace.FrozenMeasurement{
			FrozenAt: time.Unix(20, 0),
			Recorded: true,
			Latency: trace.Latency{
				Total:    6 * time.Second,
				Provider: 1200 * time.Millisecond,
				Tool:     3 * time.Second,
			},
		},
		turnkernel.UsageState{
			Calls: 2, InputTokens: 48, OutputTokens: 6,
			CostKnown: true, Frozen: true,
		},
	)
	if err != nil {
		b.Fatal(err)
	}
	terminal := turnkernel.TerminalDecision{
		Kind: turnkernel.TerminalCompleted,
	}
	b.ReportAllocs()
	for b.Loop() {
		if records := terminalMeasurementTrace(
			measurement,
			terminal,
		); len(records) == 0 {
			b.Fatal("measurement trace is empty")
		}
	}
}
