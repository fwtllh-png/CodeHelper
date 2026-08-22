package guard

import (
	"testing"
	"time"
)

// FuzzGuardCompletedIsBounded verifies that the Guard's completed map does
// not grow unbounded under random sequences of pending/finish operations.
// This catches regressions in the pruneCompletedLocked logic.
func FuzzGuardCompletedIsBounded(f *testing.F) {
	f.Add(uint8(10), uint8(0))
	f.Add(uint8(50), uint8(1))
	f.Add(uint8(100), uint8(2))

	f.Fuzz(func(t *testing.T, iterations uint8, advanceMinutes uint8) {
		// Limit iterations to avoid excessive test time.
		iterations = uint8(int(iterations)%200 + 1)
		advanceMinutes = uint8(int(advanceMinutes)%30 + 1)

		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		guard := &Guard{
			pending:     make(map[string]*pending),
			completed:   make(map[string]time.Time),
			now:         func() time.Time { return now },
			approvalTTL: 5 * time.Minute,
		}

		for i := uint8(0); i < iterations; i++ {
			requestID := randomID("approval_")
			guard.pending[requestID] = &pending{
				callID:   "call_" + requestID,
				decision: make(chan ApprovalDecision, 1),
				resume:   make(chan struct{}),
			}
			guard.finishPending(requestID)
		}

		// Advance time past the TTL.
		now = now.Add(time.Duration(advanceMinutes) * time.Minute)

		// One more cycle to trigger pruning.
		requestID := randomID("approval_")
		guard.pending[requestID] = &pending{
			callID:   "call_" + requestID,
			decision: make(chan ApprovalDecision, 1),
			resume:   make(chan struct{}),
		}
		guard.finishPending(requestID)

		guard.mu.Lock()
		size := len(guard.completed)
		guard.mu.Unlock()

		// The completed map should be bounded after TTL pruning.
		// After TTL expiry, old entries should be pruned, leaving only
		// the newly added entry (size 1) or a few recent entries.
		if advanceMinutes > 5 && size > 100 {
			t.Errorf("completed map has %d entries after %d iterations and %d min advance; "+
				"pruning may not be working correctly",
				size, iterations, advanceMinutes)
		}

		// The map should never grow larger than the number of iterations + 1.
		if size > int(iterations)+1 {
			t.Errorf("completed map size %d exceeds expected max %d",
				size, iterations+1)
		}
	})
}
