// Package rlm owns recursive language-model orchestration admission.
package rlm

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrTokenBudget = errors.New("rlm token budget exhausted")
	ErrCostBudget  = errors.New("rlm cost budget exhausted")
	ErrDepthBudget = errors.New("rlm recursion depth exceeded")
	ErrConcurrency = errors.New("rlm concurrency budget exhausted")
)

type Limits struct {
	MaxTokens      uint64
	MaxCostUSD     float64
	MaxDepth       int
	MaxConcurrency int
}

func (l Limits) WithDefaults() Limits {
	if l.MaxDepth <= 0 {
		l.MaxDepth = 5
	}
	if l.MaxConcurrency <= 0 {
		l.MaxConcurrency = 16
	}
	return l
}

type Snapshot struct {
	SpentTokens  uint64
	SpentCostUSD float64
	InFlight     int32
	MaxDepthSeen int32
}

// Governor is shared across nested RLM/parallel workers and cannot be bypassed
// because every spawn path must call Admit before work starts.
type Governor struct {
	mu     sync.Mutex
	limits Limits
	spentT uint64
	spentC float64
	flight atomic.Int32
	depth  atomic.Int32
}

func NewGovernor(limits Limits) *Governor {
	return &Governor{limits: limits.WithDefaults()}
}

func (g *Governor) Limits() Limits { return g.limits }

func (g *Governor) Snapshot() Snapshot {
	return Snapshot{
		SpentTokens: atomic.LoadUint64(&g.spentT), SpentCostUSD: g.cost(),
		InFlight: g.flight.Load(), MaxDepthSeen: g.depth.Load(),
	}
}

func (g *Governor) cost() float64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.spentC
}

type Lease struct {
	tokens uint64
	cost   float64
}

func (g *Governor) Admit(depth int, tokens uint64, cost float64) (Lease, error) {
	if depth > g.limits.MaxDepth {
		return Lease{}, ErrDepthBudget
	}
	for {
		cur := g.flight.Load()
		if int(cur) >= g.limits.MaxConcurrency {
			return Lease{}, ErrConcurrency
		}
		if g.flight.CompareAndSwap(cur, cur+1) {
			break
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.limits.MaxTokens > 0 && g.spentT+tokens > g.limits.MaxTokens {
		g.flight.Add(-1)
		return Lease{}, ErrTokenBudget
	}
	if g.limits.MaxCostUSD > 0 && g.spentC+cost > g.limits.MaxCostUSD {
		g.flight.Add(-1)
		return Lease{}, ErrCostBudget
	}
	g.spentT += tokens
	g.spentC += cost
	if int32(depth) > g.depth.Load() {
		g.depth.Store(int32(depth))
	}
	return Lease{tokens: tokens, cost: cost}, nil
}

func (g *Governor) Release(lease Lease) {
	g.flight.Add(-1)
	_ = lease
}

func (g *Governor) Charge(tokens uint64, cost float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.limits.MaxTokens > 0 && g.spentT+tokens > g.limits.MaxTokens {
		return ErrTokenBudget
	}
	if g.limits.MaxCostUSD > 0 && g.spentC+cost > g.limits.MaxCostUSD {
		return ErrCostBudget
	}
	g.spentT += tokens
	g.spentC += cost
	return nil
}

// Record books spend that already happened. Unlike Charge it cannot refuse:
// dropping a number that was really spent would leave the ledger claiming
// budget that is gone. The error reports that the limit is now exceeded, which
// is the caller's signal to stop admitting new work.
func (g *Governor) Record(tokens uint64, cost float64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spentT += tokens
	g.spentC += cost
	if g.limits.MaxTokens > 0 && g.spentT > g.limits.MaxTokens {
		return ErrTokenBudget
	}
	if g.limits.MaxCostUSD > 0 && g.spentC > g.limits.MaxCostUSD {
		return ErrCostBudget
	}
	return nil
}

// Bridge exposes RLM recursion that always admits through the shared governor.
type Bridge struct {
	Gov *Governor
}

func NewBridge(gov *Governor) *Bridge {
	if gov == nil {
		gov = NewGovernor(Limits{})
	}
	return &Bridge{Gov: gov}
}

type StepFunc func(depth int, prompt string) (string, uint64, float64, error)

func (b *Bridge) Run(depth int, prompt string, step StepFunc) (string, error) {
	if step == nil {
		return "", errors.New("rlm step is required")
	}
	lease, err := b.Gov.Admit(depth, 0, 0)
	if err != nil {
		return "", err
	}
	defer b.Gov.Release(lease)
	out, tokens, cost, err := step(depth, prompt)
	if err != nil {
		return "", err
	}
	if err := b.Gov.Charge(tokens, cost); err != nil {
		return "", err
	}
	return out, nil
}
