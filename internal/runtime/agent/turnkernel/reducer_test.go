package turnkernel

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestNonInteractiveReadOnlyResearchCompletesWithoutDeclaration(t *testing.T) {
	for _, intent := range []protocol.TurnIntent{
		protocol.TurnIntentAnswer,
		protocol.TurnIntentPlan,
	} {
		t.Run(string(intent), func(t *testing.T) {
			state := startSampling(t, intent)
			state = apply(t, state, ModelTextReceived{
				Text: "analysis complete",
			}).State
			released := apply(t, state, ReleaseProvisionalOutput{})
			if len(released.Effects) != 0 ||
				!released.State.OutputEligibility ||
				len(released.State.ProvisionalOutput) != 1 {
				t.Fatalf("release effects = %+v", released.Effects)
			}
			state = released.State

			prepared := apply(t, state, TerminalRequested{})
			if prepared.State.Phase != PhaseCommitting ||
				len(prepared.Effects) != 0 {
				t.Fatalf("prepared = %+v", prepared)
			}
			finished := apply(t, prepared.State, FinishTerminal{})
			if finished.State.Phase != PhaseCompleted ||
				finished.State.Terminal == nil ||
				len(finished.Effects) != 0 {
				t.Fatalf("finished = %+v", finished)
			}
			if len(finished.State.ProvisionalOutput) != 0 ||
				!finished.State.OutputEligibility ||
				len(finished.State.FinalOutput) != 1 {
				t.Fatalf("output state = %+v", finished.State)
			}
		})
	}
}

func TestStructuredInteractiveTurnRequiresDeclarationAndUsesItsSummary(
	t *testing.T,
) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state.Policy.StructuredTerminalRequired = true
	state = apply(t, state, ModelTextReceived{
		Text: "I will continue investigating.",
	}).State
	repair := apply(t, state, EvaluateTurnStep{
		ProgressKey: "sample=1",
	})
	if repair.State.NextAction != StepActionRepairDeclaration {
		t.Fatalf("ordinary text action = %q", repair.State.NextAction)
	}
	state = apply(t, repair.State, CompletionEvaluated{
		Candidate: CompletionCandidate{
			DeclarationValid: true,
			Status:           "complete",
			Summary:          "The investigation is complete.",
			CompletionCall:   "complete-1",
			BatchSize:        1,
		},
	}).State
	if state.Completion == nil || !state.Completion.Accepted ||
		len(state.ProvisionalOutput) != 1 ||
		state.ProvisionalOutput[0] != "The investigation is complete." {
		t.Fatalf("completion output = %+v", state)
	}
	completed := apply(t, state, EvaluateTurnStep{
		ProgressKey: "sample=2",
	})
	if completed.State.NextAction != StepActionComplete {
		t.Fatalf("declared action = %q", completed.State.NextAction)
	}
}

func TestCompletionRequirementUsesMutationIntentAndOperationFacts(t *testing.T) {
	answer := startSampling(t, protocol.TurnIntentAnswer)
	answer.ClosedCalls["read"] = ToolResultState{ID: "read", Name: "file_read"}
	if RequiresCompletion(answer) {
		t.Fatal("read-only answer requires completion")
	}
	answer.Policy.StructuredTerminalRequired = true
	if !RequiresCompletion(answer) {
		t.Fatal("structured interactive answer does not require completion")
	}
	answer.Policy.StructuredTerminalRequired = false
	answer.ClosedCalls["read"] = ToolResultState{ID: "read", Name: "file_read"}
	answer.MutationRevision = 1
	if !RequiresCompletion(answer) {
		t.Fatal("observed mutation does not require completion")
	}
	operation := startSampling(t, protocol.TurnIntentOperation)
	operation.ClosedCalls["deploy"] = ToolResultState{
		ID: "deploy", Name: "deploy",
	}
	if !RequiresCompletion(operation) {
		t.Fatal("successful operation does not require completion")
	}
	operation.ClosedCalls["deploy"] = ToolResultState{
		ID: "deploy", Name: "deploy", IsError: true,
	}
	if RequiresCompletion(operation) {
		t.Fatal("failed operation was treated as completed")
	}
	workspaceChange := startSampling(t, protocol.TurnIntentWorkspaceChange)
	if !RequiresCompletion(workspaceChange) {
		t.Fatal("workspace change intent does not require completion")
	}
	workspaceChange.Policy.CompletionRequired = false
	if RequiresCompletion(workspaceChange) {
		t.Fatal("disabled completion policy still requires completion")
	}
}

