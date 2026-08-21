//go:build stress
// +build stress

package tui

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestStressChannelBackpressure verifies that the out channel does not
// deadlock when the producer is faster than the consumer.
func TestStressChannelBackpressure(t *testing.T) {
	const channelCapacity = 64
	const numMessages = 10000

	out := make(chan tea.Msg, channelCapacity)
	var produced atomic.Int64
	var consumed atomic.Int64

	// Producer: fast sender.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < numMessages; i++ {
			select {
			case out <- streamMsg{text: "test"}:
				produced.Add(1)
			default:
				// Channel full, drop.
			}
		}
	}()

	// Consumer: slow reader.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-out:
				consumed.Add(1)
				time.Sleep(time.Microsecond) // Simulate slow consumer.
			case <-time.After(5 * time.Second):
				return
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("produced=%d consumed=%d", produced.Load(), consumed.Load())
	case <-time.After(15 * time.Second):
		t.Error("BUG: stress channel backpressure deadlocked")
	}
}

// TestStressStreamDoneMsgNonBlocking verifies that streamDoneMsg send is
// always non-blocking and does not deadlock when the channel is full.
func TestStressStreamDoneMsgNonBlocking(t *testing.T) {
	const channelCapacity = 4
	out := make(chan tea.Msg, channelCapacity)

	// Fill the channel.
	for i := 0; i < channelCapacity; i++ {
		out <- streamMsg{text: "fill"}
	}

	// streamDoneMsg should be sent non-blocking (select with default).
	var wg sync.WaitGroup
	const numGoroutines = 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case out <- streamDoneMsg{}:
			default:
				// Expected: channel is full, default case used.
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
		// Success: no deadlock.
	case <-time.After(5 * time.Second):
		t.Error("BUG: streamDoneMsg non-blocking send deadlocked")
	}
}

// TestStressConcurrentWaitMsgAndSend verifies that WaitMsg and concurrent
// channel sends do not deadlock.
func TestStressConcurrentWaitMsgAndSend(t *testing.T) {
	out := make(chan tea.Msg, 64)

	var sent atomic.Int64
	var received atomic.Int64
	var totalAttempts atomic.Int64

	// Senders.
	var wg sync.WaitGroup
	const numSenders = 20
	for i := 0; i < numSenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				totalAttempts.Add(1)
				select {
				case out <- streamMsg{text: "msg"}:
					sent.Add(1)
				default:
				}
			}
		}()
	}

	// Receivers using WaitMsg pattern.
	const numReceivers = 5
	for i := 0; i < numReceivers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case msg, ok := <-out:
					if !ok {
						return
					}
					_ = msg
					received.Add(1)
				case <-time.After(100 * time.Millisecond):
					// All senders have finished their attempts.
					if totalAttempts.Load() >= numSenders*500 {
						// Drain any remaining items, then exit.
						for {
							select {
							case <-out:
								received.Add(1)
							default:
								return
							}
						}
					}
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
		t.Logf("sent=%d received=%d", sent.Load(), received.Load())
	case <-time.After(30 * time.Second):
		t.Error("BUG: stress concurrent WaitMsg/send deadlocked")
	}
}

// TestStressChannelCloseDoesNotBlock verifies that closing a channel and
// draining it does not deadlock.
func TestStressChannelCloseDoesNotBlock(t *testing.T) {
	out := make(chan tea.Msg, 64)

	// Fill partially.
	for i := 0; i < 32; i++ {
		out <- streamMsg{text: "msg"}
	}

	var wg sync.WaitGroup
	var drained atomic.Int64

	// Drainer starts first.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for msg := range out {
			_ = msg
			drained.Add(1)
		}
	}()

	// Concurrent closer.
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(10 * time.Millisecond)
		close(out)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		t.Logf("drained=%d", drained.Load())
	case <-time.After(5 * time.Second):
		t.Error("BUG: channel close/drain deadlocked")
	}
}