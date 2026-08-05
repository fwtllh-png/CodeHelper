package parallel_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/parallel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
)

func TestParallelMapHonorsGovernor(t *testing.T) {
	gov := rlm.NewGovernor(rlm.Limits{MaxTokens: 100, MaxDepth: 3, MaxConcurrency: 8})
	runner := parallel.New(parallel.Options{Governor: gov, Limit: 2})
	var maxInFlight atomic.Int32
	var current atomic.Int32
	out, err := runner.Map(context.Background(), 0, []string{"a", "b", "c", "d"}, func(_ context.Context, item string) (string, error) {
		n := current.Add(1)
		defer current.Add(-1)
		for {
			old := maxInFlight.Load()
			if n <= old || maxInFlight.CompareAndSwap(old, n) {
				break
			}
		}
		return item + "!", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 || out[0] != "a!" {
		t.Fatalf("out=%v", out)
	}
	if maxInFlight.Load() > 2 {
		t.Fatalf("max in flight = %d", maxInFlight.Load())
	}
}

func TestParallelRejectsDeepFanOut(t *testing.T) {
	gov := rlm.NewGovernor(rlm.Limits{MaxTokens: 100, MaxDepth: 1, MaxConcurrency: 8})
	runner := parallel.New(parallel.Options{Governor: gov, Limit: 2})
	_, err := runner.Map(context.Background(), 1, []string{"x"}, func(context.Context, string) (string, error) {
		return "y", nil
	})
	if err == nil {
		t.Fatal("expected depth rejection")
	}
}