func TestCanceledApprovalAcceptsLateToolResult(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentWorkspaceChange)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-edit", Name: "file_edit"}},
	}).State
	state = apply(t, state, ApprovalRequired{
		RequestID: "approval-edit", CallID: "call-edit",
	}).State
	state = apply(t, state, CancelRequested{
		Reason: protocol.CancelReasonHostInterrupted,
	}).State
	closed := apply(t, state, ToolResultReceived{
		CallID: "call-edit", IsError: true,
	})
	if closed.State.Phase != PhaseSampling ||
		len(closed.State.OpenCalls) != 0 ||
		len(closed.State.PendingApprovals) != 0 ||
		len(closed.State.PendingEffects) != 0 ||
		len(closed.State.ClosedCalls) != 1 {
		t.Fatalf("late canceled result = %+v", closed.State)
	}
	terminal := apply(t, closed.State, TerminalRequested{
		CancelReason: protocol.CancelReasonHostInterrupted,
	})
	terminal = apply(t, terminal.State, FinishTerminal{})
	if terminal.State.Phase != PhaseCanceled ||
		terminal.State.Terminal == nil ||
		terminal.State.Terminal.Kind != TerminalCanceled {
		t.Fatalf("terminal = %+v", terminal.State)
	}
}

func TestRepairBudgetResetsOnlyOnStructuredProgress(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	for range 2 {
		state = apply(t, state, RepairRequested{
			Kind: RepairCompletion, ProgressKey: "mutation=0;tools=0", Limit: 2,
		}).State
	}
	before := state
	_, err := (Reducer{}).Apply(state, RepairRequested{
		Kind: RepairCompletion, ProgressKey: "mutation=0;tools=0", Limit: 2,
	})
	if !errors.Is(err, ErrRepairBudgetExhausted) {
		t.Fatalf("third repair error = %v", err)
	}
	if got, want := before.RepairBudgets[RepairCompletion], state.RepairBudgets[RepairCompletion]; got != want {
		t.Fatalf("rejected repair changed budget: before=%+v after=%+v", got, want)
	}
	state = apply(t, state, RepairRequested{
		Kind: RepairCompletion, ProgressKey: "mutation=0;tools=1", Limit: 2,
	}).State
	budget := state.RepairBudgets[RepairCompletion]
	if budget.Consecutive != 1 || budget.Steps != 3 {
		t.Fatalf("progressed repair budget = %+v", budget)
	}
}

func TestMutationOutputCannotReleaseBeforeCompletionAndVerification(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-1", Name: "file_write"}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID:  "write-1",
		Changes: []ObservedChange{{Path: "a.go", Kind: "modified"}},
	}).State
	state = apply(t, state, ModelTextReceived{Text: "premature"}).State
	_, err := (Reducer{}).Apply(state, ReleaseProvisionalOutput{})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("release error = %v", err)
	}
}

func TestWorkspaceChangeWithoutMutationFailsClosed(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentWorkspaceChange)
	_, err := (Reducer{}).Apply(state, TerminalRequested{})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("error = %v, want illegal transition", err)
	}
}

func TestMutationCompletesThroughDeclarationVerificationAndJournal(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-1", Name: "file_write"}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID:  "write-1",
		Changes: []ObservedChange{{Path: "a.go", Kind: "modified"}},
	}).State
	if state.MutationRevision != 1 || state.Journal != JournalOpen {
		t.Fatalf("mutation state = %+v", state)
	}
	state = apply(t, state, CompletionEvaluated{
		Candidate: CompletionCandidate{
			DeclarationValid: true,
			Status:           "complete",
			Summary:          "implemented",
			CompletionCall:   "complete-1",
			BatchSize:        1,
		},
	}).State
	state = apply(t, state, VerificationStarted{}).State
	state = apply(t, state, VerificationFinished{
		Status: VerificationPassed, EvidenceCalls: []string{"verify-1"},
	}).State
	state = apply(t, state, ModelTextReceived{Text: "done"}).State
	state = apply(t, state, ReleaseProvisionalOutput{}).State

	prepared := apply(t, state, TerminalRequested{})
	if len(prepared.Effects) != 1 ||
		prepared.Effects[0].Kind != EffectCommitJournal {
		t.Fatalf("terminal effects = %+v", prepared.Effects)
	}
	effect := prepared.Effects[0]
	state = apply(t, prepared.State, EffectStarted{
		EffectID: effect.ID,
		Attempt:  1,
	}).State
	state = apply(t, state, JournalResultReceived{
		EffectID: effect.ID,
		Status:   JournalCommitted,
	}).State
	state = apply(t, state, FinishTerminal{}).State
	if state.Phase != PhaseCompleted ||
		state.Journal != JournalCommitted ||
		state.Terminal == nil {
		t.Fatalf("terminal state = %+v", state)
	}
}

