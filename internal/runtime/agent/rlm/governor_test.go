package rlm_test

import (
	"sync"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
)

func TestGovernorBlocksNestedTokenBudget(t *testing.T) {
	gov := rlm.NewGovernor(rlm.Limits{MaxTokens: 10, MaxDepth: 3, MaxConcurrency: 8})
	bridge := rlm.NewBridge(gov)
	if _, err := bridge.Run(0, "root", func(depth int, prompt string) (string, uint64, float64, error) {
		if _, err := bridge.Run(depth+1, "child", func(int, string) (string, uint64, float64, error) {
			return "nested", 10, 0, nil
		}); err != nil {
			return "", 0, 0, err
		}
		return "root", 1, 0, nil
	}); err == nil {
		t.Fatal("expected token budget rejection")
	}
	if _, err := gov.Admit(0, 1, 0); err == nil {
		t.Fatal("expected exhausted budget to block further admission")
	}
}

func TestGovernorDepthAndConcurrency(t *testing.T) {
	gov := rlm.NewGovernor(rlm.Limits{MaxTokens: 1000, MaxDepth: 1, MaxConcurrency: 2})
	if _, err := gov.Admit(2, 1, 0); err != rlm.ErrDepthBudget {
		t.Fatalf("depth err = %v", err)
	}
	l1, err := gov.Admit(0, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := gov.Admit(1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gov.Admit(1, 1, 0); err != rlm.ErrConcurrency {
		t.Fatalf("concurrency err = %v", err)
	}
	gov.Release(l1)
	gov.Release(l2)
}

func TestGovernorRaceFanOut(t *testing.T) {
	gov := rlm.NewGovernor(rlm.Limits{MaxTokens: 50, MaxDepth: 4, MaxConcurrency: 16})
	var wg sync.WaitGroup
	errCh := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lease, err := gov.Admit(1, 2, 0)
			if err != nil {
				errCh <- err
				return
			}
			gov.Release(lease)
		}()
	}
	wg.Wait()
	close(errCh)
	blocked := 0
	for err := range errCh {
		if err == rlm.ErrTokenBudget || err == rlm.ErrConcurrency {
			blocked++
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if blocked == 0 {
		t.Fatal("expected some admissions to fail under race")
	}
	snap := gov.Snapshot()
	if snap.SpentTokens > 50 {
		t.Fatalf("spent tokens %d exceeded budget", snap.SpentTokens)
	}
}
