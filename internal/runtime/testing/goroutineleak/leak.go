// Package goroutineleak provides a test helper for detecting goroutine leaks
// in tests. Use it to verify that goroutines spawned during a test are properly
// cleaned up before the test exits.
package goroutineleak

import (
	"runtime"
	"testing"
	"time"
)

// Checker records the baseline goroutine count and asserts that the growth
// after a test does not exceed the allowed threshold.
type Checker struct {
	initial int
	allowed int
}

// NewChecker creates a goroutine leak checker that records the current
// goroutine count as a baseline. The allowedGrowth parameter specifies
// how many additional goroutines are acceptable after the test completes.
func NewChecker(allowedGrowth int) *Checker {
	return &Checker{
		initial: runtime.NumGoroutine(),
		allowed: allowedGrowth,
	}
}

// Check verifies that the goroutine count has not grown beyond the allowed
// threshold. It waits a brief period for goroutines to settle before checking.
// Call this in a defer or at the end of a test.
func (c *Checker) Check(t *testing.T) {
	t.Helper()
	// Wait for goroutines to settle.
	time.Sleep(100 * time.Millisecond)
	final := runtime.NumGoroutine()
	growth := final - c.initial
	if growth > c.allowed {
		t.Errorf("goroutine leak detected: %d -> %d (growth=%d, allowed=%d)",
			c.initial, final, growth, c.allowed)
	}
}

// NumGoroutines returns the current number of goroutines. This is a
// convenience wrapper around runtime.NumGoroutine for use in tests.
func NumGoroutines() int {
	return runtime.NumGoroutine()
}