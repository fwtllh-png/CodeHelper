package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

func TestToolSchedulerSerialExcludesConcurrent(t *testing.T) {
	sched := NewToolScheduler(4)
	var concurrentInside atomic.Int32
	var serialSawOverlap atomic.Bool

	var wg sync.WaitGroup
	started := make(chan struct{})
	serialReady := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		release, err := sched.Admit(context.Background(), tool.ParallelSerial)
		if err != nil {
			t.Errorf("serial admit: %v", err)
			return
		}
		close(serialReady)
		<-started
		if concurrentInside.Load() != 0 {
			serialSawOverlap.Store(true)
		}
		time.Sleep(30 * time.Millisecond)
		if concurrentInside.Load() != 0 {
			serialSawOverlap.Store(true)
		}
		release()
	}()

	<-serialReady
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := sched.Admit(context.Background(), tool.ParallelConcurrent)
			if err != nil {
				t.Errorf("concurrent admit: %v", err)
				return
			}
			concurrentInside.Add(1)
			time.Sleep(10 * time.Millisecond)
			concurrentInside.Add(-1)
			release()
		}()
	}
	close(started)
	wg.Wait()
	if serialSawOverlap.Load() {
		t.Fatal("concurrent tool overlapped with serial exclusive section")
	}
}

func TestToolSchedulerConcurrentBound(t *testing.T) {
	sched := NewToolScheduler(2)
	var inside atomic.Int32
	var maxInside atomic.Int32

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := sched.Admit(context.Background(), tool.ParallelConcurrent)
			if err != nil {
				t.Errorf("admit: %v", err)
				return
			}
			cur := inside.Add(1)
			for {
				prev := maxInside.Load()
				if cur <= prev || maxInside.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			inside.Add(-1)
			release()
		}()
	}
	wg.Wait()
	if maxInside.Load() > 2 {
		t.Fatalf("max concurrent inside = %d, want <= 2", maxInside.Load())
	}
}

func TestToolSchedulerCancelDuringAdmit(t *testing.T) {
	sched := NewToolScheduler(1)
	release, err := sched.Admit(context.Background(), tool.ParallelConcurrent)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := sched.Admit(ctx, tool.ParallelConcurrent)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancel error")
		}
	case <-time.After(time.Second):
		t.Fatal("admit did not unblock on cancel")
	}
	release()
}
