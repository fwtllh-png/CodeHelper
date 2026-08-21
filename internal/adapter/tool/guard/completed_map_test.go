package guard

import (
	"testing"
	"time"
)

// TestCompletedMapIsBounded verifies that the Guard's completed map does not
// grow unbounded over many approval cycles. After the fix, entries older than
// the approval TTL are pruned.
func TestCompletedMapIsBounded(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	guard := &Guard{
		pending:     make(map[string]*pending),
		completed:   make(map[string]time.Time),
		now:         func() time.Time { return now },
		approvalTTL: 5 * time.Minute,
	}

	// Simulate many approval cycles within the TTL window.
	const iterations = 100
	for i := 0; i < iterations; i++ {
		requestID := randomID("approval_")
		guard.pending[requestID] = &pending{
			callID:   "call_" + requestID,
			decision: make(chan ApprovalDecision, 1),
			resume:   make(chan struct{}),
		}
		guard.finishPending(requestID)
	}

	guard.mu.Lock()
	completedSize := len(guard.completed)
	guard.mu.Unlock()

	// All entries should be present since they are within the TTL.
	if completedSize < iterations {
		t.Errorf("expected at least %d completed entries, got %d", iterations, completedSize)
	}
	t.Logf("completed map size after %d iterations (within TTL): %d", iterations, completedSize)

	// Advance time past the TTL.
	now = now.Add(10 * time.Minute)

	// One more cycle should trigger pruning.
	requestID := randomID("approval_")
	guard.pending[requestID] = &pending{
		callID:   "call_" + requestID,
		decision: make(chan ApprovalDecision, 1),
		resume:   make(chan struct{}),
	}
	guard.finishPending(requestID)

	guard.mu.Lock()
	completedSize = len(guard.completed)
	guard.mu.Unlock()

	// After pruning, the map should be small (only the new entry, plus
	// any entries that were not pruned).
	if completedSize >= iterations {
		t.Errorf("BUG: completed map still has %d entries after TTL expiry; "+
			"pruning did not work", completedSize)
	}
	t.Logf("completed map size after TTL expiry: %d", completedSize)
}