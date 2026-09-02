package tool_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
)

func TestFaultInjectionToolCancelReleasesClaim(t *testing.T) {
	claims := tool.NewClaims()
	target := filepath.Join(t.TempDir(), "target.txt")
	release, err := claims.AcquireResources(context.Background(), []tool.Resource{{
		Kind: "file", Path: target, Access: tool.AccessWrite,
	}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	var acquireErr error
	go func() {
		defer wg.Done()
		_, acquireErr = claims.AcquireResources(ctx, []tool.Resource{{
			Kind: "file", Path: target, Access: tool.AccessWrite,
		}})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	wg.Wait()
	if !errors.Is(acquireErr, context.Canceled) {
		t.Fatalf("blocked acquire error = %v, want context.Canceled", acquireErr)
	}
	release()

	second, err := claims.AcquireResources(context.Background(), []tool.Resource{{
		Kind: "file", Path: target, Access: tool.AccessWrite,
	}})
	if err != nil {
		t.Fatalf("claim after cancel+release: %v", err)
	}
	second()
}
