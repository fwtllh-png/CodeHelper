package app

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func freezeTerminalMeasurement(
	frozen trace.FrozenMeasurement,
	usage turnkernel.UsageState,
) (turnkernel.TerminalMeasurementSnapshot, error) {
	frozenAt := frozen.FrozenAt
	var latency *turnkernel.TerminalLatencyMeasurement
	if frozen.Recorded {
		latency = &turnkernel.TerminalLatencyMeasurement{
			Turn: durationMeasurement(frozen.Latency.Total, true),
			Provider: durationMeasurement(
				frozen.Latency.Provider,
				true,
			),
			Tool: durationMeasurement(frozen.Latency.Tool, true),
			ApprovalWait: durationMeasurement(
				frozen.Latency.ApprovalWait,
				true,
			),
			Verification: durationMeasurement(
				frozen.Latency.Verify,
				true,
			),
		}
		if frozen.Latency.FirstToken != nil {
			latency.FirstOutput = durationMeasurement(
				*frozen.Latency.FirstToken,
				true,
			)
		}
	} else {
		frozenAt = time.Now().UTC()
	}
	return turnkernel.NewTerminalMeasurementSnapshot(
		frozenAt,
		latency,
		usage,
		true,
	)
}

func durationMeasurement(
	value time.Duration,
	recorded bool,
) turnkernel.DurationMeasurement {
	return turnkernel.DurationMeasurement{
		Recorded:     recorded,
		Milliseconds: value.Milliseconds(),
	}
}

func terminalTraceStatus(terminal protocol.EventData) trace.Status {
	switch terminal.(type) {
	case *protocol.TurnCompletedData:
		return trace.StatusOK
	case *protocol.TurnCanceledData:
		return trace.StatusCanceled
	default:
		return trace.StatusError
	}
}

func terminalMeasurementTrace(
	snapshot turnkernel.TerminalMeasurementSnapshot,
	terminal turnkernel.TerminalDecision,
) []trace.Record {
	if !snapshot.Latency.Turn.Recorded {
		return nil
	}
	status := trace.StatusError
	switch terminal.Kind {
	case turnkernel.TerminalCompleted:
		status = trace.StatusOK
	case turnkernel.TerminalCanceled:
		status = trace.StatusCanceled
	}
	root := trace.Record{
		ID: 1, Name: trace.NameTurn,
		Started: snapshot.FrozenAt.Add(
			-time.Duration(snapshot.Latency.Turn.Milliseconds) *
				time.Millisecond,
		),
		Ended: snapshot.FrozenAt, Status: status,
		Attributes: map[string]any{
			"measurement_version":    snapshot.Version,
			"measurement_digest":     snapshot.Digest,
			"usage_digest":           snapshot.UsageDigest,
			"measurement_projection": true,
		},
	}
	if snapshot.Latency.FirstOutput.Recorded {
		root.Attributes["first_output_ms"] =
			snapshot.Latency.FirstOutput.Milliseconds
	}
	records := []trace.Record{root}
	appendPhase := func(
		name string,
		value turnkernel.DurationMeasurement,
	) {
		if !value.Recorded {
			return
		}
		records = append(records, trace.Record{
			ID: uint64(len(records) + 1), ParentID: root.ID,
			Name: name,
			Started: snapshot.FrozenAt.Add(
				-time.Duration(value.Milliseconds) * time.Millisecond,
			),
			Ended: snapshot.FrozenAt, Status: status,
			Attributes: map[string]any{
				"aggregate":          true,
				"measurement_digest": snapshot.Digest,
			},
		})
	}
	appendPhase(trace.NameModelCall, snapshot.Latency.Provider)
	appendPhase(trace.NameTool, snapshot.Latency.Tool)
	appendPhase(trace.NameApprovalWait, snapshot.Latency.ApprovalWait)
	appendPhase(trace.NameVerify, snapshot.Latency.Verification)
	return records
}
