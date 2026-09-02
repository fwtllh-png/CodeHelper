package turnkernel

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

type RecoveredInteraction[T any] struct {
	pending map[string]struct{}
	early   map[string]T
}

func (r *RecoveredInteraction[T]) Mark(requestID string) {
	if r.pending == nil {
		r.pending = make(map[string]struct{})
		r.early = make(map[string]T)
	}
	r.pending[requestID] = struct{}{}
}

func (r *RecoveredInteraction[T]) Queue(
	requestID string,
	value T,
) bool {
	if _, recovered := r.pending[requestID]; !recovered {
		return false
	}
	if _, queued := r.early[requestID]; queued {
		return false
	}
	r.early[requestID] = value
	return true
}

func (r *RecoveredInteraction[T]) Take(requestID string) (T, bool) {
	delete(r.pending, requestID)
	value, ok := r.early[requestID]
	delete(r.early, requestID)
	return value, ok
}

const DefaultMailboxCapacity = 64

type Mailbox[T any] struct {
	queue chan T
}

func NewMailbox[T any](capacity int) *Mailbox[T] {
	if capacity <= 0 {
		capacity = DefaultMailboxCapacity
	}
	return &Mailbox[T]{queue: make(chan T, capacity)}
}

func (m *Mailbox[T]) Offer(value T) error {
	if m == nil {
		return errors.New("turn mailbox is unavailable")
	}
	select {
	case m.queue <- value:
		return nil
	default:
		return protocol.NewProblem(
			protocol.CodeResourceExhausted,
			"turn control mailbox is full",
			true,
			nil,
		)
	}
}

func (m *Mailbox[T]) Drain() []T {
	if m == nil {
		return nil
	}
	result := make([]T, 0, len(m.queue))
	for {
		select {
		case value := <-m.queue:
			result = append(result, value)
		default:
			return result
		}
	}
}

func (m *Mailbox[T]) Len() int {
	if m == nil {
		return 0
	}
	return len(m.queue)
}

type RequestKind string

const (
	RequestApproval RequestKind = "approval"
	RequestInput    RequestKind = "input"
)

type RequestLedger struct {
	mu       sync.Mutex
	pending  map[string]RequestKind
	resolved map[string]RequestKind
}

func NewRequestLedger() *RequestLedger {
	return &RequestLedger{
		pending:  make(map[string]RequestKind),
		resolved: make(map[string]RequestKind),
	}
}

func (l *RequestLedger) Register(kind RequestKind, requestID string) error {
	if requestID = strings.TrimSpace(requestID); requestID == "" {
		return protocol.NewProblem(protocol.CodeInvalidArgument, "control request id is required", false, nil)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.resolved[requestID]; ok {
		return requestConflict(requestID, existing, "late")
	}
	if existing, ok := l.pending[requestID]; ok && existing != kind {
		return requestConflict(requestID, existing, "kind_mismatch")
	}
	l.pending[requestID] = kind
	return nil
}

func (l *RequestLedger) Resolve(kind RequestKind, requestID string) error {
	if requestID = strings.TrimSpace(requestID); requestID == "" {
		return protocol.NewProblem(protocol.CodeInvalidArgument, "control request id is required", false, nil)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if existing, ok := l.resolved[requestID]; ok {
		return requestConflict(requestID, existing, "duplicate")
	}
	existing, ok := l.pending[requestID]
	if !ok {
		return requestConflict(requestID, kind, "late")
	}
	if existing != kind {
		return requestConflict(requestID, existing, "kind_mismatch")
	}
	delete(l.pending, requestID)
	l.resolved[requestID] = kind
	return nil
}

func (l *RequestLedger) Pending() map[string]RequestKind {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	result := make(map[string]RequestKind, len(l.pending))
	for id, kind := range l.pending {
		result[id] = kind
	}
	return result
}

func requestConflict(
	requestID string,
	kind RequestKind,
	reason string,
) error {
	return protocol.NewProblem(
		protocol.CodeConflict,
		fmt.Sprintf(
			"%s control request %q is %s",
			kind,
			requestID,
			reason,
		),
		false,
		nil,
	)
}
