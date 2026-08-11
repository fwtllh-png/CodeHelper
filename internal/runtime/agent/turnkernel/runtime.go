package turnkernel

import (
	"context"
	"errors"
	"sync"
)

var ErrCoordinatorAlreadyActive = errors.New("turn coordinator is already active")

type CoordinatorHandle struct {
	Coordinator *TurnCoordinator
	Dispatcher  *DurableEffectDispatcher
	Restored    bool
}

// CoordinatorRuntime owns per-Turn Coordinator construction. Production
// implementations are injected by runtime/app/wire so Engine never selects a
// persistence implementation.
type CoordinatorRuntime interface {
	Open(context.Context, string, State) (CoordinatorHandle, error)
	Restore(context.Context, string) (CoordinatorHandle, error)
	Release(context.Context, string) error
}

type StoreCoordinatorRuntime struct {
	mu     sync.Mutex
	store  DomainFactStore
	active map[string]CoordinatorHandle
}

func NewStoreCoordinatorRuntime(store DomainFactStore) (*StoreCoordinatorRuntime, error) {
	if store == nil {
		return nil, errors.New("coordinator runtime domain fact store is nil")
	}
	return &StoreCoordinatorRuntime{
		store:  store,
		active: make(map[string]CoordinatorHandle),
	}, nil
}

func NewEphemeralCoordinatorRuntime() *StoreCoordinatorRuntime {
	runtime, err := NewStoreCoordinatorRuntime(
		NewMemoryTerminalEnvelopeStore(nil, nil),
	)
	if err != nil {
		panic(err)
	}
	return runtime
}

func (r *StoreCoordinatorRuntime) Open(
	ctx context.Context,
	turnID string,
	initial State,
) (CoordinatorHandle, error) {
	if err := r.reserve(turnID); err != nil {
		return CoordinatorHandle{}, err
	}
	handle, err := r.openReserved(ctx, turnID, initial)
	if err != nil {
		r.unreserve(turnID)
		return CoordinatorHandle{}, err
	}
	r.activate(turnID, handle)
	return handle, nil
}

func (r *StoreCoordinatorRuntime) Restore(
	ctx context.Context,
	turnID string,
) (CoordinatorHandle, error) {
	if err := r.reserve(turnID); err != nil {
		return CoordinatorHandle{}, err
	}
	dispatcher := NewDurableEffectDispatcher()
	coordinator, err := RestoreTurnCoordinator(
		ctx,
		turnID,
		r.store,
		dispatcher,
	)
	if err != nil {
		r.unreserve(turnID)
		return CoordinatorHandle{}, err
	}
	handle := CoordinatorHandle{
		Coordinator: coordinator,
		Dispatcher:  dispatcher,
		Restored:    true,
	}
	r.activate(turnID, handle)
	return handle, nil
}

func (r *StoreCoordinatorRuntime) Release(
	_ context.Context,
	turnID string,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, turnID)
	return nil
}

func (r *StoreCoordinatorRuntime) openReserved(
	ctx context.Context,
	turnID string,
	initial State,
) (CoordinatorHandle, error) {
	facts, err := r.store.LoadDomainFacts(ctx, turnID)
	if err != nil {
		return CoordinatorHandle{}, err
	}
	dispatcher := NewDurableEffectDispatcher()
	if len(facts) != 0 {
		coordinator, err := RestoreTurnCoordinator(
			ctx,
			turnID,
			r.store,
			dispatcher,
		)
		if err != nil {
			return CoordinatorHandle{}, err
		}
		return CoordinatorHandle{
			Coordinator: coordinator,
			Dispatcher:  dispatcher,
			Restored:    true,
		}, nil
	}
	coordinator, err := NewTurnCoordinator(
		turnID,
		initial,
		r.store,
		dispatcher,
	)
	if err != nil {
		return CoordinatorHandle{}, err
	}
	return CoordinatorHandle{
		Coordinator: coordinator,
		Dispatcher:  dispatcher,
	}, nil
}

func (r *StoreCoordinatorRuntime) reserve(turnID string) error {
	if r == nil || turnID == "" {
		return errors.New("coordinator runtime turn id is empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.active[turnID]; exists {
		return ErrCoordinatorAlreadyActive
	}
	r.active[turnID] = CoordinatorHandle{}
	return nil
}

func (r *StoreCoordinatorRuntime) activate(
	turnID string,
	handle CoordinatorHandle,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[turnID] = handle
}

func (r *StoreCoordinatorRuntime) unreserve(turnID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, turnID)
}
