package app

import "github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"

type SessionService struct{ *Runtime }
type ArtifactService struct{ *Runtime }

func runtimeProblem(code protocol.ErrorCode, message string, cause error) *protocol.Problem {
	return protocol.NewProblem(code, message, false, cause)
}
func retryableProblem(code protocol.ErrorCode, message string) *protocol.Problem {
	return protocol.NewProblem(code, message, true, nil)
}
func resourceProblem(code protocol.ErrorCode, message string, retryable bool, reason, resourceID string) *protocol.Problem {
	return protocol.NewProblemWithDetails(code, message, retryable, protocol.ProblemDetails{Reason: reason, ResourceID: resourceID}, nil)
}
func revisionProblem(message, resourceID string, expected, actual uint64) *protocol.Problem {
	return protocol.NewProblemWithDetails(protocol.CodeConflict, message, true,
		protocol.ProblemDetails{Reason: protocol.ProblemReasonStaleProfileRevision,
			ResourceID: resourceID, ExpectedRevision: expected, ActualRevision: actual}, nil)
}
func sessionBusyProblem(message string, summary protocol.SessionSummary) *protocol.Problem {
	return protocol.NewProblemWithDetails(protocol.CodeConflict, message, true,
		protocol.ProblemDetails{Reason: protocol.ProblemReasonSessionBusy,
			ResourceID: summary.SessionID, SessionStatus: string(summary.Status)}, nil)
}
