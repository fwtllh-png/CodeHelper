package engine

import (
	"context"
	"sync"
	"time"
)

// SharedRateLimit is the session-wide provider sample gate. Parent and child
// engines share one instance so concurrent samples queue on a single in-flight
// request and honor one Retry-After cooldown instead of each hammering the
// provider with a private retry pot.
type SharedRateLimit struct {
	init sync.Once
	mu   sync.Mutex

	token         chan struct{}
	retries       uint32
	waited        time.Duration
	cooldownUntil time.Time
}

func NewSharedRateLimit() *SharedRateLimit {
	limiter := &SharedRateLimit{}
	limiter.ensure()
	return limiter
}

func (s *SharedRateLimit) ensure() {
	if s == nil {
		return
	}
	s.init.Do(func() {
		s.token = make(chan struct{}, 1)
		s.token <- struct{}{}
	})
}

func (s *SharedRateLimit) Load() (uint32, time.Duration) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retries, s.waited
}

func (s *SharedRateLimit) Record(delay time.Duration) (uint32, time.Duration) {
	if s == nil {
		return 1, delay
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retries++
	if delay > 0 {
		s.waited += delay
		until := time.Now().Add(delay)
		if until.After(s.cooldownUntil) {
			s.cooldownUntil = until
		}
	}
	return s.retries, s.waited
}

// ObserveSuccess clears the storm pot after a provider sample completes.
func (s *SharedRateLimit) ObserveSuccess() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retries = 0
	s.waited = 0
	s.cooldownUntil = time.Time{}
}

// BeginUserTurn refreshes the wait pot for a new parent user turn. Remaining
// Retry-After is kept so Continue does not immediately re-hit a hot provider.
func (s *SharedRateLimit) BeginUserTurn() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retries = 0
	s.waited = 0
}

// Hot reports an active cooldown or a storm that has already consumed retries.
func (s *SharedRateLimit) Hot() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.retries > 0 || time.Now().Before(s.cooldownUntil)
}

// Acquire serializes one provider sample for the session and waits out any
// remaining Retry-After before the caller may send. The returned release must
// run after the attempt, including on error.
func (s *SharedRateLimit) Acquire(ctx context.Context) (func(), error) {
	if s == nil {
		return func() {}, nil
	}
	s.ensure()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.token:
	}
	if err := s.waitCooldown(ctx); err != nil {
		s.token <- struct{}{}
		return nil, err
	}
	var once sync.Once
	return func() {
		once.Do(func() { s.token <- struct{}{} })
	}, nil
}

func (s *SharedRateLimit) waitCooldown(ctx context.Context) error {
	s.mu.Lock()
	until := s.cooldownUntil
	s.mu.Unlock()
	delay := time.Until(until)
	if delay <= 0 {
		return nil
	}
	return waitRetryDelay(ctx, delay)
}

type providerSampleLease struct {
	limiter *SharedRateLimit
	release func()
}

func (e *Engine) holdProviderSample(ctx context.Context) (*providerSampleLease, error) {
	lease := &providerSampleLease{limiter: e.options.SharedRateLimit}
	if e.options.SharedRateLimit == nil {
		return lease, nil
	}
	release, err := e.options.SharedRateLimit.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	lease.release = release
	return lease, nil
}

func (l *providerSampleLease) Release() {
	if l == nil || l.release == nil {
		return
	}
	l.release()
	l.release = nil
}

func (l *providerSampleLease) NoteRateLimit(delay time.Duration) {
	if l != nil && l.limiter != nil {
		l.limiter.Record(delay)
	}
	l.Release()
}

func (l *providerSampleLease) Succeeded() {
	if l != nil && l.limiter != nil {
		l.limiter.ObserveSuccess()
	}
	l.Release()
}
