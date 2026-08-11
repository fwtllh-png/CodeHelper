package wire

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

const defaultTurnCoordinatorLease = 30 * time.Second

type durableCoordinatorRuntime struct {
	store    *turnstate.Store
	kernel   *turnkernel.StoreCoordinatorRuntime
	owner    string
	lease    time.Duration
	stop     chan struct{}
	done     chan struct{}
	closeOne sync.Once

	mu     sync.Mutex
	active map[string]struct{}
	err    error
}

type leaseGuardedFactStore struct {
	runtime *durableCoordinatorRuntime
	store   turnkernel.DomainFactStore
}

func newDurableCoordinatorRuntime(
	store *turnstate.Store,
	owner string,
	lease time.Duration,
) (*durableCoordinatorRuntime, error) {
	if store == nil {
		return nil, errors.New("durable coordinator store is nil")
	}
	if lease <= 0 {
		lease = defaultTurnCoordinatorLease
	}
	runtime := &durableCoordinatorRuntime{
		store:  store,
		owner:  owner,
		lease:  lease,
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
		active: make(map[string]struct{}),
	}
	kernel, err := turnkernel.NewStoreCoordinatorRuntime(
		leaseGuardedFactStore{runtime: runtime, store: store},
	)
	if err != nil {
		return nil, err
	}
	runtime.kernel = kernel
	go runtime.heartbeat()
	return runtime, nil
}

func (r *durableCoordinatorRuntime) RecoverActiveTurns(
	ctx context.Context,
) ([]turnstate.ActiveTurn, error) {
	if err := r.health(); err != nil {
		return nil, err
	}
	if len(r.activeTurnIDs()) != 0 {
		return nil, turnkernel.ErrCoordinatorAlreadyActive
	}
	turns, err := r.store.ClaimActiveTurns(ctx, r.owner, r.lease)
	if err != nil {
		return nil, err
	}
	restored := make([]turnstate.ActiveTurn, 0, len(turns))
	for _, turn := range turns {
		r.track(turn.TurnID)
		if _, err := r.kernel.Restore(ctx, turn.TurnID); err != nil {
			r.untrack(turn.TurnID)
			_ = r.store.ReleaseTurns(
				context.WithoutCancel(ctx),
				r.owner,
				[]string{turn.TurnID},
			)
			for _, active := range restored {
				_ = r.kernel.Release(
					context.WithoutCancel(ctx),
					active.TurnID,
				)
				r.untrack(active.TurnID)
			}
			_ = r.store.ReleaseTurns(
				context.WithoutCancel(ctx),
				r.owner,
				activeTurnIDs(restored),
			)
			return nil, fmt.Errorf(
				"restore active turn %q: %w",
				turn.TurnID,
				err,
			)
		}
		restored = append(restored, turn)
	}
	return restored, nil
}

func (r *durableCoordinatorRuntime) Open(
	ctx context.Context,
	turnID string,
	state turnkernel.State,
) (turnkernel.CoordinatorHandle, error) {
	if err := r.health(); err != nil {
		return turnkernel.CoordinatorHandle{}, err
	}
	if err := r.store.ClaimTurn(ctx, turnID, r.owner, r.lease); err != nil {
		return turnkernel.CoordinatorHandle{}, err
	}
	r.track(turnID)
	handle, err := r.kernel.Open(ctx, turnID, state)
	if err != nil {
		r.untrack(turnID)
		_ = r.store.ReleaseTurns(
			context.WithoutCancel(ctx),
			r.owner,
			[]string{turnID},
		)
		return turnkernel.CoordinatorHandle{}, err
	}
	return handle, nil
}

func (r *durableCoordinatorRuntime) Restore(
	ctx context.Context,
	turnID string,
) (turnkernel.CoordinatorHandle, error) {
	if err := r.health(); err != nil {
		return turnkernel.CoordinatorHandle{}, err
	}
	if err := r.store.ClaimTurn(ctx, turnID, r.owner, r.lease); err != nil {
		return turnkernel.CoordinatorHandle{}, err
	}
	r.track(turnID)
	handle, err := r.kernel.Restore(ctx, turnID)
	if err != nil {
		r.untrack(turnID)
		_ = r.store.ReleaseTurns(
			context.WithoutCancel(ctx),
			r.owner,
			[]string{turnID},
		)
		return turnkernel.CoordinatorHandle{}, err
	}
	return handle, nil
}

func (r *durableCoordinatorRuntime) Release(
	ctx context.Context,
	turnID string,
) error {
	if err := r.kernel.Release(ctx, turnID); err != nil {
		return err
	}
	if err := r.store.ReleaseTurns(ctx, r.owner, []string{turnID}); err != nil {
		return err
	}
	r.untrack(turnID)
	return nil
}

func (r *durableCoordinatorRuntime) Close(ctx context.Context) error {
	r.closeOne.Do(func() {
		close(r.stop)
		<-r.done
	})
	turnIDs := r.activeTurnIDs()
	for _, turnID := range turnIDs {
		_ = r.kernel.Release(ctx, turnID)
	}
	err := r.store.ReleaseTurns(ctx, r.owner, turnIDs)
	if err == nil {
		r.mu.Lock()
		clear(r.active)
		r.mu.Unlock()
	}
	return err
}

func (r *durableCoordinatorRuntime) heartbeat() {
	defer close(r.done)
	ticker := time.NewTicker(r.lease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			turnIDs := r.activeTurnIDs()
			if len(turnIDs) == 0 {
				continue
			}
			ctx, cancel := context.WithTimeout(
				context.Background(),
				r.lease/3,
			)
			err := r.store.RenewTurns(ctx, r.owner, turnIDs, r.lease)
			cancel()
			if err != nil {
				r.fail(fmt.Errorf("renew turn coordinator leases: %w", err))
				return
			}
		}
	}
}

func (r *durableCoordinatorRuntime) health() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

func (r *durableCoordinatorRuntime) fail(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err == nil {
		r.err = err
	}
}

func (r *durableCoordinatorRuntime) track(turnID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[turnID] = struct{}{}
}

func (r *durableCoordinatorRuntime) untrack(turnID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, turnID)
}

func (r *durableCoordinatorRuntime) activeTurnIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	turnIDs := make([]string, 0, len(r.active))
	for turnID := range r.active {
		turnIDs = append(turnIDs, turnID)
	}
	slices.Sort(turnIDs)
	return turnIDs
}

func (s leaseGuardedFactStore) AppendDomainFacts(
	ctx context.Context,
	turnID string,
	expectedNext uint64,
	facts []turnkernel.DomainFact,
) error {
	if err := s.runtime.health(); err != nil {
		return err
	}
	return s.store.AppendDomainFacts(ctx, turnID, expectedNext, facts)
}

func (s leaseGuardedFactStore) LoadDomainFacts(
	ctx context.Context,
	turnID string,
) ([]turnkernel.DomainFact, error) {
	if err := s.runtime.health(); err != nil {
		return nil, err
	}
	return s.store.LoadDomainFacts(ctx, turnID)
}

func activeTurnIDs(turns []turnstate.ActiveTurn) []string {
	ids := make([]string, len(turns))
	for index, turn := range turns {
		ids[index] = turn.TurnID
	}
	return ids
}
