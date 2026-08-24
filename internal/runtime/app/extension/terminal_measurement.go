package extension

import (
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
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
