package extension

import (
	"context"
	"errors"
	"strings"
	"time"
)

type LifecycleState string

const (
	StateInactive    LifecycleState = "inactive"
	StateActivating  LifecycleState = "activating"
	StateActive      LifecycleState = "active"
	StateDraining    LifecycleState = "draining"
	StateRevoked     LifecycleState = "revoked"
	StateQuarantined LifecycleState = "quarantined"
)

type EffectKind string

const (
	EffectToolRegistration EffectKind = "tool_registration"
	EffectProcess          EffectKind = "process"
	EffectConnection       EffectKind = "connection"
	EffectHook             EffectKind = "hook"
	EffectSubscription     EffectKind = "subscription"
	EffectLease            EffectKind = "lease"
	EffectTimer            EffectKind = "timer"
)

type EffectOwner struct {
	ExtensionID  string     `json:"extension_id"`
	SourceID     string     `json:"source_id"`
	PlanRevision uint64     `json:"plan_revision"`
	Generation   uint64     `json:"generation"`
	CapabilityID string     `json:"capability_id"`
	Kind         EffectKind `json:"kind"`
}

func (o EffectOwner) Validate() error {
	if strings.TrimSpace(o.ExtensionID) == "" ||
		strings.TrimSpace(o.SourceID) == "" ||
		strings.TrimSpace(o.CapabilityID) == "" ||
		o.PlanRevision == 0 || o.Generation == 0 {
		return errors.New("extension effect owner identity is incomplete")
	}
	switch o.Kind {
	case EffectToolRegistration, EffectProcess, EffectConnection, EffectHook,
		EffectSubscription, EffectLease, EffectTimer:
		return nil
	default:
		return errors.New("extension effect kind is invalid")
	}
}

type Effect interface {
	Cancel(context.Context) error
	Drain(context.Context) error
	Close(context.Context) error
}

type EffectFuncs struct {
	CancelFunc func(context.Context) error
	DrainFunc  func(context.Context) error
	CloseFunc  func(context.Context) error
}

func (e EffectFuncs) Cancel(ctx context.Context) error {
	if e.CancelFunc == nil {
		return nil
	}
	return e.CancelFunc(ctx)
}

func (e EffectFuncs) Drain(ctx context.Context) error {
	if e.DrainFunc == nil {
		return nil
	}
	return e.DrainFunc(ctx)
}

func (e EffectFuncs) Close(ctx context.Context) error {
	if e.CloseFunc == nil {
		return nil
	}
	return e.CloseFunc(ctx)
}

type EffectHandle struct {
	id uint64
}

func (h EffectHandle) Valid() bool { return h.id != 0 }

// NewEffectHandle is reserved for the runtime-owned Effect Registry.
func NewEffectHandle(id uint64) EffectHandle {
	return EffectHandle{id: id}
}

type EffectScope interface {
	Owner() EffectOwner
	Register(Effect) (EffectHandle, error)
}

type ActivationStep struct {
	Name  string
	Start func(context.Context, EffectScope) error
}

func (s ActivationStep) Validate() error {
	if strings.TrimSpace(s.Name) == "" || s.Start == nil {
		return errors.New("extension activation step is invalid")
	}
	return nil
}

type Activation struct {
	Owner EffectOwner
	Steps []ActivationStep
}

func (a Activation) Validate() error {
	if err := a.Owner.Validate(); err != nil {
		return err
	}
	for _, step := range a.Steps {
		if err := step.Validate(); err != nil {
			return err
		}
	}
	return nil
}

type CapabilityHealth struct {
	Owner       EffectOwner
	State       LifecycleState
	EffectCount int
	InFlight    int
	FailureCode string
	ChangedAt   time.Time
}

type LifecycleAction string

const (
	ActionActivate   LifecycleAction = "activate"
	ActionDrain      LifecycleAction = "drain"
	ActionRevoke     LifecycleAction = "revoke"
	ActionQuarantine LifecycleAction = "quarantine"
	ActionRecover    LifecycleAction = "recover"
)

type LifecycleReceipt struct {
	Version       int             `json:"version"`
	Sequence      uint64          `json:"sequence"`
	Owner         EffectOwner     `json:"owner"`
	Action        LifecycleAction `json:"action"`
	State         LifecycleState  `json:"state"`
	EffectCount   int             `json:"effect_count"`
	FailureCode   string          `json:"failure_code,omitempty"`
	FailureDigest string          `json:"failure_digest,omitempty"`
	OccurredAt    time.Time       `json:"occurred_at"`
}

func (r LifecycleReceipt) Validate() error {
	if r.Version != 1 || r.Sequence == 0 || r.OccurredAt.IsZero() ||
		r.EffectCount < 0 {
		return errors.New("extension lifecycle receipt is invalid")
	}
	if err := r.Owner.Validate(); err != nil {
		return err
	}
	switch r.Action {
	case ActionActivate, ActionDrain, ActionRevoke, ActionQuarantine, ActionRecover:
	default:
		return errors.New("extension lifecycle receipt action is invalid")
	}
	switch r.State {
	case StateInactive, StateActivating, StateActive, StateDraining,
		StateRevoked, StateQuarantined:
	default:
		return errors.New("extension lifecycle receipt state is invalid")
	}
	if (r.FailureCode == "") != (r.FailureDigest == "") {
		return errors.New("extension lifecycle failure receipt is incomplete")
	}
	return nil
}

type LifecycleRecorder interface {
	Append(context.Context, LifecycleReceipt) error
}
