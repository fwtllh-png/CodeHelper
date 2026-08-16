package extension

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

type testRecorder struct {
	mu       sync.Mutex
	receipts []runtimeextension.LifecycleReceipt
}

type strictRecorder struct {
	mu       sync.Mutex
	sequence uint64
}

func (r *strictRecorder) Append(
	_ context.Context,
	receipt runtimeextension.LifecycleReceipt,
) error {
	if receipt.Sequence == 1 {
		time.Sleep(10 * time.Millisecond)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if receipt.Sequence != r.sequence+1 {
		return errors.New("receipt sequence reordered")
	}
	r.sequence = receipt.Sequence
	return nil
}

func (r *testRecorder) Append(
	_ context.Context,
	receipt runtimeextension.LifecycleReceipt,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.receipts = append(r.receipts, receipt)
	return nil
}

type effectCounters struct {
	cancel atomic.Int32
	drain  atomic.Int32
	close  atomic.Int32
}

func (c *effectCounters) effect(closeErr error) runtimeextension.Effect {
	return runtimeextension.EffectFuncs{
		CancelFunc: func(context.Context) error {
			c.cancel.Add(1)
			return nil
		},
		DrainFunc: func(context.Context) error {
			c.drain.Add(1)
			return nil
		},
		CloseFunc: func(context.Context) error {
			c.close.Add(1)
			return closeErr
		},
	}
}

func TestLifecycleActivationFailureRollsBackEveryPriorEffect(t *testing.T) {
	for failedStep := range 4 {
		t.Run(string(rune('0'+failedStep)), func(t *testing.T) {
			registry := NewLifecycleRegistry(&testRecorder{}, 0)
			counters := make([]*effectCounters, 4)
			steps := make([]runtimeextension.ActivationStep, 4)
			for index := range steps {
				counters[index] = &effectCounters{}
				current := index
				steps[index] = runtimeextension.ActivationStep{
					Name: "step",
					Start: func(
						_ context.Context,
						scope runtimeextension.EffectScope,
					) error {
						if current == failedStep {
							return errors.New("injected")
						}
						_, err := scope.Register(counters[current].effect(nil))
						return err
					},
				}
			}
			err := registry.Activate(t.Context(), runtimeextension.Activation{
				Owner: testOwner(1, 1, "capability", runtimeextension.EffectProcess),
				Steps: steps,
			})
			if err == nil {
				t.Fatal("activation failure was accepted")
			}
			for index, counters := range counters {
				expected := int32(0)
				if index < failedStep {
					expected = 1
				}
				if counters.cancel.Load() != expected ||
					counters.drain.Load() != expected ||
					counters.close.Load() != expected {
					t.Fatalf(
						"step %d cleanup = cancel:%d drain:%d close:%d",
						index, counters.cancel.Load(), counters.drain.Load(),
						counters.close.Load(),
					)
				}
			}
			health := registry.Health()
			if len(health) != 1 || health[0].EffectCount != 0 ||
				health[0].State != runtimeextension.StateInactive {
				t.Fatalf("health = %+v", health)
			}
		})
	}
}

func TestLifecycleEveryEffectKindRollsBackToZero(t *testing.T) {
	kinds := []runtimeextension.EffectKind{
		runtimeextension.EffectToolRegistration,
		runtimeextension.EffectProcess,
		runtimeextension.EffectConnection,
		runtimeextension.EffectHook,
		runtimeextension.EffectSubscription,
		runtimeextension.EffectLease,
		runtimeextension.EffectTimer,
	}
	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			registry := NewLifecycleRegistry(&testRecorder{}, 0)
			counters := &effectCounters{}
			owner := testOwner(1, 1, string(kind), kind)
			err := registry.Activate(t.Context(), runtimeextension.Activation{
				Owner: owner,
				Steps: []runtimeextension.ActivationStep{
					{
						Name: "resource",
						Start: func(
							_ context.Context,
							scope runtimeextension.EffectScope,
						) error {
							_, registerErr := scope.Register(counters.effect(nil))
							return registerErr
						},
					},
					{
						Name: "fault",
						Start: func(
							context.Context,
							runtimeextension.EffectScope,
						) error {
							return errors.New("injected")
						},
					},
				},
			})
			if err == nil {
				t.Fatal("activation fault was accepted")
			}
			health := registry.Health()
			if len(health) != 1 || health[0].EffectCount != 0 {
				t.Fatalf("health = %+v", health)
			}
			if counters.cancel.Load() != 1 || counters.drain.Load() != 1 ||
				counters.close.Load() != 1 {
				t.Fatalf(
					"cleanup = cancel:%d drain:%d close:%d",
					counters.cancel.Load(), counters.drain.Load(),
					counters.close.Load(),
				)
			}
		})
	}
}