func TestReducerOwnsCompletionAcceptanceAndRuntimeBindings(t *testing.T) {
	mutated := startSampling(t, protocol.TurnIntentAnswer)
	mutated = apply(t, mutated, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-1", Name: "file_write"}},
	}).State
	mutated = apply(t, mutated, ToolResultReceived{
		CallID:  "write-1",
		Changes: []ObservedChange{{Path: "a.go", Kind: "modified"}},
	}).State
	base := CompletionCandidate{
		DeclarationValid: true,
		Status:           "complete",
		Summary:          "implemented",
		CompletionCall:   "complete-1",
		BatchSize:        1,
	}
	tests := []struct {
		name      string
		state     State
		candidate CompletionCandidate
		accepted  bool
		reason    string
		action    string
	}{
		{
			name:      "accepted",
			state:     mutated,
			candidate: base,
			accepted:  true,
			action:    "await_runtime_verification",
		},
		{
			name:  "same batch mutation",
			state: mutated,
			candidate: func() CompletionCandidate {
				value := base
				value.BatchMutated = true
				return value
			}(),
			reason: "same_batch_mutation",
		},
		{
			name:  "quality evidence required",
			state: mutated,
			candidate: func() CompletionCandidate {
				value := base
				value.QualityRequired = true
				return value
			}(),
			reason: "quality_verification_required",
		},
		{
			name:      "read only answer",
			state:     startSampling(t, protocol.TurnIntentAnswer),
			candidate: base,
			accepted:  true,
			action:    "final_answer",
		},
		{
			name:      "workspace change without mutation",
			state:     startSampling(t, protocol.TurnIntentWorkspaceChange),
			candidate: base,
			reason:    "no_observed_changes",
			action:    "perform_workspace_mutation",
		},
		{
			name:  "incomplete work continues current turn",
			state: startSampling(t, protocol.TurnIntentAnswer),
			candidate: CompletionCandidate{
				DeclarationValid: true,
				Status:           "incomplete",
				Summary:          "workspace edits remain",
				PendingActions:   []string{"apply the workspace edits"},
				CompletionCall:   "complete-incomplete",
				BatchSize:        1,
			},
			reason: "pending_actions",
			action: "continue_work",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			state := apply(t, testCase.state, CompletionEvaluated{
				Candidate: testCase.candidate,
			}).State
			if state.Completion == nil ||
				state.Completion.Accepted != testCase.accepted ||
				state.Completion.Reason != testCase.reason {
				t.Fatalf("completion decision = %+v", state.Completion)
			}
			if testCase.action != "" &&
				state.Completion.RequiredAction != testCase.action {
				t.Fatalf("required action = %q, want %q",
					state.Completion.RequiredAction, testCase.action)
			}
			if testCase.accepted &&
				(state.Completion.Mutation != testCase.state.MutationRevision ||
					!samePaths(
						state.Completion.ChangedPaths,
						changedPaths(testCase.state.Changes),
					) ||
					state.Completion.CompletionCall != "complete-1") {
				t.Fatalf("runtime bindings = %+v", state.Completion)
			}
		})
	}
}

func TestMutationInvalidatesCompletionAndVerification(t *testing.T) {
	state := verifiedMutation(t)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-2", Name: "file_write"}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID:  "write-2",
		Changes: []ObservedChange{{Path: "b.go", Kind: "created"}},
	}).State
	if state.MutationRevision != 2 ||
		state.Completion != nil ||
		state.Verification.Status != VerificationNotEvaluated {
		t.Fatalf("state after later mutation = %+v", state)
	}
	_, err := (Reducer{}).Apply(state, TerminalRequested{})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("error = %v, want illegal transition", err)
	}
}

func TestToolAssistedReadOnlyTurnCompletesWithoutDeclaration(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "read-1", Name: "file_read"}},
	}).State
	state = apply(t, state, ToolResultReceived{CallID: "read-1"}).State
	state.ProvisionalOutput = []string{"The review is complete."}

	transition := apply(t, state, EvaluateTurnStep{ProgressKey: "read-only"})
	if transition.State.NextAction != StepActionComplete {
		t.Fatalf("next action = %q, want %q",
			transition.State.NextAction, StepActionComplete)
	}
}

