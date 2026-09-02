package httpclient

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
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

type closeUnblocksStream struct {
	closed chan struct{}
	once   sync.Once
}

func (s *closeUnblocksStream) Recv() (provider.StreamEvent, error) {
	<-s.closed
	return provider.StreamEvent{}, io.EOF
}

func (s *closeUnblocksStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestIdleTimeoutClosesStreamBeforeReturning(t *testing.T) {
	source := &closeUnblocksStream{closed: make(chan struct{})}
	var released atomic.Int32
	stream := &managedStream{
		stream: source, cancel: func() {},
		release:     func() { released.Add(1) },
		failure:     func(error) {},
		success:     func() {},
		idleTimeout: 10 * time.Millisecond,
	}
	result := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		result <- err
	}()

	select {
	case err := <-result:
		if !protocol.IsCode(err, protocol.CodeDeadlineExceeded) {
			t.Fatalf("Recv() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Recv() remained blocked after idle timeout")
	}
	if released.Load() != 1 {
		t.Fatalf("release count = %d", released.Load())
	}
}
