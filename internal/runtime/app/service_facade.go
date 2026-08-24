package app

import (
	"context"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/persist/artifact"
	sessionhistory "github.com/fwtllh-png/CodeHelper/internal/persist/history"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/eventhub"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type SessionService struct {
	*Runtime
	mutationMu sync.Mutex
}
type ArtifactService = artifact.Service

// EventService owns in-memory event projections and observer delivery. The
// durable EventStore and Hub remain injected runtime resources.
type EventService struct {
	*Runtime

	mu            sync.Mutex
	terminals     map[protocol.TurnID]protocol.EventKind
	approvals     map[string]PendingApproval
	inputs        map[string]PendingInput
	observerMu    sync.Mutex
	observers     map[uint64]func(protocol.Event)
	nextObserver  uint64
	toolItems     map[EventItemOwner]protocol.ItemID
	approvalItems map[EventItemOwner]protocol.ItemID
	inputItems    map[EventItemOwner]protocol.ItemID
}

// RecoveryService owns reconstruction of volatile indexes and replay of
// accepted operations after durable resources are ready.
type RecoveryService struct{ *Runtime }

func installRuntimeServices(runtime *Runtime, operationBuffer int) {
	runtime.EventService = &EventService{
		Runtime:       runtime,
		terminals:     make(map[protocol.TurnID]protocol.EventKind),
		approvals:     make(map[string]PendingApproval),
		inputs:        make(map[string]PendingInput),
		observers:     make(map[uint64]func(protocol.Event)),
		toolItems:     make(map[EventItemOwner]protocol.ItemID),
		approvalItems: make(map[EventItemOwner]protocol.ItemID),
		inputItems:    make(map[EventItemOwner]protocol.ItemID),
	}
	runtime.TurnService = &TurnService{
		runtime: runtime,
		active:  NewActiveTurnRegistry(),
	}
	runtime.SessionService = &SessionService{Runtime: runtime}
	runtime.OperationService = &OperationService{
		Runtime:      runtime,
		operations:   make(chan acceptedOperation, operationBuffer),
		accepted:     make(map[protocol.OperationID]PendingOperation),
		acceptedKeys: make(map[string]protocol.OperationID),
		committed:    make(map[protocol.OperationID]PendingOperation),
	}
	runtime.RecoveryService = &RecoveryService{Runtime: runtime}
	runtime.HistoryService = sessionhistory.NewService(runtime)
	runtime.ArtifactService = artifact.NewArtifactService(runtime)
	runtime.TraceService = &TraceService{
		runtime: runtime,
		store:   runtime.opts.TraceStore,
	}
}

// OperationService owns operation admission, idempotency, queueing, and
// lifecycle commit state. Runtime exposes its methods as a compatibility
// facade, but does not synchronize or mutate these fields directly.
type OperationService struct {
	*Runtime

	mu           sync.Mutex
	operations   chan acceptedOperation
	processed    uint64
	accepted     map[protocol.OperationID]PendingOperation
	acceptedKeys map[string]protocol.OperationID
	committed    map[protocol.OperationID]PendingOperation
	accepting    bool
}

func (s *OperationService) snapshot() (processed uint64, pending int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processed, len(s.accepted)
}

func (s *OperationService) hasPendingSession(sessionID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, operation := range s.accepted {
		if operation.SessionID == sessionID {
			return true
		}
	}
	return false
}

func (s *OperationService) pendingOperations() []PendingOperation {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := make([]PendingOperation, 0, len(s.accepted))
	for _, operation := range s.accepted {
		operation.Canonical = append([]byte(nil), operation.Canonical...)
		pending = append(pending, operation)
	}
	return pending
}

func (s *OperationService) pendingSnapshot() map[protocol.OperationID]PendingOperation {
	pending := s.pendingOperations()
	result := make(map[protocol.OperationID]PendingOperation, len(pending))
	for _, operation := range pending {
		result[operation.ID] = operation
	}
	return result
}

// TurnService owns active Turn leases and execution goroutine lifetime. The
// Runtime facade delegates Turn admission and control to this owner.
type TurnService struct {
	runtime *Runtime
	active  *ActiveTurnRegistry
	workers sync.WaitGroup
}

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
func sessionBusyProblem(message string, summary protocol.SessionSummary) *protocol.Problem {
	return protocol.NewProblemWithDetails(protocol.CodeConflict, message, true,
		protocol.ProblemDetails{Reason: protocol.ProblemReasonSessionBusy,
			ResourceID: summary.SessionID, SessionStatus: string(summary.Status)}, nil)
}