func TestAcceptedCompletionOutranksProviderContinuation(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-1", Name: "file_write"}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID:  "write-1",
		Changes: []ObservedChange{{Path: "a.go", Kind: "modified"}},
	}).State
	state = apply(t, state, CompletionEvaluated{
		Candidate: CompletionCandidate{
			DeclarationValid: true,
			Status:           "complete",
			Summary:          "implemented",
			CompletionCall:   "complete-1",
			BatchSize:        1,
		},
	}).State
	state.LastModelContinued = true

	transition := apply(t, state, EvaluateTurnStep{ProgressKey: "accepted"})
	if transition.State.NextAction != StepActionVerify {
		t.Fatalf("next action = %q, want %q",
			transition.State.NextAction, StepActionVerify)
	}
	if transition.State.RepairBudgets[RepairCompletion].Steps != 0 {
		t.Fatalf("accepted completion spent repair budget: %+v",
			transition.State.RepairBudgets[RepairCompletion])
	}
}

func TestObserveProgressUsesConservativeDurableThresholds(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentWorkspaceChange)
	state = apply(t, state, ObserveProgress{
		Signature: "mutation=0;plan_done=0",
	}).State

	for _, test := range []struct {
		samples uint32
		want    ProgressStage
	}{
		{samples: 8, want: ProgressStageNone},
		{samples: 16, want: ProgressStageConverge},
		{samples: 24, want: ProgressStageConverge},
		{samples: 32, want: ProgressStageFinishOnly},
		{samples: 48, want: ProgressStageExhausted},
	} {
		state = apply(t, state, ObserveProgress{
			Signature:        "mutation=0;plan_done=0",
			CompletedSamples: test.samples,
		}).State
		if state.Progress.Stage != test.want ||
			state.Progress.NoProgressSamples != test.samples {
			t.Fatalf(
				"samples=%d progress=%+v, want stage=%s",
				test.samples,
				state.Progress,
				test.want,
			)
		}
	}

	state = apply(t, state, ObserveProgress{
		Signature:        "mutation=1;plan_done=0",
		CompletedSamples: 56,
	}).State
	if state.Progress.Stage != ProgressStageNone ||
		state.Progress.NoProgressSamples != 0 {
		t.Fatalf("progress did not reset: %+v", state.Progress)
	}
}

func TestReadOnlyProgressHasBoundedTotalSampleStages(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	for _, test := range []struct {
		samples uint32
		want    ProgressStage
	}{
		{samples: 7, want: ProgressStageNone},
		{samples: 8, want: ProgressStageConverge},
		{samples: 12, want: ProgressStageFinishOnly},
		{samples: 16, want: ProgressStageExhausted},
	} {
		state = apply(t, state, ObserveProgress{
			Signature:        "evidence-changed-" + string(test.want),
			CompletedSamples: test.samples,
		}).State
		if state.Progress.Stage != test.want {
			t.Fatalf("samples=%d stage=%q, want %q",
				test.samples, state.Progress.Stage, test.want)
		}
	}
}

func TestResearchProgressLeavesTotalSampleCapAfterMutation(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ObserveProgress{
		Signature:        "mutation=0",
		CompletedSamples: researchFinishOnlySamples,
	}).State
	if state.Progress.Stage != ProgressStageFinishOnly {
		t.Fatalf("pre-mutation progress = %+v", state.Progress)
	}

	proposed := apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-1", Name: "file_write"}},
	})
	effect := proposed.Effects[0]
	state = apply(t, proposed.State, EffectStarted{
		EffectID: effect.ID, Attempt: 1,
	}).State
	state = apply(t, state, ToolResultReceived{
		EffectID: effect.ID,
		CallID:   "write-1",
		Changes:  []ObservedChange{{Path: "mcp.json", Kind: "created"}},
	}).State
	state = apply(t, state, ObserveProgress{
		Signature:        "mutation=1",
		CompletedSamples: researchExhaustedSamples,
	}).State
	if state.Progress.Stage != ProgressStageNone ||
		state.Progress.NoProgressSamples != 0 {
		t.Fatalf("post-mutation progress = %+v", state.Progress)
	}
}

func TestToolAndApprovalLifecycleIsClosedExactlyOnce(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	started := apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "shell_read"}},
	})
	if len(started.Effects) != 1 ||
		started.Effects[0].Kind != EffectExecuteTool {
		t.Fatalf("effects = %+v", started.Effects)
	}
	state = apply(t, started.State, ApprovalRequired{
		RequestID: "approval-1", CallID: "call-1",
	}).State
	state = apply(t, state, ApprovalResolved{
		RequestID: "approval-1",
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID: "call-1",
	}).State
	if len(state.OpenCalls) != 0 || len(state.ClosedCalls) != 1 {
		t.Fatalf("tool ledgers = open:%v closed:%v", state.OpenCalls, state.ClosedCalls)
	}
	_, err := (Reducer{}).Apply(state, ToolResultReceived{CallID: "call-1"})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("duplicate result error = %v", err)
	}
}

