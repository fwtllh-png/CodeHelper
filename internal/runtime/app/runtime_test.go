package app

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type testEngine struct {
	block bool
}

func (e *testEngine) StartTurn(
	ctx context.Context, payload *protocol.StartTurnPayload, sink EngineSink,
) error {
	if err := sink.Emit(&protocol.TurnStartedData{Provider: "test", Model: "test"}); err != nil {
		return err
	}
	if e.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := sink.Emit(&protocol.OutputDeltaData{Text: payload.Prompt}); err != nil {
		return err
	}
	return sink.Emit(&protocol.TurnCompletedData{Text: payload.Prompt})
}

func (*testEngine) CancelTurn(context.Context, *protocol.CancelTurnPayload, EngineSink) error {
	return nil
}
func (*testEngine) SteerTurn(_ context.Context, payload *protocol.SteerTurnPayload, sink EngineSink) error {
	return sink.Emit(&protocol.TurnSteeredData{Prompt: payload.Prompt})
}
func (*testEngine) DecideApproval(_ context.Context, payload *protocol.ApprovalDecisionPayload, sink EngineSink) error {
	return sink.Emit(&protocol.ApprovalResolvedData{RequestID: payload.RequestID, Decision: payload.Decision})
}
func (*testEngine) ReplyInput(_ context.Context, payload *protocol.InputReplyPayload, sink EngineSink) error {
	return sink.Emit(&protocol.InputResolvedData{RequestID: payload.RequestID, Answer: payload.Answer})
}
func (*testEngine) CompactThread(context.Context, *protocol.CompactThreadPayload, EngineSink) error {
	return errors.New("compact unsupported")
}
func (*testEngine) ForkThread(_ context.Context, payload *protocol.ForkThreadPayload, sink EngineSink) error {
	return sink.Emit(&protocol.ThreadForkedData{NewThreadID: payload.NewThreadID})
}
func (*testEngine) RevertTurn(_ context.Context, payload *protocol.RevertTurnPayload, sink EngineSink) error {
	return sink.Emit(&protocol.TurnRevertedData{TargetTurnID: payload.TargetTurnID})
}

func TestRuntimeConcurrentSubmitHasStrictSequenceAndUniqueTerminal(t *testing.T) {
	const count = 32
	runtime := NewRuntime(Options{
		Engine: &testEngine{}, OperationBuffer: count,
		EventHistory: count * 3, SubscriberBuffer: count * 3,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			submitEventually(t, runtime, startOperation(t, index))
		}()
	}
	group.Wait()

	terminals := make(map[protocol.TurnID]int)
	for sequence := protocol.Cursor(1); len(terminals) < count; sequence++ {
		event := receiveEvent(t, events)
		if event.Sequence != sequence {
			t.Fatalf("sequence = %d, want %d", event.Sequence, sequence)
		}
		if protocol.IsTerminalEvent(event.Kind) {
			terminals[event.TurnID]++
		}
	}
	for turnID, terminalCount := range terminals {
		if terminalCount != 1 {
			t.Fatalf("turn %s terminal count = %d", turnID, terminalCount)
		}
	}
}

func TestRuntimeCancelActuallyCancelsActiveTurn(t *testing.T) {
	runtime := NewRuntime(Options{Engine: &testEngine{block: true}})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start := startOperation(t, 1)
	if err := runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	started := receiveEvent(t, events)
	if started.Kind != protocol.EventTurnStarted {
		t.Fatalf("first event = %s", started.Kind)
	}
	threadID, turnID, _ := protocol.OperationReferences(start)
	cancel, err := protocol.NewOperation(&protocol.CancelTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: "cancel_item", Reason: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), cancel); err != nil {
		t.Fatal(err)
	}
	terminal := receiveEvent(t, events)
	if terminal.Kind != protocol.EventTurnCanceled {
		t.Fatalf("terminal = %s, want %s", terminal.Kind, protocol.EventTurnCanceled)
	}
	data, ok := terminal.Data.(*protocol.TurnCanceledData)
	if !ok {
		t.Fatalf("data type = %T", terminal.Data)
	}
	if data.Reason != "test" {
		t.Fatalf("reason = %q, want test", data.Reason)
	}
	if terminal.ItemID != "cancel_item" {
		t.Fatalf("ItemID = %q, want cancel_item", terminal.ItemID)
	}
}

