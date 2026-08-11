package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type failOnceAppendStore struct {
	EventStore

	mu       sync.Mutex
	kind     protocol.EventKind
	failed   bool
	attempts int
}

func (s *failOnceAppendStore) Append(
	ctx context.Context,
	event protocol.Event,
) error {
	s.mu.Lock()
	if event.Kind == s.kind {
		s.attempts++
		if !s.failed {
			s.failed = true
			s.mu.Unlock()
			return errors.New("injected durable event append failure")
		}
	}
	s.mu.Unlock()
	return s.EventStore.Append(ctx, event)
}

func (s *failOnceAppendStore) appendAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

type toolLifecycleTestEngine struct {
	testEngine
}

func (*toolLifecycleTestEngine) StartTurn(
	_ context.Context,
	payload *protocol.StartTurnPayload,
	sink EngineSink,
) error {
	if err := sink.Emit(&protocol.TurnStartedData{
		Provider: "test",
		Model:    "test",
	}); err != nil {
		return err
	}
	if err := sink.Emit(&protocol.ToolStartData{
		Tool:      "echo",
		CallID:    "call-1",
		Arguments: []byte(`{"text":"hello"}`),
	}); err != nil {
		return err
	}
	if err := sink.Emit(&protocol.ToolResultData{
		Tool:   "echo",
		CallID: "call-1",
		Output: "hello",
	}); err != nil {
		return err
	}
	return sink.Emit(&protocol.TurnCompletedData{Text: payload.Prompt})
}

func TestRuntimeRetriesTransientDurableToolLifecycleFailure(t *testing.T) {
	for _, kind := range []protocol.EventKind{
		protocol.EventToolStart,
		protocol.EventToolResult,
	} {
		t.Run(string(kind), func(t *testing.T) {
			store := &failOnceAppendStore{
				EventStore: NewMemoryEventStore(16),
				kind:       kind,
			}
			runtime := NewRuntime(Options{
				Engine:           &toolLifecycleTestEngine{},
				EventStore:       store,
				SubscriberBuffer: 8,
			})
			t.Cleanup(func() { closeRuntime(t, runtime) })
			events, err := runtime.Events(t.Context(), 0)
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.Submit(t.Context(), startOperation(t, 1)); err != nil {
				t.Fatal(err)
			}

			want := []protocol.EventKind{
				protocol.EventTurnStarted,
				protocol.EventToolStart,
				protocol.EventToolResult,
				protocol.EventTurnCompleted,
			}
			for index, wantKind := range want {
				event := receiveEvent(t, events)
				if event.Sequence != protocol.Cursor(index+1) ||
					event.Kind != wantKind {
					t.Fatalf(
						"event[%d] = sequence %d kind %s, want %d %s",
						index,
						event.Sequence,
						event.Kind,
						index+1,
						wantKind,
					)
				}
			}
			if attempts := store.appendAttempts(); attempts != 2 {
				t.Fatalf("Append attempts for %s = %d, want 2", kind, attempts)
			}
		})
	}
}

type failTerminalAppendStore struct {
	EventStore

	mu       sync.Mutex
	attempts int
}

func (s *failTerminalAppendStore) Append(
	ctx context.Context,
	event protocol.Event,
) error {
	if protocol.IsTerminalEvent(event.Kind) {
		s.mu.Lock()
		s.attempts++
		s.mu.Unlock()
		return errors.New("injected terminal append failure")
	}
	return s.EventStore.Append(ctx, event)
}

func (s *failTerminalAppendStore) terminalAttempts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attempts
}

type terminalEnvelopeTestEngine struct {
	testEngine
}

func (*terminalEnvelopeTestEngine) StartTurn(
	_ context.Context,
	payload *protocol.StartTurnPayload,
	sink EngineSink,
) error {
	if err := sink.Emit(&protocol.TurnStartedData{
		Provider: "test",
		Model:    "test",
		Intent:   protocol.TurnIntentAnswer,
	}); err != nil {
		return err
	}
	if err := sink.Emit(&protocol.ExecutionReceiptData{
		Goal:    payload.Prompt,
		Intent:  protocol.TurnIntentAnswer,
		Outcome: protocol.TurnOutcomeAnswered,
	}); err != nil {
		return err
	}
	return sink.Emit(&protocol.TurnCompletedData{
		Text:    payload.Prompt,
		Outcome: protocol.TurnOutcomeAnswered,
	})
}