func TestLifecycleGenerationSwitchRejectsStaleCallsAndDrains(t *testing.T) {
	registry := NewLifecycleRegistry(&testRecorder{}, 0)
	first := testOwner(1, 1, "tool", runtimeextension.EffectToolRegistration)
	firstCounters := &effectCounters{}
	if err := registry.Activate(t.Context(), activationWithEffect(
		first, firstCounters.effect(nil),
	)); err != nil {
		t.Fatal(err)
	}
	release, err := registry.Begin(first)
	if err != nil {
		t.Fatal(err)
	}
	second := testOwner(2, 2, "tool", runtimeextension.EffectToolRegistration)
	secondCounters := &effectCounters{}
	updated := make(chan error, 1)
	go func() {
		updated <- registry.Activate(
			context.Background(),
			activationWithEffect(second, secondCounters.effect(nil)),
		)
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		probe, probeErr := registry.Begin(first)
		if errors.Is(probeErr, ErrLifecycleUnavailable) {
			break
		}
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		probe()
		time.Sleep(time.Millisecond)
	}
	if _, err := registry.Begin(first); !errors.Is(err, ErrLifecycleUnavailable) {
		t.Fatalf("stale begin error = %v", err)
	}
	secondRelease, err := registry.Begin(second)
	if err != nil {
		t.Fatalf("new generation unavailable: %v", err)
	}
	secondRelease()
	release()
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if firstCounters.close.Load() != 1 {
		t.Fatalf("old generation close count = %d", firstCounters.close.Load())
	}
}

func TestLifecycleDrainAndRevokeAreConcurrentAndIdempotent(t *testing.T) {
	for _, action := range []string{"drain", "revoke"} {
		t.Run(action, func(t *testing.T) {
			registry := NewLifecycleRegistry(&testRecorder{}, 0)
			owner := testOwner(1, 1, action, runtimeextension.EffectSubscription)
			counters := &effectCounters{}
			if err := registry.Activate(
				t.Context(), activationWithEffect(owner, counters.effect(nil)),
			); err != nil {
				t.Fatal(err)
			}
			var wait sync.WaitGroup
			errorsFound := make(chan error, 16)
			for range 16 {
				wait.Add(1)
				go func() {
					defer wait.Done()
					var err error
					if action == "drain" {
						err = registry.Drain(context.Background(), owner)
					} else {
						err = registry.Revoke(context.Background(), owner)
					}
					errorsFound <- err
				}()
			}
			wait.Wait()
			close(errorsFound)
			for err := range errorsFound {
				if err != nil {
					t.Fatal(err)
				}
			}
			if counters.drain.Load() != 1 || counters.close.Load() != 1 {
				t.Fatalf(
					"cleanup = drain:%d close:%d",
					counters.drain.Load(), counters.close.Load(),
				)
			}
			expectedCancel := int32(0)
			if action == "revoke" {
				expectedCancel = 1
			}
			if counters.cancel.Load() != expectedCancel {
				t.Fatalf("cancel count = %d", counters.cancel.Load())
			}
		})
	}
}

func TestLifecycleConcurrentReceiptsCommitInSequenceOrder(t *testing.T) {
	recorder := &strictRecorder{}
	registry := NewLifecycleRegistry(recorder, 0)
	var wait sync.WaitGroup
	failures := make(chan error, 20)
	for index := range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			owner := testOwner(
				1, 1, fmt.Sprintf("capability-%d", index),
				runtimeextension.EffectLease,
			)
			failures <- registry.Activate(
				context.Background(),
				activationWithEffect(owner, (&effectCounters{}).effect(nil)),
			)
		}()
	}
	wait.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if recorder.sequence != 20 {
		t.Fatalf("receipt sequence = %d", recorder.sequence)
	}
}