func TestRuntimeUnsupportedOperationIsExplicitlyRejected(t *testing.T) {
	runtime := NewRuntime(Options{Engine: &testEngine{}})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := protocol.NewOperation(&protocol.CompactThreadPayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "item",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	event := receiveEvent(t, events)
	if event.Kind != protocol.EventOperationRejected {
		t.Fatalf("event = %s, want operation.rejected", event.Kind)
	}
}

func TestRuntimeDispatchesControlOperations(t *testing.T) {
	runtime := NewRuntime(Options{Engine: &testEngine{}, SubscriberBuffer: 8})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	// Active turn + pending approval so steer injects and approval resumes.
	runtime.activeMu.Lock()
	runtime.active["turn"] = func() {}
	runtime.activeThreads["thread"] = "turn"
	runtime.activeMu.Unlock()
	runtime.mu.Lock()
	runtime.approvals["approval_1"] = PendingApproval{
		RequestID: "approval_1", ThreadID: "thread", TurnID: "turn",
	}
	runtime.mu.Unlock()
	operations := []struct {
		payload protocol.OperationPayload
		event   protocol.EventKind
	}{
		{
			payload: &protocol.SteerTurnPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "steer", Prompt: "change",
			},
			event: protocol.EventTurnSteered,
		},
		{
			payload: &protocol.ApprovalDecisionPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "approval",
				RequestID: "approval_1", Decision: protocol.ApprovalApprove,
			},
			event: protocol.EventApprovalResolved,
		},
		{
			payload: &protocol.ForkThreadPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "fork", NewThreadID: "forked",
			},
			event: protocol.EventThreadForked,
		},
		{
			payload: &protocol.RevertTurnPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "revert", TargetTurnID: "previous",
			},
			event: protocol.EventTurnReverted,
		},
	}
	for _, test := range operations {
		operation, createErr := protocol.NewOperation(test.payload)
		if createErr != nil {
			t.Fatal(createErr)
		}
		if submitErr := runtime.Submit(t.Context(), operation); submitErr != nil {
			t.Fatal(submitErr)
		}
		event := receiveEvent(t, events)
		if event.Kind != test.event {
			t.Fatalf("event = %s, want %s", event.Kind, test.event)
		}
	}
}

func TestRuntimeDropsSlowSubscriberDeterministically(t *testing.T) {
	runtime := NewRuntime(Options{
		Engine: &testEngine{}, OperationBuffer: 4, EventHistory: 16, SubscriberBuffer: 1,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), startOperation(t, 1)); err != nil {
		t.Fatal(err)
	}
	waitForProcessed(t, runtime, 1)
	deadline := time.After(time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				if runtime.Snapshot(t.Context()).Metrics.SubscribersDropped != 1 {
					t.Fatal("slow subscriber was not counted")
				}
				return
			}
		case <-deadline:
			t.Fatal("slow subscriber was not closed")
		}
	}
}

func TestRuntimeSubmitCloseRaceAccountsForAcceptedOperations(t *testing.T) {
	runtime := NewRuntime(Options{
		Engine: &testEngine{}, OperationBuffer: 64, EventHistory: 256, SubscriberBuffer: 256,
	})
	var accepted atomic.Uint64
	var group sync.WaitGroup
	for index := range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			err := runtime.Submit(context.Background(), startOperation(t, index))
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrClosed) && !errors.Is(err, ErrQueueFull) {
				t.Errorf("Submit() error = %v", err)
			}
		}()
	}
	closeRuntime(t, runtime)
	group.Wait()
	if got := runtime.Snapshot(t.Context()).OperationsProcessed; got != accepted.Load() {
		t.Fatalf("processed = %d, accepted = %d", got, accepted.Load())
	}
}

