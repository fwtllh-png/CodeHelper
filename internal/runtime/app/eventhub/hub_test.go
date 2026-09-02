package eventhub_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/app"
	"github.com/fwtllh-png/QCode/internal/runtime/app/eventhub"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
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

func TestHubStableRetryAnnouncesAfterProjectionRecovers(t *testing.T) {
	store := app.NewMemoryEventStore(8)
	observed := 0
	hub := eventhub.New(eventhub.Config{
		Store: store, Buffer: 2, Context: t.Context(),
		OnEvent: func(protocol.Event) {
			observed++
		},
	})
	events, err := hub.Events(t.Context(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	meta := protocol.EventMeta{
		OperationID: "op", ThreadID: "thread",
		TurnID: "turn", ItemID: "item",
	}
	projections := 0
	project := func(protocol.Event) error {
		projections++
		if projections == 1 {
			return errors.New("projection unavailable")
		}
		return nil
	}
	if err := hub.PublishStable(
		meta,
		"evt_retry",
		&protocol.TurnCompletedData{},
		project,
	); err == nil {
		t.Fatal("initial projection unexpectedly succeeded")
	}
	select {
	case event := <-events:
		t.Fatalf("failed projection announced event %s", event.ID)
	default:
	}
	if err := hub.PublishStable(
		meta,
		"evt_retry",
		&protocol.TurnCompletedData{},
		project,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		if event.ID != "evt_retry" {
			t.Fatalf("announced event = %s", event.ID)
		}
	case <-time.After(time.Second):
		t.Fatal("recovered projection was not announced")
	}
	if observed != 1 {
		t.Fatalf("observed events = %d, want 1", observed)
	}
	if err := hub.PublishStable(
		meta,
		"evt_retry",
		&protocol.TurnCompletedData{},
		project,
	); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		t.Fatalf("idempotent retry re-announced event %s", event.ID)
	default:
	}
	if observed != 1 {
		t.Fatalf("observed events after duplicate = %d, want 1", observed)
	}
}

func TestHubObservesAfterProjectionWithoutSubscriber(t *testing.T) {
	var phases []string
	hub := eventhub.New(eventhub.Config{
		Store: app.NewMemoryEventStore(8), Context: t.Context(),
		OnEvent: func(event protocol.Event) {
			phases = append(phases, "observe:"+string(event.Kind))
		},
	})
	err := hub.Publish(
		protocol.EventMeta{
			OperationID: "op", ThreadID: "thread",
			TurnID: "turn", ItemID: "item",
		},
		&protocol.TurnCompletedData{},
		func(protocol.Event) error {
			phases = append(phases, "project")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(phases) != 2 || phases[0] != "project" ||
		phases[1] != "observe:turn.completed" {
		t.Fatalf("event phases = %v", phases)
	}
	if subscribers := hub.Snapshot().Subscribers; subscribers != 0 {
		t.Fatalf("observer created %d subscribers", subscribers)
	}
}

func TestHubDoesNotHoldStateLockAcrossStoreOrProjection(t *testing.T) {
	store := &blockingStore{
		Store:   app.NewMemoryEventStore(8),
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	hub := eventhub.New(eventhub.Config{
		Store: store, Context: t.Context(),
	})
	projectEntered := make(chan struct{})
	projectRelease := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- hub.Publish(
			protocol.EventMeta{
				OperationID: "op", ThreadID: "thread",
				TurnID: "turn", ItemID: "item",
			},
			&protocol.OutputDeltaData{Text: "one"},
			func(protocol.Event) error {
				close(projectEntered)
				<-projectRelease
				return nil
			},
		)
	}()
	<-store.entered
	assertSnapshotResponsive(t, hub)
	close(store.release)
	<-projectEntered
	assertSnapshotResponsive(t, hub)
	close(projectRelease)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func assertSnapshotResponsive(t *testing.T, hub *eventhub.Hub) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		_ = hub.Snapshot()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event hub state lock was held across external work")
	}
}

type blockingStore struct {
	eventhub.Store
	once    sync.Once
	entered chan struct{}
	release chan struct{}
}

func (s *blockingStore) Append(
	ctx context.Context,
	event protocol.Event,
) error {
	s.once.Do(func() {
		close(s.entered)
		<-s.release
	})
	return s.Store.Append(ctx, event)
}
