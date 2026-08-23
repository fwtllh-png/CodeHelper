package artifact

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func runtimeProblem(
	code protocol.ErrorCode,
	message string,
	cause error,
) *protocol.Problem {
	return protocol.NewProblem(code, message, false, cause)
}

func retryableProblem(
	code protocol.ErrorCode,
	message string,
) *protocol.Problem {
	return protocol.NewProblem(code, message, true, nil)
}

func resourceProblem(
	code protocol.ErrorCode,
	message string,
	retryable bool,
	reason string,
	resourceID string,
) *protocol.Problem {
	return protocol.NewProblemWithDetails(
		code,
		message,
		retryable,
		protocol.ProblemDetails{Reason: reason, ResourceID: resourceID},
		nil,
	)
}

func revisionProblem(
	message string,
	resourceID string,
	expected uint64,
	actual uint64,
) *protocol.Problem {
	return protocol.NewProblemWithDetails(
		protocol.CodeConflict,
		message,
		true,
		protocol.ProblemDetails{
			Reason:           protocol.ProblemReasonStaleProfileRevision,
			ResourceID:       resourceID,
			ExpectedRevision: expected,
			ActualRevision:   actual,
		},
		nil,
	)
}

func ensureSessionQuiescent(
	summary protocol.SessionSummary,
	action string,
) error {
	switch summary.Status {
	case protocol.SessionStatusRunning,
		protocol.SessionStatusAwaitingApproval,
		protocol.SessionStatusAwaitingInput:
		return protocol.NewProblemWithDetails(
			protocol.CodeConflict,
			fmt.Sprintf(
				"cannot %s session while status is %s",
				action,
				summary.Status,
			),
			true,
			protocol.ProblemDetails{
				Reason:        protocol.ProblemReasonSessionBusy,
				ResourceID:    summary.SessionID,
				SessionStatus: string(summary.Status),
			},
			nil,
		)
	default:
		return nil
	}
}
