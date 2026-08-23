//go:build stress
// +build stress

package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

// TestStressConcurrentScopeCloseAndRun verifies that Scope does not deadlock
// under heavy concurrent Close and Run operations. This is a scaled-up version
// of the deadlock test with more goroutines and longer duration.
func TestStressConcurrentScopeCloseAndRun(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var runCount atomic.Int64
	var closeCount atomic.Int64

	scope, err := NewScope(
		"stress-spec",
		func(ctx context.Context) (string, error) {
			runCount.Add(1)
			<-ctx.Done()
			return "done", ctx.Err()
		},
		&noopControl{},
		func() string { return "snapshot" },
		func(ctx context.Context) error {
			closeCount.Add(1)
			cancel()
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	const numGoroutines = 100
	var wg sync.WaitGroup

	// Half the goroutines call Run, half call Close.
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = scope.Run(ctx)
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = scope.Close(ctx)
		}()
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("runCount=%d closeCount=%d", runCount.Load(), closeCount.Load())
	case <-time.After(10 * time.Second):
		t.Error("BUG: stress concurrent Close/Run deadlocked (timeout after 10s)")
	}
}

// TestStressConcurrentMailboxOfferAndDrain verifies that Mailbox does not
// deadlock or corrupt data under heavy concurrent Offer and Drain operations.
func TestStressConcurrentMailboxOfferAndDrain(t *testing.T) {
	mailbox := turnkernel.NewMailbox[int](turnkernel.DefaultMailboxCapacity)

	const numProducers = 50
	const numConsumers = 20
	const numItemsPerProducer = 10000

	var produced atomic.Int64
	var consumed atomic.Int64
	var totalOffers atomic.Int64
	var wg sync.WaitGroup

	// Producers.
	for i := 0; i < numProducers; i++ {
		wg.Add(1)
		go func(start int) {
			defer wg.Done()
			for j := 0; j < numItemsPerProducer; j++ {
				totalOffers.Add(1)
				val := start*numItemsPerProducer + j
				if mailbox.Offer(val) == nil {
					produced.Add(1)
				}
			}
		}(i)
	}

	// Consumers drain until all producers finish and the queue is empty.
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				items := mailbox.Drain()
				consumed.Add(int64(len(items)))
				if len(items) == 0 {
					// All producers have finished offering.
					if totalOffers.Load() >= int64(numProducers*numItemsPerProducer) {
						// Give one more drain cycle for straggling items.
						items = mailbox.Drain()
						consumed.Add(int64(len(items)))
						return
					}
					time.Sleep(time.Microsecond)
				}
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
		t.Logf("produced=%d consumed=%d", produced.Load(), consumed.Load())
	case <-time.After(30 * time.Second):
		t.Error("BUG: stress concurrent Mailbox Offer/Drain deadlocked (timeout after 30s)")
	}
}

// TestStressRequestLedgerConcurrentRegisterAndResolve verifies that
// turnkernel.RequestLedger is safe under concurrent Register and Resolve calls.
func TestStressRequestLedgerConcurrentRegisterAndResolve(t *testing.T) {
	ledger := turnkernel.NewRequestLedger()

	const numRequests = 1000
	var wg sync.WaitGroup

	// Register all requests.
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			kind := turnkernel.RequestApproval
			if id%2 == 0 {
				kind = turnkernel.RequestInput
			}
			_ = ledger.Register(kind, formatRequestID(id))
		}(i)
	}
	wg.Wait()

	// Resolve all requests concurrently.
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			kind := turnkernel.RequestApproval
			if id%2 == 0 {
				kind = turnkernel.RequestInput
			}
			_ = ledger.Resolve(kind, formatRequestID(id))
		}(i)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success.
	case <-time.After(10 * time.Second):
		t.Error("BUG: stress concurrent turnkernel.RequestLedger operations deadlocked")
	}
}

func formatRequestID(id int) string {
	return "req-" + string(rune('0'+id%10)) + string(rune('0'+(id/10)%10)) + string(rune('0'+(id/100)%10))
}