func TestRuntimeReplayEventsPagesWithoutSubscribing(t *testing.T) {
	runtime := NewRuntime(Options{Engine: &testEngine{}, EventHistory: 64})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	submitEventually(t, runtime, startOperation(t, 1))
	waitForProcessed(t, runtime, 1)
	head := runtime.Snapshot(t.Context()).LastSequence
	if head < 3 {
		t.Fatalf("last sequence = %d, want the turn's three events", head)
	}

	var collected []protocol.Event
	cursor := protocol.Cursor(0)
	for {
		page, more, err := runtime.ReplayEvents(t.Context(), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) > 2 {
			t.Fatalf("page size = %d, want at most 2", len(page))
		}
		collected = append(collected, page...)
		if !more {
			break
		}
		if len(page) == 0 {
			t.Fatal("more events reported but page is empty")
		}
		cursor = page[len(page)-1].Sequence
	}
	if protocol.Cursor(len(collected)) != head {
		t.Fatalf("replayed %d events, want %d", len(collected), head)
	}
	for index, event := range collected {
		if event.Sequence != protocol.Cursor(index+1) {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
	}
	// Paging must not leave a subscriber behind, or a host holding a live
	// subscription would receive every event twice.
	if subscribers := runtime.Snapshot(t.Context()).Subscribers; subscribers != 0 {
		t.Fatalf("subscribers = %d, want 0", subscribers)
	}

	if _, _, err := runtime.ReplayEvents(t.Context(), 0, 0); !protocol.IsCode(
		err, protocol.CodeInvalidArgument,
	) {
		t.Fatalf("zero limit error = %v", err)
	}
	if _, _, err := runtime.ReplayEvents(t.Context(), head+1, 2); !errors.Is(
		err, ErrCursorAhead,
	) {
		t.Fatalf("cursor ahead error = %v", err)
	}
}

func TestRuntimeReplayEventsSurfacesCursorGap(t *testing.T) {
	// A two-event window forces the oldest events out, which is what retention
	// looks like to a reconnecting client.
	runtime := NewRuntime(Options{Engine: &testEngine{}, EventHistory: 2})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	submitEventually(t, runtime, startOperation(t, 1))
	waitForProcessed(t, runtime, 1)

	_, _, err := runtime.ReplayEvents(t.Context(), 0, 8)
	var gap *CursorGapError
	if !errors.As(err, &gap) {
		t.Fatalf("replay from evicted cursor error = %v", err)
	}
	if gap.OldestAvailable == 0 || gap.Latest < gap.OldestAvailable {
		t.Fatalf("gap = %+v", gap)
	}
}

func startOperation(t *testing.T, index int) protocol.Operation {
	t.Helper()
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: protocol.ThreadID(fmt.Sprintf("thread_%d", index)),
		TurnID:   protocol.TurnID(fmt.Sprintf("turn_%d", index)),
		ItemID:   protocol.ItemID(fmt.Sprintf("item_%d", index)),
		Prompt:   "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	return operation
}

func receiveEvent(t *testing.T, events <-chan protocol.Event) protocol.Event {
	t.Helper()
	select {
	case event, open := <-events:
		if !open {
			t.Fatal("event stream closed")
		}
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
		return protocol.Event{}
	}
}

func submitEventually(t *testing.T, runtime *Runtime, operation protocol.Operation) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		err := runtime.Submit(context.Background(), operation)
		if err == nil {
			return
		}
		if !errors.Is(err, ErrQueueFull) || time.Now().After(deadline) {
			t.Fatalf("Submit() error = %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForProcessed(t *testing.T, runtime *Runtime, count uint64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for runtime.Snapshot(t.Context()).OperationsProcessed != count {
		if time.Now().After(deadline) {
			t.Fatalf("runtime did not process %d operations", count)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRuntimeIdleTurnRejectedInPlanMode(t *testing.T) {
	runtime := NewRuntime(Options{
		Engine: &planGatedEngine{mode: "plan"}, SubscriberBuffer: 8,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread-idle", TurnID: "turn-idle", ItemID: "item-idle",
		Prompt: "auto work", Idle: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), operation); err != nil {
		t.Fatal(err)
	}
	event := receiveEvent(t, events)
	if event.Kind != protocol.EventOperationRejected {
		t.Fatalf("event = %s, want operation.rejected", event.Kind)
	}

	// Non-idle user turns still start in plan mode.
	userOp, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread-user", TurnID: "turn-user", ItemID: "item-user",
		Prompt: "please plan",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), userOp); err != nil {
		t.Fatal(err)
	}
	started := receiveEvent(t, events)
	if started.Kind != protocol.EventTurnStarted {
		t.Fatalf("user turn event = %s, want turn.started", started.Kind)
	}
}

type planGatedEngine struct {
	testEngine
	mode string
}

func (e *planGatedEngine) AllowIdleTurn() error {
	if e.mode == "plan" {
		return protocol.NewProblem(
			protocol.CodeConflict, "plan mode rejects automatic idle turns", false, nil,
		)
	}
	return nil
}

func TestRuntimeToolAndApprovalGetOwnedItemIDs(t *testing.T) {
	runtime := NewRuntime(Options{
		Engine: &itemOwningEngine{}, SubscriberBuffer: 16,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start := startOperation(t, 42)
	if err := runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	_, startTurnItem := mustReceiveKinds(t, events,
		protocol.EventTurnStarted,
		protocol.EventApprovalRequired,
		protocol.EventToolResult,
		protocol.EventTurnCompleted,
	)
	approval := mustFindKind(t, startTurnItem, protocol.EventApprovalRequired)
	tool := mustFindKind(t, startTurnItem, protocol.EventToolResult)
	completed := mustFindKind(t, startTurnItem, protocol.EventTurnCompleted)
	_, _, startItem := protocol.OperationReferences(start)
	if approval.ItemID == "" || approval.ItemID == startItem {
		t.Fatalf("approval ItemID = %q start = %q", approval.ItemID, startItem)
	}
	if tool.ItemID == "" || tool.ItemID == startItem || tool.ItemID == approval.ItemID {
		t.Fatalf("tool ItemID = %q approval = %q start = %q", tool.ItemID, approval.ItemID, startItem)
	}
	if completed.ItemID != startItem {
		t.Fatalf("completed ItemID = %q want start %q", completed.ItemID, startItem)
	}
}

type itemOwningEngine struct{}

func (*itemOwningEngine) StartTurn(
	_ context.Context, _ *protocol.StartTurnPayload, sink EngineSink,
) error {
	if err := sink.Emit(&protocol.TurnStartedData{Provider: "test", Model: "test"}); err != nil {
		return err
	}
	if err := sink.Emit(&protocol.ApprovalRequiredData{
		RequestID: "req-1", CallID: "call-1", Tool: "shell_run",
		Arguments: []byte(`{}`), ArgumentsDigest: "x",
		ExpiresAt:     time.Now().Add(time.Minute),
		AllowedScopes: []protocol.ApprovalScope{protocol.ApprovalScopeOnce},
	}); err != nil {
		return err
	}
	if err := sink.Emit(&protocol.ToolResultData{
		Tool: "shell_run", CallID: "call-1", Output: "ok",
	}); err != nil {
		return err
	}
	return nil
}
func (*itemOwningEngine) CancelTurn(context.Context, *protocol.CancelTurnPayload, EngineSink) error {
	return nil
}
func (*itemOwningEngine) SteerTurn(context.Context, *protocol.SteerTurnPayload, EngineSink) error {
	return nil
}
func (*itemOwningEngine) DecideApproval(context.Context, *protocol.ApprovalDecisionPayload, EngineSink) error {
	return nil
}
func (*itemOwningEngine) ReplyInput(context.Context, *protocol.InputReplyPayload, EngineSink) error {
	return nil
}
func (*itemOwningEngine) CompactThread(context.Context, *protocol.CompactThreadPayload, EngineSink) error {
	return nil
}
func (*itemOwningEngine) ForkThread(context.Context, *protocol.ForkThreadPayload, EngineSink) error {
	return nil
}
func (*itemOwningEngine) RevertTurn(context.Context, *protocol.RevertTurnPayload, EngineSink) error {
	return nil
}

func mustReceiveKinds(t *testing.T, events <-chan protocol.Event, kinds ...protocol.EventKind) (protocol.ItemID, []protocol.Event) {
	t.Helper()
	out := make([]protocol.Event, 0, len(kinds))
	var startItem protocol.ItemID
	for range kinds {
		event := receiveEvent(t, events)
		out = append(out, event)
		if event.Kind == protocol.EventTurnStarted {
			startItem = event.ItemID
		}
	}
	return startItem, out
}

func mustFindKind(t *testing.T, events []protocol.Event, kind protocol.EventKind) protocol.Event {
	t.Helper()
	for _, event := range events {
		if event.Kind == kind {
			return event
		}
	}
	t.Fatalf("missing event kind %s in %+v", kind, events)
	return protocol.Event{}
}

func TestRuntimeRestoreBackfillsOwnedItemMaps(t *testing.T) {
	runtime := NewRuntime(Options{Engine: &testEngine{}})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	runtime.restore(RecoveryState{
		PendingApprovals: map[string]PendingApproval{
			"req-a": {RequestID: "req-a", ItemID: "item-approval"},
		},
		PendingInputs: map[string]PendingInput{
			"req-i": {RequestID: "req-i", ItemID: "item-input"},
		},
		ToolItems: map[string]protocol.ItemID{
			"call-1": "item-tool",
		},
	})
	if got := runtime.approvalItems["req-a"]; got != "item-approval" {
		t.Fatalf("approvalItems = %q", got)
	}
	if got := runtime.inputItems["req-i"]; got != "item-input" {
		t.Fatalf("inputItems = %q", got)
	}
	if got := runtime.toolItems["call-1"]; got != "item-tool" {
		t.Fatalf("toolItems = %q", got)
	}
}

func closeRuntime(t *testing.T, runtime *Runtime) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := runtime.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
