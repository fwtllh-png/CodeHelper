package turnexec

import (
	"context"
	"errors"
	"sync"

	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
)

var ErrClosed = errors.New("turn scope is closed")

type ControlPort interface {
	Cancel(string) error
	Steer(string) error
	ResolveApproval(toolguard.ApprovalDecision) error
	ResolveInput(interact.Reply) error
}

type Factory[S, O, P any] interface {
	Open(context.Context, S) (*Scope[S, O, P], error)
	Restore(context.Context, S) (*Scope[S, O, P], error)
}

type Scope[S, O, P any] struct {
	spec     S
	run      func(context.Context) (O, error)
	control  ControlPort
	snapshot func() P
	close    func(context.Context) error
	once     sync.Once
	closeErr error
}

func NewScope[S, O, P any](
	spec S,
	run func(context.Context) (O, error),
	control ControlPort,
	snapshot func() P,
	close func(context.Context) error,
) (*Scope[S, O, P], error) {
	if run == nil || control == nil || snapshot == nil || close == nil {
		return nil, errors.New("turn scope dependencies are incomplete")
	}
	return &Scope[S, O, P]{
		spec: spec, run: run, control: control,
		snapshot: snapshot, close: close,
	}, nil
}

func (s *Scope[S, O, P]) Spec() S {
	return s.spec
}

func (s *Scope[S, O, P]) Run(ctx context.Context) (O, error) {
	if s == nil || s.run == nil {
		var zero O
		return zero, ErrClosed
	}
	return s.run(ctx)
}

func (s *Scope[S, O, P]) Control() ControlPort {
	if s == nil {
		return nil
	}
	return s.control
}

func (s *Scope[S, O, P]) Snapshot() P {
	if s == nil || s.snapshot == nil {
		var zero P
		return zero
	}
	return s.snapshot()
}

func (s *Scope[S, O, P]) Close(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.closeErr = s.close(ctx)
		s.run, s.control, s.snapshot, s.close = nil, nil, nil, nil
	})
	return s.closeErr
}
