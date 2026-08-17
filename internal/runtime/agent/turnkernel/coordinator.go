package turnkernel

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
)

var ErrEffectResultIdentity = errors.New("effect result identity mismatch")

type DomainFactStore interface {
	AppendDomainFacts(context.Context, string, uint64, []DomainFact) error
	LoadDomainFacts(context.Context, string) ([]DomainFact, error)
}

type EffectExecutor interface {
	ExecuteEffect(context.Context, Effect) (Command, error)
}

type EffectDispatcher interface {
	Dispatch(context.Context, Effect, func(Command) error) error
}

// DomainFactObserver receives facts only after their authoritative append
// succeeds. It is diagnostics-only: errors cannot flow back into the Kernel.
type DomainFactObserver func(context.Context, []DomainFact)

type SynchronousEffectDispatcher struct {
	Executors map[EffectKind]EffectExecutor
}

func (d SynchronousEffectDispatcher) Dispatch(
	ctx context.Context,
	effect Effect,
	submit func(Command) error,
) error {
	executor := d.Executors[effect.Kind]
	if executor == nil {
		return fmt.Errorf("no executor for effect kind %q", effect.Kind)
	}
	result, err := executor.ExecuteEffect(ctx, effect)
	if err != nil {
		result = EffectResultReceived{
			EffectID: effect.ID,
			Success:  false,
			Error:    err.Error(),
		}
	}
	if result == nil {
		return errors.New("effect executor returned no result command")
	}
	if resultEffectID(result) != effect.ID {
		return ErrEffectResultIdentity
	}
	return submit(result)
}

type TurnCoordinator struct {
	mu         sync.Mutex
	turnID     string
	state      State
	reducer    Reducer
	store      DomainFactStore
	dispatcher EffectDispatcher
	observer   DomainFactObserver
	nextFact   uint64
}

func (c *TurnCoordinator) SetDomainFactObserver(observer DomainFactObserver) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.observer = observer
}

func NewTurnCoordinator(
	turnID string,
	state State,
	store DomainFactStore,
	dispatcher EffectDispatcher,
) (*TurnCoordinator, error) {
	if turnID == "" {
		return nil, errors.New("turn coordinator turn id is empty")
	}
	if state.ProfileRevision == 0 {
		return nil, errors.New("turn coordinator requires a frozen profile revision")
	}
	if store == nil {
		return nil, errors.New("turn coordinator domain fact store is nil")
	}
	if dispatcher == nil {
		return nil, errors.New("turn coordinator effect dispatcher is nil")
	}
	if err := Validate(state); err != nil {
		return nil, err
	}
	facts, err := store.LoadDomainFacts(context.Background(), turnID)
	if err != nil {
		return nil, err
	}
	if len(facts) != 0 {
		return nil, errors.New(
			"turn coordinator requires RestoreTurnCoordinator for existing facts",
		)
	}
	return &TurnCoordinator{
		turnID: turnID, state: cloneState(state), store: store,
		dispatcher: dispatcher, nextFact: 1,
	}, nil
}

