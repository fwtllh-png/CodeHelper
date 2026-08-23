package turnkernel

import (
	"context"
	"errors"
	"sync"
)

var ErrLifecycleClosed = errors.New("turn scope is closed")

type Lifecycle[S, O, P, C any] struct {
	mu       sync.Mutex
	spec     S
	run      func(context.Context) (O, error)
	control  C
	snapshot func() P
	close    func(context.Context) error
	once     sync.Once
	closeErr error
}

func NewLifecycle[S, O, P, C any](
	spec S,
	run func(context.Context) (O, error),
	control C,
	snapshot func() P,
	close func(context.Context) error,
) (*Lifecycle[S, O, P, C], error) {
	if run == nil || any(control) == nil || snapshot == nil || close == nil {
		return nil, errors.New("turn scope dependencies are incomplete")
	}
	return &Lifecycle[S, O, P, C]{
		spec: spec, run: run, control: control,
		snapshot: snapshot, close: close,
	}, nil
}

func (s *Lifecycle[S, O, P, C]) Spec() S {
	return s.spec
}

func (s *Lifecycle[S, O, P, C]) Run(ctx context.Context) (O, error) {
	if s == nil {
		var zero O
		return zero, ErrLifecycleClosed
	}
	s.mu.Lock()
	run := s.run
	s.mu.Unlock()
	if run == nil {
		var zero O
		return zero, ErrLifecycleClosed
	}
	return run(ctx)
}

func (s *Lifecycle[S, O, P, C]) Control() C {
	if s == nil {
		var zero C
		return zero
	}
	s.mu.Lock()
	control := s.control
	s.mu.Unlock()
	return control
}

func (s *Lifecycle[S, O, P, C]) Snapshot() P {
	if s == nil {
		var zero P
		return zero
	}
	s.mu.Lock()
	snapshot := s.snapshot
	s.mu.Unlock()
	if snapshot == nil {
		var zero P
		return zero
	}
	return snapshot()
}

func (s *Lifecycle[S, O, P, C]) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.closeErr = s.close(ctx)
		s.mu.Lock()
		var zero C
		s.run, s.control, s.snapshot, s.close = nil, zero, nil, nil
		s.mu.Unlock()
	})
	return s.closeErr
}
