package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestMemoryEventStoreReplayCursorAndClose(t *testing.T) {
	store := NewMemoryEventStore(2)
	for sequence := protocol.Cursor(1); sequence <= 3; sequence++ {
		event := protocol.Event{
			Version: protocol.Version, ID: protocol.EventID("evt_test"),
			Sequence: sequence, OperationID: "op", ThreadID: "thread", TurnID: "turn",
			ItemID: "item", Kind: protocol.EventTurnCompleted,
			CreatedAt: time.Now().UTC(), Data: &protocol.TurnCompletedData{},
		}
		if err := store.Append(t.Context(), event); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.Replay(t.Context(), 0); !errors.Is(err, ErrCursorGap) {
		t.Fatalf("stale replay error = %v", err)
	}
	if _, err := store.Replay(t.Context(), 4); !errors.Is(err, ErrCursorAhead) {
		t.Fatalf("ahead replay error = %v", err)
	}
	replay, err := store.Replay(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != 2 || replay[0].Sequence != 2 || replay[1].Sequence != 3 {
		t.Fatalf("replay = %+v", replay)
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Replay(t.Context(), 3); !errors.Is(err, ErrClosed) {
		t.Fatalf("replay after close error = %v", err)
	}
}

func TestMemoryEventStoreRejectsSequenceGap(t *testing.T) {
	store := NewMemoryEventStore(2)
	event := protocol.Event{
		Version: protocol.Version, ID: "evt_test", Sequence: 2, OperationID: "op",
		ThreadID: "thread", TurnID: "turn", ItemID: "item", Kind: protocol.EventTurnCompleted,
		CreatedAt: time.Now().UTC(), Data: &protocol.TurnCompletedData{},
	}
	if err := store.Append(context.Background(), event); !protocol.IsCode(err, protocol.CodeConflict) {
		t.Fatalf("Append() error = %v", err)
	}
}

func TestMemoryContentStoreCopiesValuesAndCloses(t *testing.T) {
	store := NewMemoryContentStore()
	input := []byte("hello")
	if err := store.Put(t.Context(), "content", input); err != nil {
		t.Fatal(err)
	}
	input[0] = 'x'
	got, err := store.Get(t.Context(), "content")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("content = %q", got)
	}
	got[0] = 'x'
	again, _ := store.Get(t.Context(), "content")
	if string(again) != "hello" {
		t.Fatal("Get returned aliased content")
	}
	if err := store.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(t.Context(), "other", nil); !errors.Is(err, ErrClosed) {
		t.Fatalf("Put after close error = %v", err)
	}
}