func RestoreTurnCoordinator(
	ctx context.Context,
	turnID string,
	store DomainFactStore,
	dispatcher EffectDispatcher,
) (*TurnCoordinator, error) {
	if turnID == "" || store == nil || dispatcher == nil {
		return nil, errors.New("turn coordinator restore dependencies are incomplete")
	}
	facts, err := store.LoadDomainFacts(ctx, turnID)
	if err != nil {
		return nil, err
	}
	if len(facts) == 0 {
		return nil, errors.New("turn coordinator restore has no domain facts")
	}
	var restored State
	for index, fact := range facts {
		if fact.TurnID != turnID || fact.Sequence != uint64(index+1) {
			return nil, fmt.Errorf("domain fact sequence is invalid at index %d", index)
		}
		if err := Validate(fact.State); err != nil {
			return nil, fmt.Errorf("domain fact state %d: %w", index+1, err)
		}
		digest, digestErr := Digest(fact.State)
		if digestErr != nil || digest != fact.StateDigest {
			return nil, fmt.Errorf("domain fact digest mismatch at sequence %d", fact.Sequence)
		}
		restored = cloneState(fact.State)
	}
	coordinator := &TurnCoordinator{
		turnID: turnID, state: restored, store: store,
		dispatcher: dispatcher, nextFact: uint64(len(facts) + 1),
	}
	for _, id := range sortedEffectIDs(restored.PendingEffects) {
		if restored.PendingEffects[id].Status != EffectRunning {
			continue
		}
		if err := coordinator.Submit(ctx, EffectRequeued{EffectID: id}); err != nil {
			return nil, fmt.Errorf("requeue running effect %q: %w", id, err)
		}
	}
	restored = coordinator.Snapshot()
	for _, id := range sortedEffectIDs(restored.PendingEffects) {
		effect := restored.PendingEffects[id]
		if err := dispatcher.Dispatch(ctx, effect, func(result Command) error {
			return coordinator.Submit(ctx, result)
		}); err != nil {
			return nil, err
		}
	}
	return coordinator, nil
}

func (c *TurnCoordinator) Snapshot() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneState(c.state)
}

func (c *TurnCoordinator) TurnID() string {
	return c.turnID
}

func (c *TurnCoordinator) DomainFacts(
	ctx context.Context,
) ([]DomainFact, error) {
	return c.store.LoadDomainFacts(ctx, c.turnID)
}

func (c *TurnCoordinator) Submit(ctx context.Context, command Command) error {
	effects, err := c.transition(ctx, command)
	if err != nil {
		return err
	}
	for _, effect := range effects {
		if err := c.dispatcher.Dispatch(ctx, effect, func(result Command) error {
			return c.Submit(ctx, result)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *TurnCoordinator) transition(
	ctx context.Context,
	command Command,
) ([]Effect, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	transition, err := c.reducer.Apply(c.state, command)
	if err != nil {
		return nil, err
	}
	digest, err := Digest(transition.State)
	if err != nil {
		return nil, err
	}
	facts := make([]DomainFact, 0, max(1, len(transition.Events)))
	if len(transition.Events) == 0 {
		facts = append(facts, DomainFact{
			TurnID: c.turnID, Sequence: c.nextFact,
			Command: CommandName(command), State: cloneState(transition.State),
			StateDigest: digest,
		})
	} else {
		for index, event := range transition.Events {
			facts = append(facts, DomainFact{
				TurnID: c.turnID, Sequence: c.nextFact + uint64(index),
				Command: CommandName(command), Event: event,
				State: cloneState(transition.State), StateDigest: digest,
			})
		}
	}
	if err := c.store.AppendDomainFacts(
		ctx,
		c.turnID,
		c.nextFact,
		facts,
	); err != nil {
		return nil, err
	}
	c.state = transition.State
	c.nextFact += uint64(len(facts))
	c.observeFacts(facts)
	return append([]Effect(nil), transition.Effects...), nil
}

func (c *TurnCoordinator) observeFacts(facts []DomainFact) {
	if c.observer == nil || len(facts) == 0 {
		return
	}
	func() {
		defer func() {
			if recover() != nil {
				_ = debug.Stack()
			}
		}()
		c.observer(context.Background(), facts)
	}()
}

func resultEffectID(command Command) string {
	switch value := command.(type) {
	case EffectResultReceived:
		return value.EffectID
	case PersistenceResultReceived:
		return value.EffectID
	case ApprovalResultReceived:
		return value.EffectID
	case InputResultReceived:
		return value.EffectID
	case ToolResultReceived:
		return value.EffectID
	case ModelSampleResultReceived:
		return value.EffectID
	case VerificationFinished:
		return value.EffectID
	case JournalResultReceived:
		return value.EffectID
	default:
		return ""
	}
}
