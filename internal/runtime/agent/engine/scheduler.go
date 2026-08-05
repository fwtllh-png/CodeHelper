package engine

import (
	"context"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

const defaultMaxToolConcurrent = 8

// ToolScheduler admits tool calls through an RW gate with a bound on concurrent
// shared slots. Serial tools take an exclusive write lock;
// concurrent tools take a shared read lock and one semaphore slot.
type ToolScheduler struct {
	gate  sync.RWMutex
	slots chan struct{}
}

func NewToolScheduler(maxConcurrent int) *ToolScheduler {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxToolConcurrent
	}
	s := &ToolScheduler{slots: make(chan struct{}, maxConcurrent)}
	for i := 0; i < maxConcurrent; i++ {
		s.slots <- struct{}{}
	}
	return s
}

// Admit blocks until the call may run under policy, or ctx is canceled.
// The returned release must be called exactly once after the tool finishes.
func (s *ToolScheduler) Admit(ctx context.Context, policy tool.ParallelPolicy) (release func(), err error) {
	if s == nil {
		return func() {}, nil
	}
	if policy == tool.ParallelSerial {
		return s.admitSerial(ctx)
	}
	return s.admitConcurrent(ctx)
}

func (s *ToolScheduler) admitSerial(ctx context.Context) (func(), error) {
	acquired := make(chan struct{})
	go func() {
		s.gate.Lock()
		close(acquired)
	}()
	select {
	case <-acquired:
		return func() { s.gate.Unlock() }, nil
	case <-ctx.Done():
		go func() {
			<-acquired
			s.gate.Unlock()
		}()
		return nil, ctx.Err()
	}
}

func (s *ToolScheduler) admitConcurrent(ctx context.Context) (func(), error) {
	// Take a slot first so a flood of concurrent waiters cannot starve serial
	// writers forever while holding nothing but goroutines.
	select {
	case <-s.slots:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	acquired := make(chan struct{})
	go func() {
		s.gate.RLock()
		close(acquired)
	}()
	select {
	case <-acquired:
		return func() {
			s.gate.RUnlock()
			s.slots <- struct{}{}
		}, nil
	case <-ctx.Done():
		go func() {
			<-acquired
			s.gate.RUnlock()
			s.slots <- struct{}{}
		}()
		return nil, ctx.Err()
	}
}
