package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

var (
	ErrLifecycleStale       = errors.New("extension lifecycle owner is stale")
	ErrLifecycleUnavailable = errors.New("extension capability is not active")
)

type LifecycleRegistry struct {
	mu        sync.Mutex
	receiptMu sync.Mutex
	entries   map[string]*effectSlot
	current   map[string]string
	recorder  runtimeextension.LifecycleRecorder
	now       func() time.Time
	sequence  uint64
	closed    bool
}

type effectSlot struct {
	opMu        sync.Mutex
	owner       runtimeextension.EffectOwner
	state       runtimeextension.LifecycleState
	effects     []registeredEffect
	nextHandle  uint64
	inFlight    int
	zero        chan struct{}
	failureCode string
	changedAt   time.Time
}

type registeredEffect struct {
	handle runtimeextension.EffectHandle
	value  runtimeextension.Effect
}

type lifecycleScope struct {
	registry *LifecycleRegistry
	slot     *effectSlot
}

func NewLifecycleRegistry(
	recorder runtimeextension.LifecycleRecorder,
	initialSequence uint64,
) *LifecycleRegistry {
	return &LifecycleRegistry{
		entries:  make(map[string]*effectSlot),
		current:  make(map[string]string),
		recorder: recorder, now: time.Now, sequence: initialSequence,
	}
}

func (s lifecycleScope) Owner() runtimeextension.EffectOwner {
	return s.slot.owner
}

func (s lifecycleScope) Register(
	effect runtimeextension.Effect,
) (runtimeextension.EffectHandle, error) {
	if effect == nil {
		return runtimeextension.EffectHandle{}, errors.New("extension effect is required")
	}
	r := s.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	key := ownerKey(s.slot.owner)
	if r.closed || r.entries[key] != s.slot ||
		s.slot.state != runtimeextension.StateActivating {
		return runtimeextension.EffectHandle{}, ErrLifecycleStale
	}
	s.slot.nextHandle++
	handle := newEffectHandle(s.slot.nextHandle)
	s.slot.effects = append(s.slot.effects, registeredEffect{
		handle: handle,
		value:  effect,
	})
	return handle, nil
}

func (r *LifecycleRegistry) Activate(
	ctx context.Context,
	activation runtimeextension.Activation,
) error {
	_, _, err := r.activate(ctx, activation, true)
	return err
}

func (r *LifecycleRegistry) activate(
	ctx context.Context,
	activation runtimeextension.Activation,
	publish bool,
) (*effectSlot, bool, error) {
	if err := contextError(ctx); err != nil {
		return nil, false, err
	}
	if err := activation.Validate(); err != nil {
		return nil, false, err
	}
	key := ownerKey(activation.Owner)
	capability := capabilityKey(activation.Owner)
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, false, errors.New("extension lifecycle registry is closed")
	}
	if existing := r.entries[key]; existing != nil {
		state := existing.state
		r.mu.Unlock()
		if state == runtimeextension.StateActive {
			return existing, false, nil
		}
		return nil, false, fmt.Errorf(
			"%w: owner is %s", ErrLifecycleUnavailable, state,
		)
	}
	previousKey := r.current[capability]
	if previous := r.entries[previousKey]; previous != nil &&
		!newerOwner(activation.Owner, previous.owner) {
		r.mu.Unlock()
		return nil, false, ErrLifecycleStale
	}
	slot := &effectSlot{
		owner: activation.Owner, state: runtimeextension.StateActivating,
		changedAt: r.now().UTC(),
	}
	r.entries[key] = slot
	r.mu.Unlock()

	scope := lifecycleScope{registry: r, slot: slot}
	for _, step := range activation.Steps {
		if err := contextError(ctx); err != nil {
			return nil, false, r.activationFailed(
				ctx, slot, "activation_canceled", err,
			)
		}
		if err := step.Start(ctx, scope); err != nil {
			return nil, false, r.activationFailed(
				ctx, slot, "activation_step_failed",
				fmt.Errorf("%s: %w", step.Name, err),
			)
		}
	}

	r.mu.Lock()
	if r.entries[key] != slot || slot.state != runtimeextension.StateActivating {
		r.mu.Unlock()
		return nil, false, r.activationFailed(
			ctx, slot, "activation_fenced", ErrLifecycleStale,
		)
	}
	slot.state = runtimeextension.StateActive
	slot.changedAt = r.now().UTC()
	if publish {
		r.current[capability] = key
	}
	r.mu.Unlock()
	if err := r.record(ctx, slot, runtimeextension.ActionActivate, ""); err != nil {
		_ = r.quarantine(context.Background(), slot, "receipt_write_failed", err)
		return nil, false, err
	}
	if publish && previousKey != "" && previousKey != key {
		if previous := r.slot(previousKey); previous != nil {
			if err := r.drainSlot(ctx, previous); err != nil {
				return slot, true, err
			}
		}
	}
	return slot, true, nil
}

