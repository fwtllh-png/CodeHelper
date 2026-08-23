package eventhub

import (
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TerminalObservationOutcome(
	terminal turnkernel.TerminalDecision,
) trace.TerminalOutcome {
	outcome := trace.TerminalOutcome{
		Status: trace.TerminalFailed,
		Code:   terminal.Code,
		Fault:  protocol.CloneFaultMetadata(terminal.Fault),
	}
	switch terminal.Kind {
	case turnkernel.TerminalCompleted:
		outcome.Status, outcome.Code, outcome.Fault =
			trace.TerminalCompleted, "", nil
	case turnkernel.TerminalCanceled:
		outcome.Status = trace.TerminalCanceled
		if outcome.Code == "" {
			outcome.Code = string(protocol.CodeCanceled)
		}
	case turnkernel.TerminalFailed:
		if outcome.Code == "" {
			outcome.Code = string(protocol.CodeInternal)
		}
	}
	return outcome
}
