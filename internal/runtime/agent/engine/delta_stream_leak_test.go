package engine

import (
	"errors"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

// errorThenBlockStream returns an error on the first Recv call, then blocks
// forever on subsequent calls (simulating a stuck source stream).
type errorThenBlockStream struct {
	called bool
	block  chan struct{}
}

func (s *errorThenBlockStream) Recv() (provider.StreamEvent, error) {
	if !s.called {
		s.called = true
		return provider.StreamEvent{}, errors.New("source stream error")
	}
	<-s.block
	return provider.StreamEvent{}, io.EOF
}

func (s *errorThenBlockStream) Close() error {
	close(s.block)
	return nil
}

// TestDeltaStreamReadExitsOnSourceError verifies that the read() goroutine
// exits cleanly when the source stream returns an error, and that subsequent
// Recv() calls do not deadlock. This catches the bug where the read()
// goroutine exits but s.reading is left true, causing request() to skip
// sending on s.requests and the consumer to block forever on s.results.
func TestDeltaStreamReadExitsOnSourceError(t *testing.T) {
	goroutinesBefore := runtime.NumGoroutine()

	source := &errorThenBlockStream{block: make(chan struct{})}
	stream := newDeltaCoalescingStream(source)
	defer stream.Close()

	// First Recv should get the error from the source.
	_, err := stream.Recv()
	if err == nil {
		t.Fatal("expected an error from Recv, got nil")
	}

	// Wait for the read() goroutine to exit.
	time.Sleep(100 * time.Millisecond)

	// Subsequent Recv calls should not deadlock. They should return
	// io.EOF or another error after the stream is closed.
	done := make(chan struct{})
	go func() {
		_, _ = stream.Recv()
		close(done)
	}()

	select {
	case <-done:
		// Success: Recv returned without deadlocking.
	case <-time.After(2 * time.Second):
		t.Error("BUG: Recv() deadlocked after source stream error; read() goroutine likely leaked")
	}

	// Close the stream and verify goroutines are cleaned up.
	_ = stream.Close()
	time.Sleep(100 * time.Millisecond)

	goroutinesAfter := runtime.NumGoroutine()
	if goroutinesAfter > goroutinesBefore+5 {
		t.Errorf("goroutine leak detected: before=%d, after=%d", goroutinesBefore, goroutinesAfter)
	}
}
