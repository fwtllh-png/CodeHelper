package app

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/eventhub"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type SessionService struct{ *Runtime }
type ArtifactService struct{ *Runtime }

func newEventHub(ctx context.Context, runtime *Runtime) *eventhub.Hub {
	return eventhub.New(eventhub.Config{
		Store: runtime.events, Buffer: runtime.opts.SubscriberBuffer,
		Context: ctx, Closed: ErrClosed, CursorAhead: ErrCursorAhead,
		ReplayOverflow: func(cursor protocol.Cursor, limit int) error {
			return &ReplayLimitError{Requested: cursor, Limit: limit}
		},
		OnPublished: runtime.metrics.EventPublished, OnDropped: runtime.metrics.SubscriberDropped,
		OnEvent: runtime.observeEvent,
	})
}

func runtimeProblem(code protocol.ErrorCode, message string, cause error) *protocol.Problem {
	return protocol.NewProblem(code, message, false, cause)
}
func retryableProblem(code protocol.ErrorCode, message string) *protocol.Problem {
	return protocol.NewProblem(code, message, true, nil)
}
func turnNotActiveProblem() *protocol.Problem {
	return runtimeProblem(protocol.CodeInvalidArgument, "turn is not active", nil)
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