func TestParallelApprovalsRemainPendingIndependently(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{
			{ID: "call-1", Name: "first"},
			{ID: "call-2", Name: "second"},
		},
	}).State
	state = apply(t, state, ApprovalRequired{
		RequestID: "approval-1", CallID: "call-1",
	}).State
	state = apply(t, state, ApprovalRequired{
		RequestID: "approval-2", CallID: "call-2",
	}).State
	state = apply(t, state, ApprovalResolved{
		RequestID: "approval-1",
	}).State
	if state.Phase != PhaseAwaitingApproval ||
		len(state.PendingApprovals) != 1 {
		t.Fatalf("state after first approval = %+v", state)
	}
	state = apply(t, state, ApprovalResolved{
		RequestID: "approval-2",
	}).State
	if state.Phase != PhaseExecutingTools ||
		len(state.PendingApprovals) != 0 {
		t.Fatalf("state after all approvals = %+v", state)
	}
}

func TestAbortOpenCallsClosesBatchBeforeCancellation(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{
			{ID: "call-b", Name: "second"},
			{ID: "call-a", Name: "first"},
		},
	}).State
	state = apply(t, state, ApprovalRequired{
		RequestID: "approval-1", CallID: "call-a",
	}).State
	aborted := apply(t, state, AbortOpenCalls{Reason: "turn canceled"})
	if aborted.State.Phase != PhaseSampling ||
		len(aborted.State.PendingApprovals) != 0 ||
		len(aborted.State.OpenCalls) != 0 ||
		len(aborted.State.ClosedCalls) != 2 {
		t.Fatalf("aborted = %+v", aborted.State)
	}
	for _, result := range aborted.State.ClosedCalls {
		if !result.IsError {
			t.Fatalf("aborted result = %+v", result)
		}
	}
	state = apply(t, aborted.State, TerminalRequested{CancelReason: "turn canceled"}).State
	state = apply(t, state, FinishTerminal{}).State
	if state.Phase != PhaseCanceled {
		t.Fatalf("state = %+v", state)
	}
}

func TestInputLifecycleMustResolveBeforeTerminal(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, InputRequired{RequestID: "input-1"}).State
	_, err := (Reducer{}).Apply(state, TerminalRequested{CancelReason: "turn canceled"})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("pending-input terminal error = %v", err)
	}
	state = apply(t, state, InputResolved{RequestID: "input-1"}).State
	state = apply(t, state, TerminalRequested{CancelReason: "turn canceled"}).State
	state = apply(t, state, FinishTerminal{}).State
	if state.Phase != PhaseCanceled {
		t.Fatalf("state = %+v", state)
	}
}

func TestFailureRequiresToolClosureAndRollsBackMutation(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-1", Name: "file_write"}},
	}).State
	_, err := (Reducer{}).Apply(state, TerminalRequested{FailureMessage: "tool failed"})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("open-call failure error = %v", err)
	}
	state = apply(t, state, ToolResultReceived{
		CallID: "write-1", IsError: true,
		Changes: []ObservedChange{{Path: "partial.txt", Kind: "created"}},
	}).State
	prepared := apply(t, state, TerminalRequested{FailureMessage: "tool failed"})
	if len(prepared.Effects) != 1 ||
		prepared.Effects[0].Kind != EffectRollbackJournal {
		t.Fatalf("effects = %+v", prepared.Effects)
	}
	state = apply(t, prepared.State, JournalFinalized{
		Status: JournalRolledBack,
	}).State
	state = apply(t, state, FinishTerminal{}).State
	if state.Phase != PhaseFailed || state.Journal != JournalRolledBack {
		t.Fatalf("state = %+v", state)
	}
}

func TestTerminalStateHasNoOutgoingTransition(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, TerminalRequested{CancelReason: "user canceled"}).State
	state = apply(t, state, FinishTerminal{}).State
	_, err := (Reducer{}).Apply(state, ModelTextReceived{Text: "late"})
	if !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("error = %v, want illegal transition", err)
	}
}

func TestApplyDoesNotMutateInputAndDigestIsDeterministic(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	before, err := Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	transition := apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{
			{ID: "b", Name: "second"},
			{ID: "a", Name: "first"},
		},
	})
	after, err := Digest(state)
	if err != nil {
		t.Fatal(err)
	}
	if before != after || len(state.OpenCalls) != 0 {
		t.Fatalf("input mutated: before=%s after=%s state=%+v", before, after, state)
	}
	reordered := cloneState(transition.State)
	reordered.OpenCalls = map[string]ToolCallState{
		"a": transition.State.OpenCalls["a"],
		"b": transition.State.OpenCalls["b"],
	}
	left, err := Digest(transition.State)
	if err != nil {
		t.Fatal(err)
	}
	right, err := Digest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatalf("digest depends on map insertion order: %s != %s", left, right)
	}
}

