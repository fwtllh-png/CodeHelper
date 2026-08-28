//go:build !windows

package mcp

import (
	"context"
	"testing"
	"time"
)

func TestStdioForcedKillCleansProcessGroup(t *testing.T) {
	transport, err := NewStdioTransport(context.Background(), ServerConfig{
		Command: "/bin/sh",
		Args:    []string{"-c", `trap '' TERM; while :; do :; done`},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := transport.Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-transport.processDone:
	default:
		t.Fatal("hung MCP process was not reaped")
	}
}
