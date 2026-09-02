package eventhub

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestSlowConsumerStopsSubscriptionOwner(t *testing.T) {
	store := &lifecycleStore{}
	hub := New(Config{
		Store: store, Buffer: 1, Context: context.Background(),
	})
	events, err := hub.Events(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	subscriber := hub.subscribers[1]
	hub.mu.Unlock()
	if subscriber == nil {
		t.Fatal("subscription owner was not registered")
	}
	meta := protocol.EventMeta{
		OperationID: "op", ThreadID: "thread",
		TurnID: "turn", ItemID: "item",
	}
	err = hub.Publish(
		meta,
		&protocol.OutputDeltaData{Text: "one"},
		func(protocol.Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	err = hub.Publish(
		meta,
		&protocol.TurnCompletedData{Text: "done"},
		func(protocol.Event) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-subscriber.done:
	case <-time.After(time.Second):
		t.Fatal("dropped subscription owner did not stop")
	}
	if snapshot := hub.Snapshot(); snapshot.Subscribers != 0 {
		t.Fatalf("subscribers = %d", snapshot.Subscribers)
	}
	if event := <-events; event.Sequence != 1 {
		t.Fatalf("buffered event sequence = %d", event.Sequence)
	}
	if _, open := <-events; open {
		t.Fatal("dropped subscription event channel remained open")
	}
	replay, _, err := hub.Replay(t.Context(), 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(replay) != 1 ||
		replay[0].Kind != protocol.EventTurnCompleted {
		t.Fatalf("durable terminal replay = %+v", replay)
	}
}

func TestSubscriptionCancellationStormReleasesOwners(t *testing.T) {
	hub := New(Config{
		Store: &lifecycleStore{}, Buffer: 1,
		Context: context.Background(),
	})
	const subscriptions = 128
	owners := make([]*subscription, 0, subscriptions)
	for range subscriptions {
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := hub.Events(ctx, 0, 0); err != nil {
			t.Fatal(err)
		}
		hub.mu.Lock()
		owners = append(owners, hub.subscribers[hub.next])
		hub.mu.Unlock()
		cancel()
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.Snapshot().Subscribers == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if snapshot := hub.Snapshot(); snapshot.Subscribers != 0 {
		t.Fatalf("subscribers after cancellation storm = %d", snapshot.Subscribers)
	}
	for index, owner := range owners {
		select {
		case <-owner.done:
		default:
			t.Fatalf("subscription owner %d remained active", index)
		}
	}
}

type lifecycleStore struct {
	mu     sync.Mutex
	events []protocol.Event
}

func (s *lifecycleStore) Append(
	_ context.Context,
	event protocol.Event,
) error {
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}

func (s *lifecycleStore) Replay(
	_ context.Context,
	cursor protocol.Cursor,
) ([]protocol.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var events []protocol.Event
	for _, event := range s.events {
		if event.Sequence > cursor {
			events = append(events, event)
		}
	}
	return events, nil
}

func (s *lifecycleStore) LastSequence(
	_ context.Context,
) (protocol.Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return 0, nil
	}
	return s.events[len(s.events)-1].Sequence, nil
}

func (*lifecycleStore) Close(context.Context) error {
	return nil
}
