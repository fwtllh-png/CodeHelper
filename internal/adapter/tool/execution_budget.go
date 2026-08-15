package tool

import (
	"context"
	"sync"
)

type budgetWaiter struct {
	ready chan struct{}
}

// ExecutionBudget grants a bounded number of leases in FIFO order.
type ExecutionBudget struct {
	mu       sync.Mutex
	capacity int
	active   int
	queue    []*budgetWaiter
}

func NewExecutionBudget(capacity int) *ExecutionBudget {
	if capacity < 1 {
		capacity = 1
	}
	return &ExecutionBudget{capacity: capacity}
}

func (b *ExecutionBudget) Acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	waiter := &budgetWaiter{ready: make(chan struct{})}
	b.mu.Lock()
	if b.active < b.capacity && len(b.queue) == 0 {
		b.active++
		b.mu.Unlock()
		return b.releaseFunc(), nil
	}
	b.queue = append(b.queue, waiter)
	b.mu.Unlock()
	select {
	case <-waiter.ready:
		return b.releaseFunc(), nil
	case <-ctx.Done():
		b.mu.Lock()
		if removeBudgetWaiter(&b.queue, waiter) {
			b.dispatchLocked()
			b.mu.Unlock()
			return nil, ctx.Err()
		}
		b.mu.Unlock()
		b.release()
		return nil, ctx.Err()
	}
}

func (b *ExecutionBudget) Active() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active
}

func (b *ExecutionBudget) Waiting() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.queue)
}

func (b *ExecutionBudget) releaseFunc() func() {
	var once sync.Once
	return func() { once.Do(b.release) }
}

func (b *ExecutionBudget) release() {
	b.mu.Lock()
	if b.active > 0 {
		b.active--
	}
	b.dispatchLocked()
	b.mu.Unlock()
}

func (b *ExecutionBudget) dispatchLocked() {
	for b.active < b.capacity && len(b.queue) != 0 {
		waiter := b.queue[0]
		b.queue = b.queue[1:]
		b.active++
		close(waiter.ready)
	}
}

func removeBudgetWaiter(queue *[]*budgetWaiter, target *budgetWaiter) bool {
	for index, waiter := range *queue {
		if waiter == target {
			copy((*queue)[index:], (*queue)[index+1:])
			*queue = (*queue)[:len(*queue)-1]
			return true
		}
	}
	return false
}
