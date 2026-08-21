package mcp

import (
	"context"
	"errors"
	"testing"
	"time"
)

// failingCloseTransport is a transport that fails on Close.
type failingCloseTransport struct {
	closed   bool
	closeErr error
}

func (t *failingCloseTransport) Request(ctx context.Context, method string, params any, result any) error {
	return errors.New("not implemented")
}
func (t *failingCloseTransport) Notify(ctx context.Context, method string, params any) error {
	return nil
}
func (t *failingCloseTransport) Close(ctx context.Context) error {
	t.closed = true
	return t.closeErr
}
func (t *failingCloseTransport) StderrTail() string { return "" }

// TestPoolCloseFailureDoesNotLeakResources verifies that when a connection's
// Close fails, the transport is still closed and the connection is idempotent.
// This catches the bug where old connection close failure causes the transport
// to be leaked.
func TestPoolCloseFailureDoesNotLeakResources(t *testing.T) {
	transport := &failingCloseTransport{
		closeErr: errors.New("transport close failed"),
	}

	conn, err := NewConnection("test-server", transport, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}

	// Attempt to close the connection — it should return the transport error.
	err = conn.Close(t.Context())
	if err == nil {
		t.Error("expected Close to return an error from the failing transport")
	}

	// The transport should have been closed (even if it returned an error).
	if !transport.closed {
		t.Error("BUG: transport.Close was not called; connection was leaked")
	}

	// Second close attempt should be idempotent (via sync.Once).
	// The first error is preserved and returned on subsequent calls.
	err2 := conn.Close(t.Context())
	t.Logf("first close error: %v, second close error: %v", err, err2)

	// The transport should only be closed once.
	transport.closed = false // Reset to verify it's not called again.
	_ = conn.Close(t.Context())
	if transport.closed {
		t.Error("BUG: transport.Close was called more than once (double-close)")
	}
}