func TestLifecycleCloseFailureQuarantinesAndIsolatesCapability(t *testing.T) {
	registry := NewLifecycleRegistry(&testRecorder{}, 0)
	broken := testOwner(1, 1, "broken", runtimeextension.EffectConnection)
	healthy := testOwner(1, 1, "healthy", runtimeextension.EffectConnection)
	if err := registry.Activate(t.Context(), activationWithEffect(
		broken, (&effectCounters{}).effect(errors.New("close failed")),
	)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Activate(t.Context(), activationWithEffect(
		healthy, (&effectCounters{}).effect(nil),
	)); err != nil {
		t.Fatal(err)
	}
	if err := registry.Drain(t.Context(), broken); err == nil {
		t.Fatal("close failure was accepted")
	}
	if _, err := registry.Begin(broken); !errors.Is(err, ErrLifecycleUnavailable) {
		t.Fatalf("quarantined begin error = %v", err)
	}
	release, err := registry.Begin(healthy)
	if err != nil {
		t.Fatalf("unrelated capability unavailable: %v", err)
	}
	release()
	health := registry.Health()
	for _, item := range health {
		if item.Owner == broken &&
			(item.State != runtimeextension.StateQuarantined ||
				item.FailureCode != "drain_failed") {
			t.Fatalf("broken health = %+v", item)
		}
	}
}

func TestLifecycleDrainTimeoutQuarantinesAndClosesEffects(t *testing.T) {
	recorder := &testRecorder{}
	registry := NewLifecycleRegistry(recorder, 0)
	owner := testOwner(1, 1, "lease", runtimeextension.EffectLease)
	counters := &effectCounters{}
	if err := registry.Activate(
		t.Context(), activationWithEffect(owner, counters.effect(nil)),
	); err != nil {
		t.Fatal(err)
	}
	release, err := registry.Begin(owner)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := registry.Drain(ctx, owner); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error = %v", err)
	}
	release()
	health := registry.Health()
	if len(health) != 1 ||
		health[0].State != runtimeextension.StateQuarantined ||
		health[0].EffectCount != 0 {
		t.Fatalf("health = %+v", health)
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	last := recorder.receipts[len(recorder.receipts)-1]
	if last.Action != runtimeextension.ActionQuarantine ||
		last.FailureCode != "drain_failed" ||
		last.FailureDigest == "" {
		t.Fatalf("failure receipt = %+v", last)
	}
}

func TestLifecycleSecurityRevokeRejectsEveryNewCall(t *testing.T) {
	registry := NewLifecycleRegistry(&testRecorder{}, 0)
	owner := testOwner(1, 1, "process", runtimeextension.EffectProcess)
	if err := registry.Activate(
		t.Context(), activationWithEffect(owner, (&effectCounters{}).effect(nil)),
	); err != nil {
		t.Fatal(err)
	}
	if err := registry.Revoke(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	for range 100 {
		if _, err := registry.Begin(owner); !errors.Is(err, ErrLifecycleUnavailable) {
			t.Fatalf("revoked call error = %v", err)
		}
	}
}

func TestLifecycleReconcileFailureRollsBackBatchInReverse(t *testing.T) {
	registry := NewLifecycleRegistry(&testRecorder{}, 0)
	firstCounters := &effectCounters{}
	first := runtimeextension.Activation{
		Owner: testOwner(1, 1, "first", runtimeextension.EffectLease),
		Steps: activationWithEffect(
			testOwner(1, 1, "first", runtimeextension.EffectLease),
			firstCounters.effect(nil),
		).Steps,
	}
	second := runtimeextension.Activation{
		Owner: testOwner(1, 1, "second", runtimeextension.EffectTimer),
		Steps: []runtimeextension.ActivationStep{{
			Name: "fail",
			Start: func(context.Context, runtimeextension.EffectScope) error {
				return errors.New("injected")
			},
		}},
	}
	if err := registry.Reconcile(
		t.Context(), "plugin:test", []runtimeextension.Activation{first, second},
	); err == nil {
		t.Fatal("reconcile failure was accepted")
	}
	if firstCounters.cancel.Load() != 1 || firstCounters.close.Load() != 1 {
		t.Fatalf(
			"rollback cleanup = cancel:%d close:%d",
			firstCounters.cancel.Load(), firstCounters.close.Load(),
		)
	}
	if _, err := registry.Begin(first.Owner); !errors.Is(err, ErrLifecycleUnavailable) {
		t.Fatalf("rolled back owner begin error = %v", err)
	}
}

func TestLifecycleReconcileFailureKeepsPreviousGenerationActive(t *testing.T) {
	registry := NewLifecycleRegistry(&testRecorder{}, 0)
	previous := testOwner(1, 1, "first", runtimeextension.EffectLease)
	if err := registry.Reconcile(
		t.Context(),
		"plugin:test",
		[]runtimeextension.Activation{
			activationWithEffect(previous, (&effectCounters{}).effect(nil)),
		},
	); err != nil {
		t.Fatal(err)
	}
	next := testOwner(2, 2, "first", runtimeextension.EffectLease)
	failing := testOwner(2, 2, "second", runtimeextension.EffectTimer)
	err := registry.Reconcile(
		t.Context(),
		"plugin:test",
		[]runtimeextension.Activation{
			activationWithEffect(next, (&effectCounters{}).effect(nil)),
			{
				Owner: failing,
				Steps: []runtimeextension.ActivationStep{{
					Name: "fail",
					Start: func(
						context.Context,
						runtimeextension.EffectScope,
					) error {
						return errors.New("injected")
					},
				}},
			},
		},
	)
	if err == nil {
		t.Fatal("failed update reconcile was accepted")
	}
	release, err := registry.Begin(previous)
	if err != nil {
		t.Fatalf("previous generation was not retained: %v", err)
	}
	release()
	if _, err := registry.Begin(next); !errors.Is(err, ErrLifecycleUnavailable) {
		t.Fatalf("failed next generation begin error = %v", err)
	}
}

func TestLifecycleReconcileRebindsUnchangedGenerationWithoutRestart(t *testing.T) {
	registry := NewLifecycleRegistry(&testRecorder{}, 0)
	counters := &effectCounters{}
	first := testOwner(1, 1, "connection", runtimeextension.EffectConnection)
	if err := registry.Reconcile(
		t.Context(), "plugin:test",
		[]runtimeextension.Activation{
			activationWithEffect(first, counters.effect(nil)),
		},
	); err != nil {
		t.Fatal(err)
	}
	second := testOwner(2, 1, "connection", runtimeextension.EffectConnection)
	if err := registry.Reconcile(
		t.Context(), "plugin:test",
		[]runtimeextension.Activation{
			activationWithEffect(second, counters.effect(nil)),
		},
	); err != nil {
		t.Fatal(err)
	}
	if counters.cancel.Load() != 0 || counters.drain.Load() != 0 ||
		counters.close.Load() != 0 {
		t.Fatalf(
			"rebind restarted effect: cancel=%d drain=%d close=%d",
			counters.cancel.Load(), counters.drain.Load(), counters.close.Load(),
		)
	}
	if _, err := registry.Begin(first); !errors.Is(err, ErrLifecycleUnavailable) {
		t.Fatalf("old plan owner begin error = %v", err)
	}
	release, err := registry.Begin(second)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func activationWithEffect(
	owner runtimeextension.EffectOwner,
	effect runtimeextension.Effect,
) runtimeextension.Activation {
	return runtimeextension.Activation{
		Owner: owner,
		Steps: []runtimeextension.ActivationStep{{
			Name: "register",
			Start: func(
				_ context.Context,
				scope runtimeextension.EffectScope,
			) error {
				_, err := scope.Register(effect)
				return err
			},
		}},
	}
}

func testOwner(
	plan, generation uint64,
	capability string,
	kind runtimeextension.EffectKind,
) runtimeextension.EffectOwner {
	return runtimeextension.EffectOwner{
		ExtensionID: "plugin/test", SourceID: "plugin:test",
		PlanRevision: plan, Generation: generation,
		CapabilityID: capability, Kind: kind,
	}
}
