package turnkernel

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func applyEvaluateTurnStep(
	transition *Transition,
	current State,
	command EvaluateTurnStep,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if strings.TrimSpace(command.ProgressKey) == "" {
		return illegal(current, command, "turn step progress key is empty")
	}
	transition.State.NextAction = StepActionNone
	spend := func(kind RepairKind, limit uint32, action StepAction) error {
		if limit == 0 {
			transition.State.NextAction = action
			return nil
		}
		if err := spendRepairBudget(
			transition,
			kind,
			command.ProgressKey,
			limit,
		); err != nil {
			if errors.Is(err, ErrRepairBudgetExhausted) {
				beginConvergence(transition, ConvergenceRequested{
					Cause:      ConvergenceRepairBudget,
					Used:       limit,
					Limit:      limit,
					RepairKind: kind,
				})
				return nil
			}
			return err
		}
		transition.State.NextAction = action
		return nil
	}
	switch {
	case current.Completion != nil && current.Completion.Accepted:
		if current.Policy.VerificationRequired &&
			current.MutationRevision != 0 &&
			(current.Verification.Mutation != current.MutationRevision ||
				(current.Verification.Action != VerificationActionPassed &&
					current.Verification.Action != VerificationActionReported &&
					current.Verification.Action != VerificationActionReverted)) {
			transition.State.NextAction = StepActionVerify
		} else {
			transition.State.NextAction = StepActionComplete
		}
	case current.Convergence != nil:
		if current.Convergence.FinalizationAttempted {
			transition.State.NextAction = StepActionBlock
		} else {
			transition.State.NextAction = StepActionFinalize
		}
	case current.UnresolvedToolFailure:
		if err := spend(
			RepairCompletion,
			current.Policy.CompletionRepairLimit,
			StepActionRepairToolFailure,
		); err != nil {
			return err
		}
		if current.RecoveryToolSucceeded ||
			current.Intent == protocol.TurnIntentAnswer ||
			current.Intent == protocol.TurnIntentPlan {
			transition.State.UnresolvedToolFailure = false
			transition.State.RecoveryToolSucceeded = false
		}
	case current.LastModelContinued && len(current.ClosedCalls) != 0 &&
		(current.Completion == nil || !current.Completion.Accepted):
		if err := spend(
			RepairCompletion,
			current.Policy.CompletionRepairLimit,
			StepActionRepairCompletion,
		); err != nil {
			return err
		}
	case len(current.ProvisionalOutput) == 0:
		if err := spend(
			RepairCompletion,
			current.Policy.CompletionRepairLimit,
			StepActionRepairCompletion,
		); err != nil {
			return err
		}
	case current.Intent == protocol.TurnIntentWorkspaceChange &&
		current.MutationRevision == 0:
		if err := spend(
			RepairWorkspace,
			current.Policy.WorkspaceRepairLimit,
			StepActionRepairWorkspace,
		); err != nil {
			return err
		}
	case RequiresCompletion(current) &&
		(current.Completion == nil || !current.Completion.Accepted):
		if err := spend(
			RepairDeclaration,
			current.Policy.DeclarationRepairLimit,
			StepActionRepairDeclaration,
		); err != nil {
			return err
		}
	case current.Policy.VerificationRequired &&
		current.MutationRevision != 0 &&
		(current.Verification.Mutation != current.MutationRevision ||
			(current.Verification.Action != VerificationActionPassed &&
				current.Verification.Action != VerificationActionReported &&
				current.Verification.Action != VerificationActionReverted)):
		transition.State.NextAction = StepActionVerify
	default:
		transition.State.NextAction = StepActionComplete
	}
	return nil
}

func completionRejectionAction(reason string) string {
	switch reason {
	case "no_observed_changes":
		return "perform_workspace_mutation"
	case "quality_verification_required":
		return "run_quality_verification"
	case "pending_actions":
		return "continue_work"
	case "convergence_blocked":
		return "finalize_blocked"
	default:
		return "correct_and_retry_turn_complete"
	}
}

func requirePhase(state State, command Command, allowed ...Phase) error {
	if slices.Contains(allowed, state.Phase) {
		return nil
	}
	return illegal(
		state,
		command,
		fmt.Sprintf("expected phase %v", allowed),
	)
}

func move(transition *Transition, phase Phase) {
	from := transition.State.Phase
	transition.State.Phase = phase
	transition.Events = append(transition.Events, Event{
		Kind: EventTransition, From: from, To: phase,
	})
}

func illegal(state State, command Command, reason string) error {
	name := "<nil>"
	if command != nil {
		name = command.commandName()
	}
	return &TransitionError{Phase: state.Phase, Command: name, Reason: reason}
}
