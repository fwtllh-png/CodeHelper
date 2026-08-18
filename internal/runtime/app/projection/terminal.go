package projection

import (
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TerminalIssues(
	source []agentengine.TerminalIssue,
) []protocol.TerminalIssue {
	result := make([]protocol.TerminalIssue, len(source))
	for index, issue := range source {
		result[index] = protocol.TerminalIssue{
			Phase: issue.Phase, Code: issue.Code, Message: issue.Message,
		}
	}
	return result
}

func RunTerminal(graph model.Graph) (protocol.EventData, error) {
	reference := protocol.RunReference{RunID: graph.Run.ID}
	switch graph.Run.State {
	case protocol.RunStateCompleted:
		return &protocol.RunCompletedData{
			Run: reference, Revision: graph.Run.Revision,
		}, nil
	case protocol.RunStateFailed:
		return &protocol.RunFailedData{
			Run: reference, Revision: graph.Run.Revision,
			Code:    protocol.CodeConflict,
			Message: nonEmpty(graph.Run.Reason, "run failed"),
			Fault: &protocol.FaultMetadata{
				Origin:         protocol.FaultOriginRuntime,
				Disposition:    protocol.FaultRetryTurn,
				SideEffects:    protocol.SideEffectUnknown,
				RecoveryAction: "retry failed WorkGraph nodes from durable state",
			},
		}, nil
	case protocol.RunStateCanceled:
		return &protocol.RunCanceledData{
			Run: reference, Revision: graph.Run.Revision,
			Reason: nonEmpty(graph.Run.Reason, "run canceled"),
		}, nil
	default:
		return nil, errors.New("work graph is not terminal")
	}
}

func nonEmpty(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}
