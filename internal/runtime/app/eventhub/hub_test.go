package eventhub_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/eventhub"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestHubReplaysSubscribesAndDropsSlowConsumer(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	dropped := 0
	hub := eventhub.New(eventhub.Config{
		Store: app.NewMemoryEventStore(8), Buffer: 1, Context: ctx,
		Closed: app.ErrClosed, CursorAhead: app.ErrCursorAhead,
		ReplayOverflow: func(cursor protocol.Cursor, limit int) error {
			return &app.ReplayLimitError{Requested: cursor, Limit: limit}
		},
		OnDropped: func() { dropped++ },
	})
	meta := protocol.EventMeta{OperationID: "op", ThreadID: "thread", TurnID: "turn", ItemID: "item"}
	if err := hub.Publish(meta, &protocol.OutputDeltaData{Text: "one"}, func(protocol.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	events, err := hub.Events(t.Context(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if event := <-events; event.Sequence != 1 {
		t.Fatalf("replayed sequence = %d", event.Sequence)
	}
	if err := hub.Publish(meta, &protocol.OutputDeltaData{Text: "two"}, func(protocol.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := hub.Publish(meta, &protocol.OutputDeltaData{Text: "three"}, func(protocol.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := hub.Publish(meta, &protocol.OutputDeltaData{Text: "four"}, func(protocol.Event) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if dropped != 1 {
		t.Fatalf("dropped subscribers = %d", dropped)
	}
}

func TestHubStableIdentityIsIdempotent(t *testing.T) {
	store := app.NewMemoryEventStore(8)
	hub := eventhub.New(eventhub.Config{
		Store: store, Context: t.Context(), CursorAhead: app.ErrCursorAhead,
	})
	meta := protocol.EventMeta{OperationID: "op", ThreadID: "thread", TurnID: "turn", ItemID: "item"}
	projected := 0
	publish := func() error {
		return hub.PublishStable(meta, "evt_stable", &protocol.TurnCompletedData{}, func(protocol.Event) error {
			projected++
			return nil
		})
	}
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	events, err := store.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || projected != 2 {
		t.Fatalf("events = %d, projections = %d", len(events), projected)
	}
	if _, err := hub.Events(t.Context(), hub.Snapshot().LastSequence+1, 0); !errors.Is(err, app.ErrCursorAhead) {
		t.Fatalf("cursor error = %v", err)
	}
}
