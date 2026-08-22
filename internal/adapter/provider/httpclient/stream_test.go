package httpclient

import (
	"io"
	"sync"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// mockStream implements provider.Stream for testing managedStream.Close safety.
type mockStream struct{}

func (m *mockStream) Recv() (provider.StreamEvent, error) {
	return provider.StreamEvent{}, io.EOF
}

func (m *mockStream) Close() error {
	return nil
}

// TestStreamCloseIsSafeMultipleTimes verifies that managedStream.Close() is
// safe to call multiple times and that the release function is called exactly
// once. This catches the double-release bug where the idle timeout path calls
// s.Close() (which calls s.release()) and the observe method also calls
// s.Close().
func TestStreamCloseIsSafeMultipleTimes(t *testing.T) {
	var releaseCount int
	var mu sync.Mutex

	release := func() {
		mu.Lock()
		releaseCount++
		mu.Unlock()
	}

	stream := &managedStream{
		stream:    &mockStream{},
		release:   release,
		cancel:    func() {},
		closeOnce: sync.Once{},
	}

	// Call Close multiple times concurrently.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
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

	if count != 1 {
		t.Errorf("BUG: release called %d times, expected exactly 1 (double-release detected)", count)
	}
}
