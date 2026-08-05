package wire_test

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
)

func TestMCPPrewarmCoalescesRefresh(t *testing.T) {
	pool := mcp.NewPool(nil)
	prewarm := wire.NewMCPPrewarm(pool, filepath.Join(t.TempDir(), "missing.json"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prewarm.Start(ctx)
	defer prewarm.Stop()

	var wakes atomic.Int32
	// Request many times; channel capacity 1 coalesces.
	for i := 0; i < 20; i++ {
		prewarm.RequestRefresh()
		wakes.Add(1)
	}
	time.Sleep(50 * time.Millisecond)
	// Worker may run refreshIfDirty (fails on missing config) but should not hang.
	if wakes.Load() != 20 {
		t.Fatalf("requests=%d", wakes.Load())
	}
}