func TestPhase4R1EffectLifecycleHasStableIdentityAndClosure(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	command := ToolCallsProposed{Calls: []ToolCallState{
		{ID: "call-1", Name: "first"},
		{ID: "call-2", Name: "second"},
	}}
	left := apply(t, state, command)
	right := apply(t, state, command)
	if len(left.Effects) != 2 ||
		!reflect.DeepEqual(left.Effects[0], right.Effects[0]) ||
		!reflect.DeepEqual(left.Effects[1], right.Effects[1]) {
		t.Fatalf("effect identity is not deterministic: left=%+v right=%+v", left.Effects, right.Effects)
	}
	for index, effect := range left.Effects {
		if effect.ID == "" ||
			effect.Kind != EffectExecuteTool ||
			effect.PayloadDigest == "" ||
			effect.IdempotencyKey == "" ||
			effect.Status != EffectRequested ||
			effect.Attempt != 0 {
			t.Fatalf("effect[%d] = %+v", index, effect)
		}
	}
	state = left.State
	firstID := left.Effects[0].ID
	state = apply(t, state, EffectStarted{
		EffectID: firstID,
		Attempt:  1,
	}).State
	state = apply(t, state, ToolResultReceived{
		EffectID: firstID,
		CallID:   "call-1",
	}).State
	state = apply(t, state, AbortOpenCalls{Reason: "stop batch"}).State
	if len(state.PendingEffects) != 0 ||
		len(state.CompletedEffects) != 2 ||
		state.CompletedEffects[firstID].Attempt != 1 ||
		state.CompletedEffects[firstID].Status != EffectSucceeded {
		t.Fatalf("effect ledgers = pending:%+v completed:%+v", state.PendingEffects, state.CompletedEffects)
	}
}

func TestPhase4R1SampleUsageContextAndCancelAreStructured(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ModelSampleRequested{SampleID: "sample-1"}).State
	effectID := pendingEffectID(state, EffectSampleProvider, "sample-1")
	state = apply(t, state, EffectStarted{
		EffectID: effectID, Attempt: 1,
	}).State
	state = apply(t, state, ProviderRetryRequested{
		EffectID: effectID, SampleID: "sample-1",
		Attempt: 1, Retry: 1,
		Failure: provider.Failure{
			Code: provider.FailureStreamClosed, Message: "unexpected eof",
		},
		RetryAt: time.Now(), PolicyRevision: "test/v1",
	}).State
	state = apply(t, state, EffectStarted{
		EffectID: effectID, Attempt: 2,
	}).State
	state = apply(t, state, ModelSampleResultReceived{
		EffectID: effectID, SampleID: "sample-1",
		Usage: UsageState{
			InputTokens: 10, OutputTokens: 3, CostKnown: true,
		},
		Context: ContextState{
			Digest: "sha256:context", HistoryBytes: 128, MaxBytes: 1024,
		},
	}).State
	sample := state.SampleLedger["sample-1"]
	if sample.Status != SampleCompleted ||
		sample.ProviderRetries != 1 ||
		state.Usage.InputTokens != 10 ||
		state.Context.Digest != "sha256:context" {
		t.Fatalf("sample state = %+v usage=%+v context=%+v", sample, state.Usage, state.Context)
	}
	state = apply(t, state, CancelRequested{Reason: "user interrupted"}).State
	if !state.Cancellation.Accepted {
		t.Fatalf("cancellation = %+v", state.Cancellation)
	}
	if _, err := (Reducer{}).Apply(state, ModelSampleStarted{
		SampleID: "sample-2",
		Attempt:  1,
	}); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("post-cancel sample error = %v", err)
	}
	if _, err := (Reducer{}).Apply(state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "tool"}},
	}); !errors.Is(err, ErrIllegalTransition) {
		t.Fatalf("post-cancel tool error = %v", err)
	}
}

func TestSO4SupplementalUsageJoinsFrozenKernelAuthority(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, SupplementalUsageRecorded{
		Source: "vision", SampleID: "tool-sample-1",
		Usage: UsageState{
			InputTokens:    1500,
			CostMicrounits: 20,
			CostKnown:      true,
		},
	}).State
	state = apply(t, state, SupplementalUsageRecorded{
		Source: "sub_query", SampleID: "tool-sample-2",
		Usage: UsageState{
			InputTokens: 20,
			CostKnown:   false,
		},
	}).State
	if state.Usage.Calls != 2 ||
		state.Usage.InputTokens != 1520 ||
		state.Usage.CostMicrounits != 20 ||
		state.Usage.CostKnown {
		t.Fatalf("usage = %+v", state.Usage)
	}
	state = apply(t, state, ModelTextReceived{Text: "done"}).State
	state = apply(t, state, ReleaseProvisionalOutput{}).State
	state = apply(t, state, TerminalRequested{}).State
	if !state.Usage.Frozen {
		t.Fatal("terminal request did not freeze supplemental usage")
	}
	if _, err := (Reducer{}).Apply(state, SupplementalUsageRecorded{
		Source: "late", SampleID: "late",
	}); err == nil {
		t.Fatal("late usage changed frozen terminal state")
	}
}

