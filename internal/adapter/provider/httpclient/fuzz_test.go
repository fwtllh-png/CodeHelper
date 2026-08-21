package httpclient

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// fuzzStream is a mock provider.Stream for fuzz testing managedStream.
type fuzzStream struct {
	mu       sync.Mutex
	events   []provider.StreamEvent
	position int
	closed   bool
}

func (s *fuzzStream) Recv() (provider.StreamEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return provider.StreamEvent{}, io.EOF
	}
	if s.position >= len(s.events) {
		return provider.StreamEvent{}, io.EOF
	}
	event := s.events[s.position]
	s.position++
	return event, nil
}

func (s *fuzzStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// FuzzManagedStreamRecvCloseInterleaving verifies that interleaving Recv()
// and Close() calls on a managedStream does not panic, double-close, or
// corrupt internal state. This catches the double-release bug where the
// idle timeout path and the observe method both call Close().
func FuzzManagedStreamRecvCloseInterleaving(f *testing.F) {
	// Seed corpus: different interleaving patterns.
	f.Add(uint8(0), uint8(0))
	f.Add(uint8(1), uint8(0))
	f.Add(uint8(0), uint8(1))
	f.Add(uint8(5), uint8(5))

	f.Fuzz(func(t *testing.T, recvCalls, closeCalls uint8) {
		recvCalls = uint8(int(recvCalls)%10 + 1)
		closeCalls = uint8(int(closeCalls)%5 + 1)

		var releaseCount int
		var mu sync.Mutex

		release := func() {
			mu.Lock()
			releaseCount++
			mu.Unlock()
		}

		events := make([]provider.StreamEvent, 100)
		for i := range events {
			events[i] = provider.StreamEvent{
				Type: provider.EventTextDelta,
				Text: "x",
			}
		}

		source := &fuzzStream{events: events}
		stream := &managedStream{
			stream:    source,
			cancel:    func() {},
			release:   release,
			closeOnce: sync.Once{},
		}

		var wg sync.WaitGroup

		// Start Recv goroutines.
		for i := uint8(0); i < recvCalls; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, _ = stream.Recv()
			}()
		}

		// Start Close goroutines after a brief delay.
		time.Sleep(5 * time.Millisecond)
		for i := uint8(0); i < closeCalls; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = stream.Close()
			}()
		}

		wg.Wait()

		mu.Lock()
		count := releaseCount
		mu.Unlock()

		// Release must be called at most once (sync.Once protection).
		if count > 1 {
			t.Errorf("release called %d times, expected at most 1", count)
		}
	})
}