func (r *LifecycleRegistry) activationFailed(
	ctx context.Context,
	slot *effectSlot,
	code string,
	cause error,
) error {
	cleanupErr := closeEffects(ctx, slot.effects, true)
	r.mu.Lock()
	slot.effects = nil
	slot.failureCode = code
	if cleanupErr != nil {
		slot.state = runtimeextension.StateQuarantined
	} else {
		slot.state = runtimeextension.StateInactive
	}
	slot.changedAt = r.now().UTC()
	r.mu.Unlock()
	action := runtimeextension.ActionActivate
	if cleanupErr != nil {
		action = runtimeextension.ActionQuarantine
	}
	recordErr := r.record(ctx, slot, action, code)
	return errors.Join(cause, cleanupErr, recordErr)
}

func (r *LifecycleRegistry) Begin(
	owner runtimeextension.EffectOwner,
) (func(), error) {
	if err := owner.Validate(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	slot := r.entries[ownerKey(owner)]
	if r.closed || slot == nil || slot.state != runtimeextension.StateActive ||
		r.current[capabilityKey(owner)] != ownerKey(owner) {
		return nil, ErrLifecycleUnavailable
	}
	if slot.inFlight == 0 {
		slot.zero = make(chan struct{})
	}
	slot.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			slot.inFlight--
			if slot.inFlight == 0 && slot.zero != nil {
				close(slot.zero)
				slot.zero = nil
			}
			r.mu.Unlock()
		})
	}, nil
}

func (r *LifecycleRegistry) BeginCurrent(
	extensionID, capabilityID string,
	kind runtimeextension.EffectKind,
) (runtimeextension.EffectOwner, func(), error) {
	key := strings.Join([]string{extensionID, capabilityID, string(kind)}, "\x00")
	r.mu.Lock()
	ownerKey := r.current[key]
	slot := r.entries[ownerKey]
	if slot == nil {
		r.mu.Unlock()
		return runtimeextension.EffectOwner{}, nil, ErrLifecycleUnavailable
	}
	owner := slot.owner
	r.mu.Unlock()
	release, err := r.Begin(owner)
	return owner, release, err
}

func (r *LifecycleRegistry) Drain(
	ctx context.Context,
	owner runtimeextension.EffectOwner,
) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	slot := r.slot(ownerKey(owner))
	if slot == nil {
		return nil
	}
	return r.drainSlot(ctx, slot)
}

func (r *LifecycleRegistry) drainSlot(
	ctx context.Context,
	slot *effectSlot,
) error {
	slot.opMu.Lock()
	defer slot.opMu.Unlock()

	r.mu.Lock()
	switch slot.state {
	case runtimeextension.StateInactive, runtimeextension.StateRevoked:
		r.mu.Unlock()
		return nil
	case runtimeextension.StateQuarantined:
		r.mu.Unlock()
		return ErrLifecycleUnavailable
	case runtimeextension.StateDraining:
		r.mu.Unlock()
		return nil
	}
	slot.state = runtimeextension.StateDraining
	slot.changedAt = r.now().UTC()
	if r.current[capabilityKey(slot.owner)] == ownerKey(slot.owner) {
		delete(r.current, capabilityKey(slot.owner))
	}
	zero := slot.zero
	r.mu.Unlock()

	waitErr := waitZero(ctx, zero)
	closeErr := closeEffects(ctx, slot.effects, waitErr != nil)
	r.mu.Lock()
	slot.effects = nil
	if waitErr != nil || closeErr != nil {
		slot.state = runtimeextension.StateQuarantined
		slot.failureCode = "drain_failed"
	} else {
		slot.state = runtimeextension.StateInactive
		slot.failureCode = ""
	}
	slot.changedAt = r.now().UTC()
	r.mu.Unlock()
	action := runtimeextension.ActionDrain
	code := ""
	if waitErr != nil || closeErr != nil {
		action = runtimeextension.ActionQuarantine
		code = "drain_failed"
	}
	return errors.Join(waitErr, closeErr, r.record(ctx, slot, action, code))
}