func TestPermanentProviderFailureIsPersistedWithoutRetry(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ModelSampleRequested{SampleID: "sample-auth"}).State
	effectID := pendingEffectID(state, EffectSampleProvider, "sample-auth")
	state = apply(t, state, EffectStarted{
		EffectID: effectID, Attempt: 1,
	}).State
	failure := &provider.Failure{
		Code: provider.FailureAuth, Message: "invalid credential",
		HTTPStatus: 401, RequestID: "request-1",
	}
	state = apply(t, state, ModelSampleResultReceived{
		EffectID: effectID, SampleID: "sample-auth",
		Error: failure.Message, Failure: failure,
	}).State
	sample := state.SampleLedger["sample-auth"]
	if sample.Status != SampleFailed ||
		sample.ProviderRetries != 0 ||
		sample.LastFailure == nil ||
		sample.LastFailure.Code != provider.FailureAuth ||
		sample.LastFailure.HTTPStatus != 401 ||
		sample.LastFailure.RequestID != "request-1" {
		t.Fatalf("sample = %+v", sample)
	}
}

func TestPhase4R1RecoveryBindsCurrentProfileRevision(t *testing.T) {
	state := NewState(protocol.TurnIntentWorkspaceChange, "act", 2)
	state = apply(t, state, RecoveryRequested{
		SourceTurnID:           "source-turn",
		RecoveryTurnID:         "recovery-turn",
		CurrentProfileRevision: 9,
		Action:                 string(protocol.TurnRecoveryContinue),
	}).State
	if state.ProfileRevision != 9 ||
		state.RecoveryRelation == nil ||
		state.RecoveryRelation.SourceTurnID != "source-turn" ||
		state.RecoveryRelation.RecoveryTurnID != "recovery-turn" {
		t.Fatalf("recovery state = %+v", state)
	}
}

func TestPhase4R1ApprovalAndInputResolveThroughEffectResults(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	started := apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "call-1", Name: "write"}},
	})
	state = apply(t, started.State, ApprovalRequired{
		RequestID: "approval-1",
		CallID:    "call-1",
	}).State
	approvalEffect := effectIDForKind(t, state, EffectAwaitApproval)
	state = apply(t, state, ApprovalResultReceived{
		EffectID:  approvalEffect,
		RequestID: "approval-1",
		Accepted:  true,
	}).State
	_, approvalPending := state.PendingEffects[approvalEffect]
	if state.Phase != PhaseExecutingTools ||
		approvalPending ||
		state.CompletedEffects[approvalEffect].Status != EffectSucceeded {
		t.Fatalf("approval result state = %+v", state)
	}
	state = apply(t, state, ToolResultReceived{CallID: "call-1"}).State
	state = apply(t, state, InputRequired{RequestID: "input-1"}).State
	inputEffect := effectIDForKind(t, state, EffectAwaitInput)
	state = apply(t, state, InputResultReceived{
		EffectID:  inputEffect,
		RequestID: "input-1",
		Accepted:  true,
	}).State
	if state.Phase != PhaseSampling ||
		state.PendingInput != nil ||
		state.CompletedEffects[inputEffect].Status != EffectSucceeded {
		t.Fatalf("input result state = %+v", state)
	}
}

func TestPhase4R1OutputRemainsProvisionalUntilTerminalCommit(t *testing.T) {
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ModelTextReceived{Text: "final answer"}).State
	state = apply(t, state, ReleaseProvisionalOutput{}).State
	if !state.OutputEligibility ||
		len(state.ProvisionalOutput) != 1 ||
		len(state.FinalOutput) != 0 {
		t.Fatalf("eligible output = %+v", state)
	}
	state = apply(t, state, TerminalRequested{}).State
	if len(state.ProvisionalOutput) != 1 ||
		!state.Usage.Frozen ||
		!state.Context.Frozen {
		t.Fatalf("committing output = %+v", state)
	}
	state = apply(t, state, FinishTerminal{}).State
	if len(state.ProvisionalOutput) != 0 ||
		len(state.FinalOutput) != 1 ||
		state.FinalOutput[0] != "final answer" {
		t.Fatalf("terminal output = %+v", state)
	}
}

