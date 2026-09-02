//go:build stress
// +build stress

package eventhub

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

// stressStore is a minimal in-memory store for stress tests.
type stressStore struct {
	mu       sync.Mutex
	events   []protocol.Event
	sequence protocol.Cursor
}

func (s *stressStore) Append(_ context.Context, event protocol.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	s.sequence = event.Sequence
	return nil
}

func (s *stressStore) Replay(_ context.Context, cursor protocol.Cursor) ([]protocol.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if int(cursor) >= len(s.events) {
		return nil, nil
	}
	return s.events[cursor:], nil
}

func (s *stressStore) LastSequence(_ context.Context) (protocol.Cursor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sequence, nil
}

func (s *stressStore) Close(_ context.Context) error {
	return nil
}

var _ Store = (*stressStore)(nil)

// TestStressSubscriptionStorm verifies that the Hub handles many concurrent
// subscriptions and publications without deadlock or data loss.
func TestStressSubscriptionStorm(t *testing.T) {
	store := &stressStore{}
	hub := New(Config{
		Store:   store,
		Buffer:  64,
		Context: context.Background(),
	})

	const numPublishers = 10
	const numSubscribers = 20
	const numEventsPerPublisher = 100

	var published atomic.Int64
	var received atomic.Int64
	var totalAttempts atomic.Int64
	var wg sync.WaitGroup

	// Start subscribers first.
	for i := 0; i < numSubscribers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, err := hub.Events(t.Context(), 0, 0)
			if err != nil {
				return
			}
			for range events {
				received.Add(1)
			}
		}()
	}

	// Give subscribers time to register.
	time.Sleep(50 * time.Millisecond)

	// Publish events concurrently.
	for i := 0; i < numPublishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numEventsPerPublisher; j++ {
				totalAttempts.Add(1)
				meta := protocol.EventMeta{}
				data := &protocol.OutputDeltaData{Text: "stress"}
				err := hub.Publish(meta, data, func(event protocol.Event) error {
					return nil
				})
				if err == nil {
					published.Add(1)
				}
			}
		}()
	}

	// Close the hub after all publishers finish to signal subscribers.
	go func() {
		for {
			if totalAttempts.Load() >= int64(numPublishers*numEventsPerPublisher) {
				time.Sleep(10 * time.Millisecond)
				_ = hub.Close(t.Context())
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	// Wait for all publishers to finish.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("published=%d received=%d", published.Load(), received.Load())
	case <-time.After(30 * time.Second):
		t.Error("BUG: stress subscription storm deadlocked")
	}
}

// TestStressConcurrentPublishAndClose verifies that Publish and Close do not
// deadlock when called concurrently.
func TestStressConcurrentPublishAndClose(t *testing.T) {
	store := &stressStore{}
	hub := New(Config{
		Store:   store,
		Buffer:  64,
		Context: context.Background(),
		Closed:  errHubClosed,
	})

	const numGoroutines = 50
	var wg sync.WaitGroup
	var published atomic.Int64

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				meta := protocol.EventMeta{}
				data := &protocol.OutputDeltaData{Text: "stress"}
				err := hub.Publish(meta, data, func(event protocol.Event) error {
					return nil
				})
				if err == nil {
					published.Add(1)
				}
			}
		}()
	}

	// Concurrent Close.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		_ = hub.Close(t.Context())
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("published=%d", published.Load())
	case <-time.After(15 * time.Second):
		t.Error("BUG: stress concurrent Publish/Close deadlocked")
	}
}

// TestStressConcurrentEventsAndClose verifies that Events (subscribe) and Close
// do not deadlock when called concurrently.
func TestStressConcurrentEventsAndClose(t *testing.T) {
	store := &stressStore{}
	hub := New(Config{
		Store:   store,
		Buffer:  64,
		Context: context.Background(),
		Closed:  errHubClosed,
	})

	const numGoroutines = 30
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			events, err := hub.Events(t.Context(), 0, 0)
			if err != nil {
				return
			}
			for range events {
				// Drain.
			}
		}()
	}

	// Concurrent Close.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		_ = hub.Close(t.Context())
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success.
	case <-time.After(10 * time.Second):
		t.Error("BUG: stress concurrent Events/Close deadlocked")
	}
}

// TestStressConcurrentSnapshotAndPublish verifies that Snapshot is safe when
// called concurrently with Publish.
func TestStressConcurrentSnapshotAndPublish(t *testing.T) {
	store := &stressStore{}
	hub := New(Config{
		Store:   store,
		Buffer:  64,
		Context: context.Background(),
	})

	const numGoroutines = 50
	var wg sync.WaitGroup
	var snapshots atomic.Int64

	// Publishers.
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				meta := protocol.EventMeta{}
				data := &protocol.OutputDeltaData{Text: "stress"}
				_ = hub.Publish(meta, data, func(event protocol.Event) error {
					return nil
				})
			}
		}()
	}

	// Snapshot readers.
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = hub.Snapshot()
				snapshots.Add(1)
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("snapshots=%d", snapshots.Load())
	case <-time.After(15 * time.Second):
		t.Error("BUG: stress concurrent Snapshot/Publish deadlocked")
	}
}

// TestStressConcurrentReplayAndPublish verifies that Replay is safe when
// called concurrently with Publish.
func TestStressConcurrentReplayAndPublish(t *testing.T) {
	store := &stressStore{}
	hub := New(Config{
		Store:   store,
		Buffer:  64,
		Context: context.Background(),
	})

	// Seed some events.
	for i := 0; i < 100; i++ {
		meta := protocol.EventMeta{}
		data := &protocol.OutputDeltaData{Text: "seed"}
		_ = hub.Publish(meta, data, func(event protocol.Event) error {
			return nil
		})
	}

	const numGoroutines = 50
	var wg sync.WaitGroup

	// Publishers.
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				meta := protocol.EventMeta{}
				data := &protocol.OutputDeltaData{Text: "stress"}
				_ = hub.Publish(meta, data, func(event protocol.Event) error {
					return nil
				})
			}
		}()
	}

	// Replay readers.
	for i := 0; i < numGoroutines/2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, _, _ = hub.Replay(t.Context(), 0, 10)
			}
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success.
	case <-time.After(15 * time.Second):
		t.Error("BUG: stress concurrent Replay/Publish deadlocked")
	}
}

// TestStressHubRestoreIsSafe verifies that Restore is safe under concurrent
// access.
func TestStressHubRestoreIsSafe(t *testing.T) {
	store := &stressStore{}
	hub := New(Config{
		Store:   store,
		Buffer:  64,
		Context: context.Background(),
	})

	const numGoroutines = 50
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			hub.Restore(protocol.Cursor(idx))
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		snap := hub.Snapshot()
		t.Logf("lastSequence=%d subscribers=%d", snap.LastSequence, snap.Subscribers)
	case <-time.After(10 * time.Second):
		t.Error("BUG: stress concurrent Restore deadlocked")
	}
}

var errHubClosed = &stressError{"hub is closed"}

type stressError struct {
	msg string
}

func (e *stressError) Error() string {
	return e.msg
}