func (r *LifecycleRegistry) Revoke(
	ctx context.Context,
	owner runtimeextension.EffectOwner,
) error {
	if err := owner.Validate(); err != nil {
		return err
	}
	slot := r.slot(ownerKey(owner))
	if slot == nil {
		return nil
	}
	slot.opMu.Lock()
	defer slot.opMu.Unlock()
	r.mu.Lock()
	if slot.state == runtimeextension.StateRevoked {
		r.mu.Unlock()
		return nil
	}
	if slot.state == runtimeextension.StateQuarantined {
		r.mu.Unlock()
		return ErrLifecycleUnavailable
	}
	slot.state = runtimeextension.StateRevoked
	slot.changedAt = r.now().UTC()
	if r.current[capabilityKey(slot.owner)] == ownerKey(slot.owner) {
		delete(r.current, capabilityKey(slot.owner))
	}
	zero := slot.zero
	r.mu.Unlock()
	cancelErr := cancelEffects(ctx, slot.effects)
	waitErr := waitZero(ctx, zero)
	closeErr := closeEffects(ctx, slot.effects, false)
	r.mu.Lock()
	slot.effects = nil
	code := ""
	if cancelErr != nil || waitErr != nil || closeErr != nil {
		slot.state = runtimeextension.StateQuarantined
		slot.failureCode = "revoke_failed"
		code = "revoke_failed"
	}
	slot.changedAt = r.now().UTC()
	r.mu.Unlock()
	action := runtimeextension.ActionRevoke
	if code != "" {
		action = runtimeextension.ActionQuarantine
	}
	return errors.Join(
		cancelErr, waitErr, closeErr, r.record(ctx, slot, action, code),
	)
}

func (r *LifecycleRegistry) Reconcile(
	ctx context.Context,
	sourceID string,
	desired []runtimeextension.Activation,
) error {
	sourceID = strings.TrimSpace(sourceID)
	if sourceID == "" {
		return errors.New("extension reconcile source is required")
	}
	wanted := make(map[string]runtimeextension.Activation, len(desired))
	for _, activation := range desired {
		if err := activation.Validate(); err != nil {
			return err
		}
		if activation.Owner.SourceID != sourceID {
			return ErrLifecycleStale
		}
		key := capabilityKey(activation.Owner)
		if _, exists := wanted[key]; exists {
			return errors.New("extension reconcile contains duplicate capability")
		}
		wanted[key] = activation
	}
	keys := make([]string, 0, len(wanted))
	for key := range wanted {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := r.rebind(wanted[key].Owner); err != nil {
			return err
		}
	}
	var activated []*effectSlot
	for _, key := range keys {
		slot, created, err := r.activate(ctx, wanted[key], false)
		if err != nil {
			rollbackErr := error(nil)
			for index := len(activated) - 1; index >= 0; index-- {
				rollbackErr = errors.Join(
					rollbackErr,
					r.Revoke(context.Background(), activated[index].owner),
				)
			}
			return errors.Join(err, rollbackErr)
		}
		if created {
			activated = append(activated, slot)
		}
	}
	r.mu.Lock()
	for _, key := range keys {
		owner := wanted[key].Owner
		slot := r.entries[ownerKey(owner)]
		if slot == nil || slot.state != runtimeextension.StateActive {
			r.mu.Unlock()
			rollbackErr := error(nil)
			for index := len(activated) - 1; index >= 0; index-- {
				rollbackErr = errors.Join(
					rollbackErr,
					r.Revoke(context.Background(), activated[index].owner),
				)
			}
			return errors.Join(ErrLifecycleStale, rollbackErr)
		}
		r.current[key] = ownerKey(owner)
	}
	r.mu.Unlock()
	r.mu.Lock()
	var obsolete []*effectSlot
	for _, slot := range r.entries {
		if slot.owner.SourceID != sourceID ||
			slot.state != runtimeextension.StateActive {
			continue
		}
		activation, retained := wanted[capabilityKey(slot.owner)]
		if !retained || ownerKey(activation.Owner) != ownerKey(slot.owner) {
			obsolete = append(obsolete, slot)
		}
	}
	r.mu.Unlock()
	var reconcileErr error
	for _, slot := range obsolete {
		reconcileErr = errors.Join(reconcileErr, r.drainSlot(ctx, slot))
	}
	return reconcileErr
}

func (r *LifecycleRegistry) rebind(
	owner runtimeextension.EffectOwner,
) error {
	capability := capabilityKey(owner)
	nextKey := ownerKey(owner)
	r.mu.Lock()
	currentKey := r.current[capability]
	current := r.entries[currentKey]
	r.mu.Unlock()
	if current == nil || currentKey == nextKey {
		return nil
	}
	current.opMu.Lock()
	defer current.opMu.Unlock()
	r.mu.Lock()
	currentKey = r.current[capability]
	if r.entries[currentKey] != current {
		r.mu.Unlock()
		return ErrLifecycleStale
	}
	if current.state != runtimeextension.StateActive ||
		current.owner.Generation != owner.Generation ||
		!newerOwner(owner, current.owner) {
		r.mu.Unlock()
		return nil
	}
	if r.entries[nextKey] != nil {
		r.mu.Unlock()
		return ErrLifecycleStale
	}
	delete(r.entries, currentKey)
	current.owner = owner
	current.changedAt = r.now().UTC()
	r.entries[nextKey] = current
	r.current[capability] = nextKey
	r.mu.Unlock()
	return r.record(
		context.Background(), current, runtimeextension.ActionRecover, "",
	)
}