func TestPhase4RBaselineTerminalAppendFailureLeavesDurableReceipt(t *testing.T) {
	store := &failTerminalAppendStore{
		EventStore: NewMemoryEventStore(16),
	}
	runtime := NewRuntime(Options{
		Engine:           &terminalEnvelopeTestEngine{},
		EventStore:       store,
		SubscriberBuffer: 8,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	operation := startOperation(t, 51)
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		return store.terminalAttempts() == 4 &&
			runtime.Snapshot(t.Context()).ActiveTurns == 0
	})

	events, err := store.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var receipts, terminals int
	for _, event := range events {
		if event.Kind == protocol.EventExecutionReceipt {
			receipts++
		}
		if protocol.IsTerminalEvent(event.Kind) {
			terminals++
		}
	}
	if receipts != 1 || terminals != 0 {
		t.Fatalf(
			"split terminal baseline: receipts=%d terminals=%d events=%+v",
			receipts,
			terminals,
			events,
		)
	}
}

type commitFailureLifecycle struct {
	mu       sync.Mutex
	events   []protocol.Event
	commits  int
	accepted map[protocol.OperationID]PendingOperation
}

func newCommitFailureLifecycle() *commitFailureLifecycle {
	return &commitFailureLifecycle{
		accepted: make(map[protocol.OperationID]PendingOperation),
	}
}

func (l *commitFailureLifecycle) Recover(context.Context) (RecoveryState, error) {
	return RecoveryState{}, nil
}

func (l *commitFailureLifecycle) Accept(
	_ context.Context,
	operation protocol.Operation,
	idempotencyKey string,
	canonical json.RawMessage,
) (Acceptance, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.accepted[operation.ID] = PendingOperation{
		ID: operation.ID, IdempotencyKey: idempotencyKey,
		Canonical: append([]byte(nil), canonical...),
	}
	return Acceptance{OperationID: operation.ID}, nil
}

func (l *commitFailureLifecycle) Project(
	_ context.Context,
	event protocol.Event,
) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
	return nil
}

func (l *commitFailureLifecycle) Commit(
	context.Context,
	CommitReceipt,
) error {
	l.mu.Lock()
	l.commits++
	l.mu.Unlock()
	return errors.New("injected operation commit failure")
}

func (l *commitFailureLifecycle) snapshot() ([]protocol.Event, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]protocol.Event(nil), l.events...), l.commits
}

func TestC4DurableRuntimeRejectsNonAtomicTerminalEngine(t *testing.T) {
	lifecycle := newCommitFailureLifecycle()
	runtime := NewRuntime(Options{
		Engine:           &terminalEnvelopeTestEngine{},
		Lifecycle:        lifecycle,
		SubscriberBuffer: 8,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	if err := runtime.Submit(t.Context(), startOperation(t, 52)); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		_, commits := lifecycle.snapshot()
		return commits == 1 && runtime.Snapshot(t.Context()).ActiveTurns == 0
	})

	events, commits := lifecycle.snapshot()
	var terminal bool
	for _, event := range events {
		terminal = terminal || protocol.IsTerminalEvent(event.Kind)
	}
	snapshot := runtime.Snapshot(t.Context())
	if terminal || commits != 1 || snapshot.PendingOperations != 1 {
		t.Fatalf(
			"non-atomic terminal: terminal=%v commits=%d pending=%d events=%+v",
			terminal,
			commits,
			snapshot.PendingOperations,
			events,
		)
	}
}

type rejectInteractionEngine struct {
	testEngine
}

func (*rejectInteractionEngine) DecideApproval(
	_ context.Context,
	_ *protocol.ApprovalDecisionPayload,
	_ EngineSink,
) error {
	return errors.New("injected approval result rejection")
}

func (*rejectInteractionEngine) ReplyInput(
	_ context.Context,
	_ *protocol.InputReplyPayload,
	_ EngineSink,
) error {
	return errors.New("injected input result rejection")
}

