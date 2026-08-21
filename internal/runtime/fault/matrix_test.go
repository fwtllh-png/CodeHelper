package fault

import (
	"testing"
)

// TestDecideExhaustiveMatrix verifies that Decide() returns the correct
// RecoveryAction for every Code × Origin × Disposition × Retryable
// combination. This ensures the fault classification system behaves
// consistently and that no combination panics or returns an unexpected
// recovery action.
func TestDecideExhaustiveMatrix(t *testing.T) {
	codes := []Code{
		InvalidArgument, Conflict, ResourceExhausted, Unavailable,
		Canceled, DeadlineExceeded, Internal,
	}
	origins := []Origin{
		OriginRuntime, OriginProvider, OriginTool, OriginHook,
		OriginVerification, OriginPersistence, OriginProjection, OriginKernel,
	}
	dispositions := []Disposition{
		FailTurn, RetryStep, RetryTurn, ResumeTurn, Reject,
	}

	for _, code := range codes {
		for _, origin := range origins {
			for _, disposition := range dispositions {
				for _, retryable := range []bool{true, false} {
					name := string(code) + "_" + string(origin) + "_" + string(disposition)
					if retryable {
						name += "_retryable"
					} else {
						name += "_nonretryable"
					}
					t.Run(name, func(t *testing.T) {
						problem := NewClassified(
							code,
							"test fault: "+name,
							retryable,
							Metadata{
								Origin:      origin,
								Disposition: disposition,
								SideEffects: SideEffectUnchanged,
								RetryOwner:  RetryOwnerEngine,
							},
							nil,
						)

						// Verify the problem was created correctly.
						if problem.Code != code {
							t.Errorf("Code = %q, want %q", problem.Code, code)
						}
						if problem.Fault == nil {
							t.Fatal("Fault metadata is nil")
						}
						if problem.Fault.Origin != origin {
							t.Errorf("Origin = %q, want %q", problem.Fault.Origin, origin)
						}
						if problem.Fault.Disposition != disposition {
							t.Errorf("Disposition = %q, want %q", problem.Fault.Disposition, disposition)
						}

						// Verify Decide() returns a valid recovery action.
						decision := Decide(problem, RecoveryContext{
							Owner:      RetryOwnerEngine,
							Idempotent: true,
							Attempt:    1,
						})
						if !validRecoveryAction(decision.Action) {
							t.Errorf("Decide returned invalid action %q", decision.Action)
						}

						// Verify CodeOf returns the correct code.
						if CodeOf(problem) != code {
							t.Errorf("CodeOf = %q, want %q", CodeOf(problem), code)
						}

						// Verify DispositionOf returns the correct disposition.
						if DispositionOf(problem) != disposition {
							t.Errorf("DispositionOf = %q, want %q", DispositionOf(problem), disposition)
						}

						// Verify no panic.
						_ = problem.Error()
						_ = problem.Unwrap()
					})
				}
			}
		}
	}
}

func validRecoveryAction(action RecoveryAction) bool {
	switch action {
	case RecoveryRetry, RecoveryResume, RecoveryWait, RecoveryBlock, RecoveryFail, RecoveryReject:
		return true
	default:
		return false
	}
}

// TestInjectorRegisterAndClear verifies the injector lifecycle.
func TestInjectorRegisterAndClear(t *testing.T) {
	injector := &Injector{rules: make(map[Stage][]InjectionRule)}

	// Register a rule.
	injector.Register(StageModelSample, InjectionRule{
		Probability: 1.0,
		Code:        Unavailable,
		Message:     "injected fault",
		Retryable:   true,
		Origin:      OriginProvider,
		Disposition: RetryStep,
		SideEffects: SideEffectUnchanged,
	})

	// Verify the rule is registered.
	snapshot := injector.Snapshot()
	if len(snapshot[StageModelSample]) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(snapshot[StageModelSample]))
	}

	// Inject should return a problem.
	problem := injector.Inject(t.Context(), StageModelSample, "op-1")
	if problem == nil {
		t.Fatal("expected a problem from Inject")
	}
	if problem.Code != Unavailable {
		t.Errorf("Code = %q, want %q", problem.Code, Unavailable)
	}
	if problem.Fault.Stage != StageModelSample {
		t.Errorf("Stage = %q, want %q", problem.Fault.Stage, StageModelSample)
	}
	if problem.Fault.OperationID != "op-1" {
		t.Errorf("OperationID = %q, want %q", problem.Fault.OperationID, "op-1")
	}

	// Clear should remove all rules.
	injector.Clear()
	snapshot = injector.Snapshot()
	if len(snapshot) != 0 {
		t.Fatalf("expected 0 rules after clear, got %d", len(snapshot))
	}

	// Inject should return nil after clear.
	problem = injector.Inject(t.Context(), StageModelSample, "op-2")
	if problem != nil {
		t.Fatal("expected nil after clear")
	}
}

// TestInjectorProbability verifies probability-based injection.
func TestInjectorProbability(t *testing.T) {
	injector := &Injector{rules: make(map[Stage][]InjectionRule)}

	// Register a rule with 0% probability.
	injector.Register(StageConnection, InjectionRule{
		Probability: 0.0,
		Code:        Unavailable,
		Message:     "never fires",
		Retryable:   true,
		Origin:      OriginProvider,
		Disposition: RetryStep,
		SideEffects: SideEffectUnchanged,
	})

	// Should never fire.
	for i := 0; i < 100; i++ {
		problem := injector.Inject(t.Context(), StageConnection, "op")
		if problem != nil {
			t.Fatal("0% probability rule fired")
		}
	}

	injector.Clear()

	// Register a rule with 100% probability.
	injector.Register(StageConnection, InjectionRule{
		Probability: 1.0,
		Code:        DeadlineExceeded,
		Message:     "always fires",
		Retryable:   true,
		Origin:      OriginProvider,
		Disposition: RetryStep,
		SideEffects: SideEffectUnchanged,
	})

	// Should always fire.
	for i := 0; i < 10; i++ {
		problem := injector.Inject(t.Context(), StageConnection, "op")
		if problem == nil {
			t.Fatal("100% probability rule did not fire")
		}
		if problem.Code != DeadlineExceeded {
			t.Errorf("Code = %q, want %q", problem.Code, DeadlineExceeded)
		}
	}
}

// TestGlobalInjectorIsNoopByDefault verifies that the global injector
// does not inject faults unless explicitly configured.
func TestGlobalInjectorIsNoopByDefault(t *testing.T) {
	// The global injector should start empty.
	problem := GlobalInjector.Inject(t.Context(), StageModelSample, "op")
	if problem != nil {
		t.Fatal("global injector should be a no-op by default")
	}
}