func effectIDForKind(t *testing.T, state State, kind EffectKind) string {
	t.Helper()
	for effectID, effect := range state.PendingEffects {
		if effect.Kind == kind {
			return effectID
		}
	}
	t.Fatalf("pending effect %s not found: %+v", kind, state.PendingEffects)
	return ""
}

func TestValidateRejectsForgedReplayState(t *testing.T) {
	testCases := []struct {
		name   string
		mutate func(*State)
	}{
		{
			name: "open call outside tool phase",
			mutate: func(state *State) {
				state.OpenCalls["call-1"] = ToolCallState{
					ID: "call-1", Name: "fixture",
				}
			},
		},
		{
			name: "completion paths disagree",
			mutate: func(state *State) {
				*state = verifiedMutation(t)
				state.Completion.ChangedPaths = []string{"forged.go"}
			},
		},
		{
			name: "completed mutation without journal",
			mutate: func(state *State) {
				*state = verifiedMutation(t)
				state.Phase = PhaseCompleted
				state.Terminal = &TerminalDecision{Kind: TerminalCompleted}
				state.Journal = JournalOpen
			},
		},
		{
			name: "terminal phase disagrees",
			mutate: func(state *State) {
				state.Phase = PhaseFailed
				state.Terminal = &TerminalDecision{Kind: TerminalCanceled, Message: "canceled"}
			},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			state := startSampling(t, protocol.TurnIntentAnswer)
			testCase.mutate(&state)
			if err := Validate(state); err == nil {
				t.Fatalf("forged state was accepted: %+v", state)
			}
			if _, err := Digest(state); err == nil {
				t.Fatal("forged state received a digest")
			}
		})
	}
}

func verifiedMutation(t *testing.T) State {
	t.Helper()
	state := startSampling(t, protocol.TurnIntentAnswer)
	state = apply(t, state, ToolCallsProposed{
		Calls: []ToolCallState{{ID: "write-1", Name: "file_write"}},
	}).State
	state = apply(t, state, ToolResultReceived{
		CallID:  "write-1",
		Changes: []ObservedChange{{Path: "a.go", Kind: "modified"}},
	}).State
	state = apply(t, state, CompletionEvaluated{
		Candidate: CompletionCandidate{
			DeclarationValid: true,
			Status:           "complete",
			Summary:          "implemented",
			CompletionCall:   "complete-1",
			BatchSize:        1,
		},
	}).State
	state = apply(t, state, VerificationStarted{}).State
	return apply(t, state, VerificationFinished{
		Status: VerificationPassed, EvidenceCalls: []string{"verify-1"},
	}).State
}

func startSampling(t *testing.T, intent protocol.TurnIntent) State {
	t.Helper()
	state := NewState(intent, "act", 1)
	state = apply(t, state, StartTurn{}).State
	return apply(t, state, PreparationFinished{}).State
}

func apply(t *testing.T, state State, command Command) Transition {
	t.Helper()
	switch value := command.(type) {
	case ToolResultReceived:
		if value.EffectID == "" {
			value.EffectID = pendingEffectID(
				state,
				EffectExecuteTool,
				value.CallID,
			)
			command = value
		}
		state = startPendingEffect(t, state, value.EffectID)
	case ApprovalResultReceived:
		state = startPendingEffect(t, state, value.EffectID)
	case InputResultReceived:
		state = startPendingEffect(t, state, value.EffectID)
	case VerificationFinished:
		if value.EffectID == "" {
			value.EffectID = pendingEffectID(
				state,
				EffectRunVerification,
				"",
			)
			command = value
		}
		state = startPendingEffect(t, state, value.EffectID)
	}
	transition, err := (Reducer{}).Apply(state, command)
	if err != nil {
		t.Fatalf("Apply(%T): %v", command, err)
	}
	return transition
}

func pendingEffectID(
	state State,
	kind EffectKind,
	callID string,
) string {
	for _, effectID := range sortedEffectIDs(state.PendingEffects) {
		effect := state.PendingEffects[effectID]
		if effect.Kind == kind &&
			(callID == "" || effect.CallID == callID) {
			return effectID
		}
	}
	return ""
}

func startPendingEffect(t *testing.T, state State, effectID string) State {
	t.Helper()
	effect, ok := state.PendingEffects[effectID]
	if !ok || effect.Status != EffectRequested {
		return state
	}
	transition, err := (Reducer{}).Apply(state, EffectStarted{
		EffectID: effectID,
		Attempt:  effect.Attempt + 1,
	})
	if err != nil {
		t.Fatalf("Apply(EffectStarted): %v", err)
	}
	return transition.State
}
