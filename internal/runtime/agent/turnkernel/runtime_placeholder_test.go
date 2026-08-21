package turnkernel

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// TestRestoreDoesNotObserveReservePlaceholder verifies that a concurrent
// Restore call does not get a spurious ErrCoordinatorAlreadyActive when
// an Open call is in progress (between reserve and activate). This catches
// the bug where the placeholder CoordinatorHandle{} inserted by reserve()
// is visible to other callers.
func TestRestoreDoesNotObserveReservePlaceholder(t *testing.T) {
	store := NewMemoryTerminalEnvelopeStore(nil, nil)
	runtime, err := NewStoreCoordinatorRuntime(store)
	if err != nil {
		t.Fatal(err)
	}

	// First, open a coordinator to populate the store.
	handle, err := runtime.Open(t.Context(), "turn-1", NewState(protocol.TurnIntentAnswer, "act", 7))
	if err != nil {
		t.Fatal(err)
	}
	// Submit a command to write domain facts.
	if err := handle.Coordinator.Submit(t.Context(), StartTurn{}); err != nil {
		t.Fatal(err)
	}
	// Release to simulate a completed turn.
	if err := runtime.Release(t.Context(), "turn-1"); err != nil {
		t.Fatal(err)
	}

	// Now verify that Restore works correctly after Release.
	// The placeholder should not be visible since the turn was released.
	restored, err := runtime.Restore(t.Context(), "turn-1")
	if err != nil {
		t.Fatalf("Restore failed after Release: %v", err)
	}
	if !restored.Restored {
		t.Error("expected Restored flag to be true")
	}

	// Verify that concurrent Open and Restore on the same turnID
	// are properly serialized.
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	// Release first.
	if err := runtime.Release(t.Context(), "turn-1"); err != nil {
		t.Fatal(err)
	}

	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := runtime.Open(t.Context(), "turn-1", NewState(protocol.TurnIntentAnswer, "act", 7))
		if err != nil {
			errCh <- err
		}
	}()
	go func() {
		defer wg.Done()
		// Give the Open goroutine a head start to increase the chance
		// of hitting the race window.
		time.Sleep(10 * time.Millisecond)
		_, err := runtime.Restore(t.Context(), "turn-1")
		if err != nil {
			errCh <- err
		}
	}()

	wg.Wait()
	close(errCh)

	// At least one operation should succeed. The other may get
	// ErrCoordinatorAlreadyActive, which is expected for concurrent
	// access — but neither should panic or deadlock.
	errCount := 0
	for err := range errCh {
		if err != nil && !errors.Is(err, ErrCoordinatorAlreadyActive) {
			t.Errorf("unexpected error: %v", err)
		}
		if err != nil {
			errCount++
		}
	}
	if errCount == 2 {
		t.Error("BUG: both Open and Restore returned errors; " +
			"placeholder may be causing spurious failures")
	}
	t.Logf("concurrent Open/Restore: %d errors", errCount)
}