func (r *LifecycleRegistry) Health() []runtimeextension.CapabilityHealth {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]runtimeextension.CapabilityHealth, 0, len(r.entries))
	for _, slot := range r.entries {
		result = append(result, runtimeextension.CapabilityHealth{
			Owner: slot.owner, State: slot.state,
			EffectCount: len(slot.effects), InFlight: slot.inFlight,
			FailureCode: slot.failureCode, ChangedAt: slot.changedAt,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return ownerKey(result[i].Owner) < ownerKey(result[j].Owner)
	})
	return result
}

func (r *LifecycleRegistry) Close(ctx context.Context) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	slots := make([]*effectSlot, 0, len(r.entries))
	for _, slot := range r.entries {
		slots = append(slots, slot)
	}
	clear(r.current)
	r.mu.Unlock()
	var closeErr error
	for _, slot := range slots {
		closeErr = errors.Join(closeErr, r.Revoke(ctx, slot.owner))
	}
	return closeErr
}

func (r *LifecycleRegistry) quarantine(
	ctx context.Context,
	slot *effectSlot,
	code string,
	cause error,
) error {
	cleanupErr := closeEffects(ctx, slot.effects, true)
	r.mu.Lock()
	slot.effects = nil
	slot.state = runtimeextension.StateQuarantined
	slot.failureCode = code
	slot.changedAt = r.now().UTC()
	delete(r.current, capabilityKey(slot.owner))
	r.mu.Unlock()
	return errors.Join(
		cause, cleanupErr,
		r.record(ctx, slot, runtimeextension.ActionQuarantine, code),
	)
}

func (r *LifecycleRegistry) record(
	ctx context.Context,
	slot *effectSlot,
	action runtimeextension.LifecycleAction,
	code string,
) error {
	if r.recorder == nil {
		return nil
	}
	r.receiptMu.Lock()
	defer r.receiptMu.Unlock()
	r.mu.Lock()
	r.sequence++
	receipt := runtimeextension.LifecycleReceipt{
		Version: 1, Sequence: r.sequence, Owner: slot.owner,
		Action: action, State: slot.state, EffectCount: len(slot.effects),
		FailureCode: code, OccurredAt: r.now().UTC(),
	}
	if code != "" {
		sum := sha256.Sum256([]byte(code + "\x00" + ownerKey(slot.owner)))
		receipt.FailureDigest = "sha256:" + hex.EncodeToString(sum[:])
	}
	r.mu.Unlock()
	if err := receipt.Validate(); err != nil {
		return err
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	return r.recorder.Append(ctx, receipt)
}

func (r *LifecycleRegistry) slot(key string) *effectSlot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[key]
}

func closeEffects(
	ctx context.Context,
	effects []registeredEffect,
	cancel bool,
) error {
	var result error
	for index := len(effects) - 1; index >= 0; index-- {
		if cancel {
			result = errors.Join(result, effects[index].value.Cancel(ctx))
		}
		result = errors.Join(result, effects[index].value.Drain(ctx))
		result = errors.Join(result, effects[index].value.Close(ctx))
	}
	return result
}

func cancelEffects(ctx context.Context, effects []registeredEffect) error {
	var result error
	for index := len(effects) - 1; index >= 0; index-- {
		result = errors.Join(result, effects[index].value.Cancel(ctx))
	}
	return result
}

func waitZero(ctx context.Context, zero <-chan struct{}) error {
	if zero == nil {
		return contextError(ctx)
	}
	select {
	case <-zero:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newerOwner(next, current runtimeextension.EffectOwner) bool {
	if next.PlanRevision != current.PlanRevision {
		return next.PlanRevision > current.PlanRevision
	}
	return next.Generation > current.Generation
}

func capabilityKey(owner runtimeextension.EffectOwner) string {
	return strings.Join([]string{
		owner.ExtensionID, owner.CapabilityID, string(owner.Kind),
	}, "\x00")
}

func ownerKey(owner runtimeextension.EffectOwner) string {
	return fmt.Sprintf(
		"%s\x00%d\x00%d",
		capabilityKey(owner), owner.PlanRevision, owner.Generation,
	)
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("extension lifecycle context is required")
	}
	return ctx.Err()
}

func newEffectHandle(id uint64) runtimeextension.EffectHandle {
	return runtimeextension.NewEffectHandle(id)
}
