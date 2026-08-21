package mcp

import (
	"context"
	"errors"
	"sync"
	"testing"
)

// FuzzMCPConnectionCloseIsIdempotent verifies that calling Close() on a
// Connection multiple times (possibly with different contexts) is safe and
// does not panic, leak, or double-close the transport. This catches the
// bug where old connection close failure causes the transport to be leaked.
func FuzzMCPConnectionCloseIsIdempotent(f *testing.F) {
	f.Add(uint8(1), uint8(0))
	f.Add(uint8(5), uint8(1))
	f.Add(uint8(10), uint8(2))

	f.Fuzz(func(t *testing.T, closeCount uint8, failMode uint8) {
		closeCount = uint8(int(closeCount)%10 + 1)
		failMode = failMode % 3

		var closeCalls int
		var mu sync.Mutex

		transport := &fuzzTransport{
			closeFn: func() error {
				mu.Lock()
				closeCalls++
				count := closeCalls
				mu.Unlock()
				switch failMode {
				case 0:
					return nil
				case 1:
					return errors.New("simulated close failure")
				default:
					// First close fails, subsequent succeed.
					if count == 1 {
						return errors.New("simulated close failure")
					}
					return nil
				}
			},
		}

		conn, err := NewConnection("fuzz-server", transport, 0)
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		for i := uint8(0); i < closeCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = conn.Close(t.Context())
			}()
		}
		wg.Wait()

		mu.Lock()
		count := closeCalls
		mu.Unlock()

		// Transport Close should be called exactly once (sync.Once protection).
		if count != 1 {
			t.Errorf("transport.Close called %d times, expected exactly 1", count)
		}
	})
}

// fuzzTransport implements Transport for fuzz testing.
type fuzzTransport struct {
	closeFn func() error
}

func (t *fuzzTransport) Request(ctx context.Context, method string, params any, result any) error {
	return errors.New("not implemented")
}
func (t *fuzzTransport) Notify(ctx context.Context, method string, params any) error {
	return nil
}
func (t *fuzzTransport) Close(ctx context.Context) error {
	if t.closeFn != nil {
		return t.closeFn()
	}
	return nil
}
func (t *fuzzTransport) StderrTail() string { return "" }