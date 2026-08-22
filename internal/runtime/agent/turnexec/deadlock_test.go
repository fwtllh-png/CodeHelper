package turnexec

import (
	"context"
	"sync"
	"testing"
	"time"

	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
)

// TestConcurrentCloseAndRunDoesNotDeadlock verifies that concurrent Close
// and Run operations on a Scope do not deadlock or panic. This catches the
// bug where triple-layered locking (Engine, Scope, Kernel) could cause
// deadlocks under concurrent access.
func TestConcurrentCloseAndRunDoesNotDeadlock(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var runCount int
	var mu sync.Mutex

	scope, err := NewScope(
		"test-spec",
		func(ctx context.Context) (string, error) {
			mu.Lock()
			runCount++
			mu.Unlock()
			<-ctx.Done()
			return "done", ctx.Err()
		},
		&noopControl{},
		func() string { return "snapshot" },
		func(ctx context.Context) error {
			cancel()
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// Run multiple goroutines that call Run and Close concurrently.
	var wg sync.WaitGroup
	const numGoroutines = 20

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = scope.Run(ctx)
		}()
	}

	// Give the Run goroutines time to start.
	time.Sleep(50 * time.Millisecond)

	// Close concurrently from multiple goroutines.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = scope.Close(ctx)
		}()
	}

	// Wait with a timeout to detect deadlocks.
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success: all goroutines completed without deadlock.
	case <-time.After(5 * time.Second):
		t.Error("BUG: concurrent Close/Run deadlocked (timeout after 5s)")
	}

	t.Logf("runCount=%d", runCount)
}

// TestConcurrentMailboxOfferAndDrainDoesNotDeadlock verifies that concurrent
// Offer and Drain operations on a Mailbox do not deadlock.
func TestConcurrentMailboxOfferAndDrainDoesNotDeadlock(t *testing.T) {
	mailbox := NewMailbox[int](DefaultMailboxCapacity)

	var wg sync.WaitGroup
	const numProducers = 10
	const numConsumers = 5
	const numItems = 1000

	// Producers.
	for i := 0; i < numProducers; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for j := 0; j < numItems; j++ {
				_ = mailbox.Offer(start*numItems + j)
			}
		}(i)
	}

	// Consumers.
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numItems/numConsumers; j++ {
				_ = mailbox.Drain()
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
	case <-time.After(5 * time.Second):
		t.Error("BUG: concurrent Mailbox Offer/Drain deadlocked")
	}
}

type noopControl struct{}

func (n *noopControl) Cancel(string) error                              { return nil }
func (n *noopControl) Steer(string) error                               { return nil }
func (n *noopControl) ResolveApproval(toolguard.ApprovalDecision) error { return nil }
func (n *noopControl) ResolveInput(interact.Reply) error                { return nil }
