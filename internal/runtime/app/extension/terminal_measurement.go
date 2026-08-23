package extension

import (
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

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

func TerminalMeasurementTrace(
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
