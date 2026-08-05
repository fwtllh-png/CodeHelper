package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type stubEngine struct{}

func (stubEngine) StartTurn(context.Context, *protocol.StartTurnPayload, app.EngineSink) error {
	return nil
}
func (stubEngine) CancelTurn(context.Context, *protocol.CancelTurnPayload, app.EngineSink) error {
	return nil
}
func (stubEngine) SteerTurn(context.Context, *protocol.SteerTurnPayload, app.EngineSink) error {
	return nil
}
func (stubEngine) DecideApproval(context.Context, *protocol.ApprovalDecisionPayload, app.EngineSink) error {
	return nil
}
func (stubEngine) ReplyInput(context.Context, *protocol.InputReplyPayload, app.EngineSink) error {
	return nil
}
func (stubEngine) CompactThread(context.Context, *protocol.CompactThreadPayload, app.EngineSink) error {
	return nil
}
func (stubEngine) ForkThread(context.Context, *protocol.ForkThreadPayload, app.EngineSink) error {
	return nil
}
func (stubEngine) RevertTurn(context.Context, *protocol.RevertTurnPayload, app.EngineSink) error {
	return nil
}

func TestOpenEventStreamSurvivesRingTrim(t *testing.T) {
	const capacity = 16
	runtime := app.NewRuntime(app.Options{
		Engine: stubEngine{}, OperationBuffer: 64, EventHistory: capacity, SubscriberBuffer: 8,
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = runtime.Close(ctx)
	})

	for i := 0; i < capacity*3; i++ {
		op, err := protocol.NewOperation(&protocol.StartTurnPayload{
			ThreadID: protocol.ThreadID(fmt.Sprintf("thread_%d", i)),
			TurnID:   protocol.TurnID(fmt.Sprintf("turn_%d", i)),
			ItemID:   protocol.ItemID(fmt.Sprintf("item_%d", i)),
			Prompt:   "x",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Submit(context.Background(), op); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.Snapshot(context.Background()).OperationsProcessed < uint64(capacity*3) {
		if time.Now().After(deadline) {
			t.Fatal("runtime did not drain operations")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := runtime.Events(context.Background(), 0); err == nil {
		t.Fatal("expected stale cursor 0 after ring trim")
	} else {
		var gap *app.CursorGapError
		if !errors.As(err, &gap) {
			t.Fatalf("want CursorGapError, got %v", err)
		}
	}

	host := &SessionHost{runtime: runtime, out: make(chan tea.Msg, 8)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := host.openEventStream(ctx)
	if err != nil {
		t.Fatalf("openEventStream after trim: %v", err)
	}
	if events == nil {
		t.Fatal("expected event channel")
	}
	if host.eventCursor == 0 {
		t.Fatal("eventCursor should advance off zero")
	}
}
