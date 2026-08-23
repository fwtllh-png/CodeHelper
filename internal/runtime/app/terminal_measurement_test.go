package app

import (
	"encoding/json"
	appextension "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	executionreceipt "github.com/fwtllh-png/CodeHelper/internal/observability/receipt"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/eventhub"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestSO4ReceiptAndTraceUseOneFrozenMeasurement(t *testing.T) {
	firstOutput := 250 * time.Millisecond
	measurement, err := executionreceipt.FreezeTerminalMeasurement(
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
	receipt := executionreceipt.New("answer").BuildWithMeasurement(&measurement)
	records := appextension.TerminalMeasurementTrace(
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
	receipt := executionreceipt.New("startup failure").
		BuildWithMeasurement(&measurement)
	if receipt.MeasurementRecorded ||
		receipt.Latency != nil ||
		len(appextension.TerminalMeasurementTrace(
			measurement,
			turnkernel.TerminalDecision{
				Kind: turnkernel.TerminalFailed,
			},
		)) != 0 {
		t.Fatalf("receipt=%+v measurement=%+v", receipt, measurement)
	}
}

func TestTerminalObservationOutcomeOmitsRawFailureMessage(t *testing.T) {
	const secret = "api-key-do-not-persist"
	outcome := eventhub.TerminalObservationOutcome(turnkernel.TerminalDecision{
		Kind:    turnkernel.TerminalFailed,
		Code:    string(protocol.CodeUnavailable),
		Message: secret,
		Fault: &protocol.FaultMetadata{
			Origin:      protocol.FaultOriginProvider,
			Stage:       protocol.FaultStageConnection,
			Disposition: protocol.FaultRetryTurn,
			ResumeHint:  protocol.FaultResumeRetryTurn,
		},
	})
	encoded, err := observation.EncodeTerminalSummary("digest", outcome)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || !json.Valid(encoded) {
		t.Fatalf("unsafe terminal summary = %s", encoded)
	}
	decoded, err := observation.DecodeTerminalSummary(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Outcome == nil ||
		decoded.Outcome.Status != observation.TerminalFailed ||
		decoded.Outcome.Code != string(protocol.CodeUnavailable) ||
		decoded.Outcome.Fault == nil ||
		decoded.Outcome.Fault.Stage != protocol.FaultStageConnection {
		t.Fatalf("decoded terminal summary = %+v", decoded)
	}
}

func BenchmarkSO4MeasurementTraceProjection(b *testing.B) {
	measurement, err := executionreceipt.FreezeTerminalMeasurement(
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
		if records := appextension.TerminalMeasurementTrace(
			measurement,
			terminal,
		); len(records) == 0 {
			b.Fatal("measurement trace is empty")
		}
	}
}