func TestPhase4RRejectedInteractionDoesNotPublishResolved(t *testing.T) {
	testCases := []struct {
		name      string
		requestID string
		resolved  protocol.EventKind
		prepare   func(*Runtime, protocol.ThreadID, protocol.TurnID)
		operation func(*testing.T, protocol.ThreadID, protocol.TurnID, string) protocol.Operation
	}{
		{
			name:      "approval",
			requestID: "approval-phase4r",
			resolved:  protocol.EventApprovalResolved,
			prepare: func(runtime *Runtime, threadID protocol.ThreadID, turnID protocol.TurnID) {
				runtime.approvals["approval-phase4r"] = PendingApproval{
					RequestID: "approval-phase4r",
					ThreadID:  threadID,
					TurnID:    turnID,
				}
			},
			operation: func(
				t *testing.T,
				threadID protocol.ThreadID,
				turnID protocol.TurnID,
				requestID string,
			) protocol.Operation {
				operation, err := protocol.NewOperation(&protocol.ApprovalDecisionPayload{
					ThreadID:  threadID,
					TurnID:    turnID,
					ItemID:    protocol.ItemID("item-approval"),
					RequestID: requestID,
					Decision:  protocol.ApprovalApprove,
					Scope:     protocol.ApprovalScopeOnce,
				})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
		{
			name:      "input",
			requestID: "input-phase4r",
			resolved:  protocol.EventInputResolved,
			prepare: func(runtime *Runtime, threadID protocol.ThreadID, turnID protocol.TurnID) {
				runtime.inputs["input-phase4r"] = PendingInput{
					RequestID: "input-phase4r",
					ThreadID:  threadID,
					TurnID:    turnID,
				}
			},
			operation: func(
				t *testing.T,
				threadID protocol.ThreadID,
				turnID protocol.TurnID,
				requestID string,
			) protocol.Operation {
				operation, err := protocol.NewOperation(&protocol.InputReplyPayload{
					ThreadID:  threadID,
					TurnID:    turnID,
					ItemID:    protocol.ItemID("item-input"),
					RequestID: requestID,
					Answer:    "continue",
				})
				if err != nil {
					t.Fatal(err)
				}
				return operation
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := NewRuntime(Options{
				Engine:           &rejectInteractionEngine{},
				SubscriberBuffer: 8,
			})
			t.Cleanup(func() { closeRuntime(t, runtime) })
			threadID := protocol.ThreadID("thread-" + testCase.name)
			turnID := protocol.TurnID("turn-" + testCase.name)
			testCase.prepare(runtime, threadID, turnID)
			operation := testCase.operation(
				t,
				threadID,
				turnID,
				testCase.requestID,
			)
			if err := runtime.Submit(t.Context(), operation); err != nil {
				t.Fatal(err)
			}
			waitForProcessed(t, runtime, 1)

			events, _, err := runtime.ReplayEvents(t.Context(), 0, 8)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 ||
				events[0].Kind != protocol.EventOperationRejected {
				t.Fatalf("interaction events = %+v", events)
			}
			snapshot := runtime.Snapshot(t.Context())
			if testCase.resolved == protocol.EventApprovalResolved &&
				snapshot.PendingApprovals != 1 {
				t.Fatalf("rejected approval was removed: %+v", snapshot)
			}
			if testCase.resolved == protocol.EventInputResolved &&
				snapshot.PendingInputs != 1 {
				t.Fatalf("rejected input was removed: %+v", snapshot)
			}
		})
	}
}

type rejectCancelEngine struct {
	testEngine
	started chan struct{}
}

func (e *rejectCancelEngine) StartTurn(
	ctx context.Context,
	_ *protocol.StartTurnPayload,
	sink EngineSink,
) error {
	if err := sink.Emit(&protocol.TurnStartedData{
		Provider: "test",
		Model:    "test",
	}); err != nil {
		return err
	}
	close(e.started)
	<-ctx.Done()
	return ctx.Err()
}

func (*rejectCancelEngine) CancelTurn(
	context.Context,
	*protocol.CancelTurnPayload,
	EngineSink,
) error {
	return errors.New("injected cancel command rejection")
}

func TestPhase4RRejectedCancelLeavesTurnActive(t *testing.T) {
	engine := &rejectCancelEngine{
		started: make(chan struct{}),
	}
	runtime := NewRuntime(Options{
		Engine:           engine,
		SubscriberBuffer: 8,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	start := startOperation(t, 53)
	if err := runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	select {
	case <-engine.started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn did not start")
	}
	threadID, turnID, _ := protocol.OperationReferences(start)
	cancel, err := protocol.NewOperation(&protocol.CancelTurnPayload{
		ThreadID: threadID,
		TurnID:   turnID,
		ItemID:   protocol.ItemID("item-cancel"),
		Reason:   protocol.CancelReasonUserInterrupted,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), cancel); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool {
		return runtime.Snapshot(t.Context()).OperationsProcessed == 2
	})

	events, _, replayErr := runtime.ReplayEvents(t.Context(), 0, 8)
	if replayErr != nil {
		t.Fatal(replayErr)
	}
	var rejectedCancel, canceledTurn bool
	for _, event := range events {
		rejectedCancel = rejectedCancel ||
			(event.OperationID == cancel.ID &&
				event.Kind == protocol.EventOperationRejected)
		canceledTurn = canceledTurn ||
			(event.OperationID == cancel.ID &&
				event.Kind == protocol.EventTurnCanceled)
	}
	if !rejectedCancel || canceledTurn ||
		runtime.Snapshot(t.Context()).ActiveTurns != 1 {
		t.Fatalf(
			"cancel=%s rejected=%v canceled=%v snapshot=%+v events=%+v",
			cancel.ID,
			rejectedCancel,
			canceledTurn,
			runtime.Snapshot(t.Context()),
			events,
		)
	}
}

func waitForCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for fault-injection condition")
		}
		time.Sleep(time.Millisecond)
	}
}
