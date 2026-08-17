package turnkernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

var (
	ErrIllegalTransition     = errors.New("illegal turn transition")
	ErrRepairBudgetExhausted = errors.New("repair budget exhausted")
)

type RepairBudgetExhaustedError struct {
	Kind        RepairKind
	ProgressKey string
	Limit       uint32
}

func (e *RepairBudgetExhaustedError) Error() string {
	return fmt.Sprintf(
		"%s: kind=%s progress_key=%q limit=%d",
		ErrRepairBudgetExhausted,
		e.Kind,
		e.ProgressKey,
		e.Limit,
	)
}

func (e *RepairBudgetExhaustedError) Unwrap() error {
	return ErrRepairBudgetExhausted
}

type TransitionError struct {
	Phase   Phase
	Command string
	Reason  string
}

func (e *TransitionError) Error() string {
	return fmt.Sprintf(
		"%s: phase=%s command=%s: %s",
		ErrIllegalTransition,
		e.Phase,
		e.Command,
		e.Reason,
	)
}

func (e *TransitionError) Unwrap() error { return ErrIllegalTransition }

type Reducer struct{}

func (Reducer) Apply(current State, command Command) (Transition, error) {
	if command == nil {
		return Transition{}, illegal(current, nil, "command is nil")
	}
	if err := Validate(current); err != nil {
		return Transition{}, fmt.Errorf("invalid current turn state: %w", err)
	}
	if current.Phase.Terminal() {
		return Transition{}, illegal(
			current,
			command,
			"terminal states have no outgoing transitions",
		)
	}

	next := cloneState(current)
	transition := Transition{State: next}
	switch value := command.(type) {
	case StartTurn:
		if err := requirePhase(current, command, PhaseCreated); err != nil {
			return Transition{}, err
		}
		move(&transition, PhasePreparing)

	case PreparationFinished:
		if err := requirePhase(current, command, PhasePreparing); err != nil {
			return Transition{}, err
		}
		move(&transition, PhaseSampling)

	case ModelSampleRequested:
		if err := applyModelSampleRequested(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ModelSampleStarted:
		if err := applyModelSampleStarted(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ModelSampleFinished:
		if err := applyModelSampleFinished(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ModelSampleResultReceived:
		if err := applyModelSampleResult(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case SupplementalUsageRecorded:
		if err := applySupplementalUsage(
			&transition,
			current,
			value,
		); err != nil {
			return Transition{}, err
		}

	case ProviderRetryRequested:
		if err := applyProviderRetry(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ModelTextReceived:
		if err := requirePhase(current, command, PhaseSampling); err != nil {
			return Transition{}, err
		}
		if value.Text == "" {
			return Transition{}, illegal(current, command, "model text is empty")
		}
		transition.State.ProvisionalOutput = append(
			transition.State.ProvisionalOutput,
			value.Text,
		)
		transition.State.OutputEligibility = false

	case ReleaseProvisionalOutput:
		if err := applyReleaseOutput(&transition, current); err != nil {
			return Transition{}, err
		}

	case DiscardProvisionalOutput:
		if err := applyDiscardOutput(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case RepairRequested:
		if err := applyRepairRequested(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case EvaluateTurnStep:
		if err := applyEvaluateTurnStep(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ObserveProgress:
		if err := applyObserveProgress(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ToolCallsProposed:
		if err := applyToolCalls(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ApprovalRequired:
		if err := applyApprovalRequired(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ApprovalResolved:
		if err := applyApprovalResolved(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ApprovalResultReceived:
		if err := applyApprovalResult(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case InputRequired:
		if err := applyInputRequired(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case InputResolved:
		if err := applyInputResolved(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case InputResultReceived:
		if err := applyInputResult(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case ToolResultReceived:
		if err := applyToolResult(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case AbortOpenCalls:
		if err := applyAbortOpenCalls(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case VerificationStarted:
		if err := requirePhase(current, command, PhaseSampling); err != nil {
			return Transition{}, err
		}
		if current.Cancellation.Accepted {
			return Transition{}, illegal(current, command, "cancellation was accepted")
		}
		if len(current.OpenCalls) != 0 {
			return Transition{}, illegal(current, command, "tool calls remain open")
		}
		if current.MutationRevision == 0 {
			return Transition{}, illegal(current, command, "verification requires a mutation")
		}
		move(&transition, PhaseVerifying)
		requestEffect(
			&transition,
			EffectRunVerification,
			struct {
				Mutation uint64 `json:"mutation_revision"`
			}{Mutation: current.MutationRevision},
			fmt.Sprintf("verification:%d", current.MutationRevision),
			"",
		)

	case VerificationFinished:
		if err := applyVerificationFinished(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case CompletionEvaluated:
		if err := applyCompletion(&transition, current, value.Candidate); err != nil {
			return Transition{}, err
		}

	case CompletionInvalidated:
		if err := applyCompletionInvalidated(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case CancelRequested:
		if err := applyCancelRequested(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case RecoveryRequested:
		if err := applyRecoveryRequested(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case EffectStarted:
		if err := applyEffectStarted(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case EffectRequeued:
		if err := applyEffectRequeued(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case EffectResultReceived:
		if err := applyEffectResult(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case PersistenceResultReceived:
		if err := applyPersistenceResult(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case TerminalRequested:
		decision := TerminalDecision{Kind: TerminalCompleted}
		switch {
		case strings.TrimSpace(value.CancelReason) != "":
			decision.Kind = TerminalCanceled
			decision.Message = value.CancelReason
		case strings.TrimSpace(value.FailureMessage) != "":
			decision.Kind = TerminalFailed
			decision.Code = value.FailureCode
			decision.Message = value.FailureMessage
		}
		if err := applyTerminalRequested(
			&transition,
			current,
			value,
			decision,
		); err != nil {
			return Transition{}, err
		}

	case JournalFinalized:
		if err := applyJournalFinalized(&transition, current, value.Status); err != nil {
			return Transition{}, err
		}

	case JournalResultReceived:
		if err := applyJournalResult(&transition, current, value); err != nil {
			return Transition{}, err
		}

	case FinishTerminal:
		if err := applyFinishTerminal(&transition, current); err != nil {
			return Transition{}, err
		}

	default:
		return Transition{}, illegal(
			current,
			command,
			fmt.Sprintf("unsupported command type %T", command),
		)
	}

	if err := Validate(transition.State); err != nil {
		return Transition{}, fmt.Errorf(
			"command %s produced invalid turn state: %w",
			command.commandName(),
			err,
		)
	}
	return transition, nil
}

func applyModelSampleRequested(
	transition *Transition,
	current State,
	command ModelSampleRequested,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if current.Cancellation.Accepted {
		return illegal(current, command, "cancellation was accepted")
	}
	if strings.TrimSpace(command.SampleID) == "" {
		return illegal(current, command, "sample identity is empty")
	}
	if current.ActiveSampleID != "" {
		return illegal(current, command, "another model sample is active")
	}
	if _, exists := current.SampleLedger[command.SampleID]; exists {
		return illegal(current, command, "sample identity is duplicated")
	}
	transition.State.SampleLedger[command.SampleID] = ModelSampleState{
		ID:     command.SampleID,
		Status: SampleRequested,
	}
	transition.State.NextAction = StepActionNone
	requestEffect(
		transition,
		EffectSampleProvider,
		command,
		"sample:"+command.SampleID,
		command.SampleID,
	)
	return nil
}

func applyModelSampleStarted(
	transition *Transition,
	current State,
	command ModelSampleStarted,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if current.Cancellation.Accepted {
		return illegal(current, command, "cancellation was accepted")
	}
	if strings.TrimSpace(command.SampleID) == "" || command.Attempt == 0 {
		return illegal(current, command, "sample identity is incomplete")
	}
	if current.ActiveSampleID != "" {
		return illegal(current, command, "another model sample is active")
	}
	if existing, ok := current.SampleLedger[command.SampleID]; ok &&
		command.Attempt <= existing.Attempt {
		return illegal(current, command, "sample attempt is not monotonic")
	}
	transition.State.SampleLedger[command.SampleID] = ModelSampleState{
		ID:      command.SampleID,
		Attempt: command.Attempt,
		Status:  SampleRunning,
	}
	transition.State.ActiveSampleID = command.SampleID
	transition.Events = append(transition.Events, Event{
		Kind: EventSampleStarted, SampleID: command.SampleID,
	})
	return nil
}

func applyModelSampleFinished(
	transition *Transition,
	current State,
	command ModelSampleFinished,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if current.ActiveSampleID == "" ||
		current.ActiveSampleID != command.SampleID {
		return illegal(current, command, "sample result does not match active sample")
	}
	sample := current.SampleLedger[command.SampleID]
	sample.Status = SampleCompleted
	sample.Error = ""
	if command.Error != "" {
		sample.Status = SampleFailed
		sample.Error = command.Error
	}
	transition.State.SampleLedger[command.SampleID] = sample
	transition.State.ActiveSampleID = ""
	mergeUsage(&transition.State.Usage, command.Usage)
	transition.State.Context = command.Context
	transition.State.Context.Frozen = false
	transition.Events = append(transition.Events, Event{
		Kind: EventSampleFinished, SampleID: command.SampleID,
	})
	return nil
}

func applyModelSampleResult(
	transition *Transition,
	current State,
	command ModelSampleResultReceived,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if strings.TrimSpace(command.EffectID) == "" ||
		strings.TrimSpace(command.SampleID) == "" {
		return illegal(current, command, "sample result identity is incomplete")
	}
	if (command.Error == "") != (command.Failure == nil) {
		return illegal(current, command, "sample failure fact does not match result")
	}
	effect, ok := current.PendingEffects[command.EffectID]
	running := ok &&
		effect.Status == EffectRunning &&
		current.ActiveSampleID == command.SampleID
	scheduledAbort := ok &&
		effect.Status == EffectRequested &&
		command.Error != "" &&
		current.ActiveSampleID == "" &&
		current.SampleLedger[command.SampleID].Status == SampleRequested
	if !ok ||
		effect.Kind != EffectSampleProvider ||
		effect.CallID != command.SampleID ||
		(!running && !scheduledAbort) {
		return illegal(current, command, "sample result effect is not running")
	}
	success := command.Error == ""
	if err := finishEffect(
		transition,
		command.EffectID,
		success,
		command.Error,
	); err != nil {
		return illegal(current, command, err.Error())
	}
	sample := current.SampleLedger[command.SampleID]
	sample.Status = SampleCompleted
	sample.Error = ""
	sample.Retry = nil
	if !success {
		sample.Status = SampleFailed
		sample.Error = command.Error
		failure := *command.Failure
		sample.LastFailure = &failure
	}
	transition.State.SampleLedger[command.SampleID] = sample
	transition.State.ActiveSampleID = ""
	mergeUsage(&transition.State.Usage, command.Usage)
	transition.State.Context = command.Context
	transition.State.Context.Frozen = false
	transition.State.LastModelContinued = command.Continued
	transition.State.NextAction = StepActionNone
	transition.Events = append(transition.Events, Event{
		Kind: EventSampleFinished, SampleID: command.SampleID,
	})
	if !success {
		return nil
	}
	if command.Text != "" {
		transition.State.ProvisionalOutput = append(
			transition.State.ProvisionalOutput,
			command.Text,
		)
		transition.State.OutputEligibility = false
	}
	if len(command.Calls) != 0 {
		return applyToolCalls(
			transition,
			transition.State,
			ToolCallsProposed{Calls: command.Calls},
		)
	}
	return nil
}

func applySupplementalUsage(
	transition *Transition,
	current State,
	command SupplementalUsageRecorded,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if strings.TrimSpace(command.Source) == "" ||
		strings.TrimSpace(command.SampleID) == "" {
		return illegal(
			current,
			command,
			"supplemental usage identity is incomplete",
		)
	}
	if command.Usage.Frozen {
		return illegal(current, command, "supplemental usage is frozen")
	}
	mergeUsage(&transition.State.Usage, command.Usage)
	transition.Events = append(transition.Events, Event{
		Kind: EventUsageRecorded, SampleID: command.SampleID,
	})
	return nil
}

func mergeUsage(target *UsageState, value UsageState) {
	calls := value.Calls
	if calls == 0 {
		calls = 1
	}
	if target.Calls == 0 {
		target.CostKnown = value.CostKnown
	} else {
		target.CostKnown = target.CostKnown && value.CostKnown
	}
	target.Calls += calls
	target.InputTokens += value.InputTokens
	target.OutputTokens += value.OutputTokens
	target.ReasoningTokens += value.ReasoningTokens
	target.CachedTokens += value.CachedTokens
	target.CostMicrounits += value.CostMicrounits
}

func applyProviderRetry(
	transition *Transition,
	current State,
	command ProviderRetryRequested,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	effect, ok := current.PendingEffects[command.EffectID]
	if current.ActiveSampleID != command.SampleID ||
		!ok ||
		effect.Kind != EffectSampleProvider ||
		effect.CallID != command.SampleID ||
		effect.Status != EffectRunning ||
		command.Attempt == 0 ||
		command.Attempt != effect.Attempt ||
		command.Failure.Code == "" ||
		strings.TrimSpace(command.Failure.Message) == "" ||
		command.Retry == 0 ||
		strings.TrimSpace(command.PolicyRevision) == "" ||
		command.RetryAt.IsZero() {
		return illegal(current, command, "provider retry does not match active sample")
	}
	sample := current.SampleLedger[command.SampleID]
	if command.Retry != sample.ProviderRetries+1 {
		return illegal(current, command, "provider retry number is not monotonic")
	}
	failure := command.Failure
	sample.ProviderRetries = command.Retry
	sample.LastFailure = &failure
	sample.Retry = &ProviderRetryState{
		EffectID:         command.EffectID,
		Attempt:          command.Attempt,
		Retry:            command.Retry,
		EffectiveDelayMS: command.EffectiveDelayMS,
		RetryAt:          command.RetryAt,
		PolicyRevision:   command.PolicyRevision,
	}
	sample.Status = SampleRequested
	sample.Error = ""
	transition.State.SampleLedger[command.SampleID] = sample
	transition.State.ActiveSampleID = ""
	effect.Status = EffectRequested
	transition.State.PendingEffects[command.EffectID] = effect
	transition.Events = append(transition.Events, Event{
		Kind: EventProviderRetry, EffectID: command.EffectID,
		SampleID: command.SampleID,
	})
	return nil
}

func applyApprovalResult(
	transition *Transition,
	current State,
	command ApprovalResultReceived,
) error {
	if strings.TrimSpace(command.EffectID) == "" {
		return illegal(current, command, "approval effect id is empty")
	}
	if effect, ok := current.PendingEffects[command.EffectID]; !ok ||
		effect.Kind != EffectAwaitApproval ||
		effect.Status != EffectRunning {
		return illegal(current, command, "approval effect is not running")
	}
	if err := finishEffect(
		transition,
		command.EffectID,
		command.Accepted,
		command.Error,
	); err != nil {
		return illegal(current, command, err.Error())
	}
	if err := applyApprovalResolved(
		transition,
		transition.State,
		ApprovalResolved{RequestID: command.RequestID},
	); err != nil {
		return err
	}
	if command.Canceled {
		return applyCancelRequested(
			transition,
			transition.State,
			CancelRequested{Reason: protocol.CancelReasonApprovalCanceled},
		)
	}
	return nil
}

func applyInputResult(
	transition *Transition,
	current State,
	command InputResultReceived,
) error {
	if strings.TrimSpace(command.EffectID) == "" {
		return illegal(current, command, "input effect id is empty")
	}
	if effect, ok := current.PendingEffects[command.EffectID]; !ok ||
		effect.Kind != EffectAwaitInput ||
		effect.Status != EffectRunning {
		return illegal(current, command, "input effect is not running")
	}
	if err := finishEffect(
		transition,
		command.EffectID,
		command.Accepted,
		command.Error,
	); err != nil {
		return illegal(current, command, err.Error())
	}
	return applyInputResolved(
		transition,
		transition.State,
		InputResolved{RequestID: command.RequestID},
	)
}

func applyCancelRequested(
	transition *Transition,
	current State,
	command CancelRequested,
) error {
	if strings.TrimSpace(command.Reason) == "" {
		return illegal(current, command, "cancel reason is empty")
	}
	if current.Cancellation.Accepted {
		return illegal(current, command, "cancellation was already accepted")
	}
	transition.State.Cancellation = CancellationState{
		Requested: true,
		Accepted:  true,
		Reason:    command.Reason,
	}
	transition.Events = append(
		transition.Events,
		Event{Kind: EventCancelAccepted},
	)
	return nil
}

func applyRecoveryRequested(
	transition *Transition,
	current State,
	command RecoveryRequested,
) error {
	if err := requirePhase(current, command, PhaseCreated); err != nil {
		return err
	}
	if strings.TrimSpace(command.SourceTurnID) == "" ||
		strings.TrimSpace(command.RecoveryTurnID) == "" ||
		command.CurrentProfileRevision == 0 ||
		(command.Action != string(protocol.TurnRecoveryRetry) &&
			command.Action != string(protocol.TurnRecoveryContinue)) ||
		(command.DraftResumed && len(command.Changes) == 0) {
		return illegal(current, command, "recovery relation is incomplete")
	}
	transition.State.ProfileRevision = command.CurrentProfileRevision
	transition.State.RecoveryRelation = &RecoveryRelation{
		SourceTurnID:   command.SourceTurnID,
		RecoveryTurnID: command.RecoveryTurnID,
		Action:         command.Action,
		DraftResumed:   command.DraftResumed,
	}
	if len(command.Changes) != 0 {
		transition.State.MutationRevision = 1
		transition.State.Changes = append(
			[]ObservedChange(nil),
			command.Changes...,
		)
		transition.State.Journal = JournalOpen
		transition.Events = append(transition.Events, Event{
			Kind: EventMutationObserved, Mutation: 1,
		})
	}
	transition.Events = append(
		transition.Events,
		Event{Kind: EventRecoveryBound},
	)
	return nil
}

func applyEffectStarted(
	transition *Transition,
	current State,
	command EffectStarted,
) error {
	effect, ok := current.PendingEffects[command.EffectID]
	if !ok {
		return illegal(current, command, "effect is not pending")
	}
	if effect.Status != EffectRequested || command.Attempt == 0 {
		return illegal(current, command, "effect cannot start")
	}
	effect.Status = EffectRunning
	effect.Attempt = command.Attempt
	transition.State.PendingEffects[command.EffectID] = effect
	if effect.Kind == EffectSampleProvider {
		sample, ok := current.SampleLedger[effect.CallID]
		if !ok || sample.Status != SampleRequested {
			return illegal(current, command, "sample effect is not requested")
		}
		sample.Status = SampleRunning
		sample.Attempt = command.Attempt
		sample.Retry = nil
		sample.Error = ""
		transition.State.SampleLedger[effect.CallID] = sample
		transition.State.ActiveSampleID = effect.CallID
	}
	transition.Events = append(transition.Events, Event{
		Kind: EventEffectStarted, EffectID: command.EffectID,
	})
	return nil
}

func applyEffectRequeued(
	transition *Transition,
	current State,
	command EffectRequeued,
) error {
	effect, ok := current.PendingEffects[command.EffectID]
	if !ok {
		return illegal(current, command, "effect is not pending")
	}
	if effect.Status != EffectRunning {
		return illegal(current, command, "only a running effect can be requeued")
	}
	effect.Status = EffectRequested
	transition.State.PendingEffects[command.EffectID] = effect
	if effect.Kind == EffectSampleProvider {
		sample, ok := current.SampleLedger[effect.CallID]
		if !ok || sample.Status != SampleRunning {
			return illegal(current, command, "running sample effect has no sample")
		}
		sample.Status = SampleRequested
		transition.State.SampleLedger[effect.CallID] = sample
		transition.State.ActiveSampleID = ""
	}
	transition.Events = append(transition.Events, Event{
		Kind: EventEffectRequeued, EffectID: command.EffectID,
	})
	return nil
}

func applyEffectResult(
	transition *Transition,
	current State,
	command EffectResultReceived,
) error {
	if err := finishEffect(
		transition,
		command.EffectID,
		command.Success,
		command.Error,
	); err != nil {
		return illegal(current, command, err.Error())
	}
	return nil
}

func applyPersistenceResult(
	transition *Transition,
	current State,
	command PersistenceResultReceived,
) error {
	if err := finishEffect(
		transition,
		command.EffectID,
		command.Success,
		command.Error,
	); err != nil {
		return illegal(current, command, err.Error())
	}
	return nil
}

func applyToolCalls(
	transition *Transition,
	current State,
	command ToolCallsProposed,
) error {
	if err := requirePhase(
		current,
		command,
		PhaseSampling,
		PhaseExecutingTools,
	); err != nil {
		return err
	}
	if len(command.Calls) == 0 {
		return illegal(current, command, "tool batch is empty")
	}
	if current.Cancellation.Accepted {
		return illegal(current, command, "cancellation was accepted")
	}
	transition.State.Completion = nil
	transition.State.OutputEligibility = false
	if len(current.ProvisionalOutput) != 0 {
		transition.State.ProvisionalOutput = nil
		transition.Events = append(
			transition.Events,
			Event{Kind: EventOutputDiscarded},
		)
	}
	seen := make(map[string]struct{}, len(command.Calls))
	for _, call := range command.Calls {
		if strings.TrimSpace(call.ID) == "" || strings.TrimSpace(call.Name) == "" {
			return illegal(current, command, "tool call identity is incomplete")
		}
		if _, ok := seen[call.ID]; ok {
			return illegal(current, command, "tool call id is duplicated")
		}
		if _, ok := current.OpenCalls[call.ID]; ok {
			return illegal(current, command, "tool call id is already open")
		}
		if _, ok := current.ClosedCalls[call.ID]; ok {
			return illegal(current, command, "tool call id was already closed")
		}
		seen[call.ID] = struct{}{}
		transition.State.OpenCalls[call.ID] = call
		transition.Events = append(transition.Events, Event{
			Kind: EventToolStarted, CallID: call.ID,
		})
		requestEffect(
			transition,
			EffectExecuteTool,
			call,
			"tool:"+call.ID,
			call.ID,
		)
	}
	if current.Phase == PhaseSampling {
		move(transition, PhaseExecutingTools)
	}
	return nil
}

func applyApprovalRequired(
	transition *Transition,
	current State,
	command ApprovalRequired,
) error {
	if err := requirePhase(
		current,
		command,
		PhaseExecutingTools,
		PhaseAwaitingApproval,
	); err != nil {
		return err
	}
	if strings.TrimSpace(command.RequestID) == "" {
		return illegal(current, command, "approval request id is empty")
	}
	if _, ok := current.OpenCalls[command.CallID]; !ok {
		return illegal(current, command, "approval does not reference an open call")
	}
	if _, exists := current.PendingApprovals[command.RequestID]; exists {
		return illegal(current, command, "approval request id is duplicated")
	}
	transition.State.PendingApprovals[command.RequestID] = ApprovalState{
		RequestID: command.RequestID,
		CallID:    command.CallID,
	}
	requestEffect(
		transition,
		EffectAwaitApproval,
		command,
		"approval:"+command.RequestID,
		command.CallID,
	)
	if current.Phase == PhaseExecutingTools {
		move(transition, PhaseAwaitingApproval)
	}
	return nil
}

func applyApprovalResolved(
	transition *Transition,
	current State,
	command ApprovalResolved,
) error {
	if err := requirePhase(current, command, PhaseAwaitingApproval); err != nil {
		return err
	}
	if _, ok := current.PendingApprovals[command.RequestID]; !ok {
		return illegal(current, command, "approval request does not match")
	}
	delete(transition.State.PendingApprovals, command.RequestID)
	closeEffectByIdentity(
		transition,
		EffectAwaitApproval,
		command.RequestID,
		true,
		"",
	)
	if len(transition.State.PendingApprovals) == 0 {
		move(transition, PhaseExecutingTools)
	}
	return nil
}

func applyInputRequired(
	transition *Transition,
	current State,
	command InputRequired,
) error {
	if err := requirePhase(
		current,
		command,
		PhaseSampling,
		PhaseExecutingTools,
	); err != nil {
		return err
	}
	if strings.TrimSpace(command.RequestID) == "" {
		return illegal(current, command, "input request id is empty")
	}
	transition.State.PendingInput = &InputState{RequestID: command.RequestID}
	requestEffect(
		transition,
		EffectAwaitInput,
		command,
		"input:"+command.RequestID,
		"",
	)
	move(transition, PhaseAwaitingInput)
	return nil
}

func applyReleaseOutput(
	transition *Transition,
	current State,
) error {
	command := ReleaseProvisionalOutput{}
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if len(current.ProvisionalOutput) == 0 {
		return illegal(current, command, "provisional output is empty")
	}
	if err := validateCompletionContract(current); err != nil {
		return illegal(current, command, err.Error())
	}
	transition.State.OutputEligibility = true
	transition.Events = append(
		transition.Events,
		Event{Kind: EventOutputReleased},
	)
	return nil
}

func applyDiscardOutput(
	transition *Transition,
	current State,
	command DiscardProvisionalOutput,
) error {
	if err := requirePhase(
		current,
		command,
		PhaseSampling,
		PhaseExecutingTools,
		PhaseAwaitingApproval,
		PhaseAwaitingInput,
		PhaseVerifying,
	); err != nil {
		return err
	}
	if strings.TrimSpace(command.Reason) == "" {
		return illegal(current, command, "discard reason is empty")
	}
	transition.State.ProvisionalOutput = nil
	transition.State.OutputEligibility = false
	transition.Events = append(
		transition.Events,
		Event{Kind: EventOutputDiscarded},
	)
	return nil
}

func applyRepairRequested(
	transition *Transition,
	current State,
	command RepairRequested,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if !validRepairKind(command.Kind) {
		return illegal(current, command, "repair kind is invalid")
	}
	if strings.TrimSpace(command.ProgressKey) == "" {
		return illegal(current, command, "repair progress key is empty")
	}
	if command.Limit == 0 {
		return illegal(current, command, "repair limit is zero")
	}
	return spendRepairBudget(
		transition,
		command.Kind,
		command.ProgressKey,
		command.Limit,
	)
}

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
		if err := spendRepairBudget(
			transition,
			kind,
			command.ProgressKey,
			limit,
		); err != nil {
			return err
		}
		transition.State.NextAction = action
		return nil
	}
	switch {
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
	case current.LastModelContinued && len(current.ClosedCalls) != 0:
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

const (
	progressConvergeSamples   = 16
	progressFinishOnlySamples = 32
	progressExhaustedSamples  = 48
	researchConvergeSamples   = 8
	researchFinishOnlySamples = 12
	researchExhaustedSamples  = 16
)

func applyObserveProgress(
	transition *Transition,
	current State,
	command ObserveProgress,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if current.ActiveSampleID != "" {
		return illegal(current, command, "model sample is active")
	}
	signature := strings.TrimSpace(command.Signature)
	if signature == "" {
		return illegal(current, command, "progress signature is empty")
	}
	progress := current.Progress
	if command.CompletedSamples < progress.ObservedSamples {
		return illegal(current, command, "completed samples regressed")
	}
	if progress.Signature == "" || progress.Signature != signature {
		progress.Signature = signature
		progress.NoProgressSamples = 0
		progress.Stage = ProgressStageNone
	} else {
		progress.NoProgressSamples +=
			command.CompletedSamples - progress.ObservedSamples
	}
	progress.ObservedSamples = command.CompletedSamples
	switch {
	case progress.NoProgressSamples >= progressExhaustedSamples:
		progress.Stage = ProgressStageExhausted
	case progress.NoProgressSamples >= progressFinishOnlySamples:
		progress.Stage = ProgressStageFinishOnly
	case progress.NoProgressSamples >= progressConvergeSamples:
		progress.Stage = ProgressStageConverge
	default:
		progress.Stage = ProgressStageNone
	}
	if IsResearchIntent(current.Intent) &&
		current.MutationRevision == 0 {
		researchStage := ProgressStageNone
		switch {
		case command.CompletedSamples >= researchExhaustedSamples:
			researchStage = ProgressStageExhausted
		case command.CompletedSamples >= researchFinishOnlySamples:
			researchStage = ProgressStageFinishOnly
		case command.CompletedSamples >= researchConvergeSamples:
			researchStage = ProgressStageConverge
		}
		if progressStageRank(researchStage) > progressStageRank(progress.Stage) {
			progress.Stage = researchStage
		}
	}
	transition.State.Progress = progress
	return nil
}

func progressStageRank(stage ProgressStage) int {
	switch stage {
	case ProgressStageConverge:
		return 1
	case ProgressStageFinishOnly:
		return 2
	case ProgressStageExhausted:
		return 3
	default:
		return 0
	}
}

func spendRepairBudget(
	transition *Transition,
	kind RepairKind,
	progressKey string,
	limit uint32,
) error {
	if !validRepairKind(kind) {
		return fmt.Errorf("invalid repair kind %q", kind)
	}
	if strings.TrimSpace(progressKey) == "" || limit == 0 {
		return errors.New("repair budget request is incomplete")
	}
	budget := transition.State.RepairBudgets[kind]
	if budget.ProgressKey != progressKey {
		budget.ProgressKey = progressKey
		budget.Consecutive = 0
	}
	if budget.Consecutive >= limit {
		return &RepairBudgetExhaustedError{
			Kind: kind, ProgressKey: progressKey, Limit: limit,
		}
	}
	budget.Consecutive++
	budget.Steps++
	transition.State.RepairBudgets[kind] = budget
	transition.Events = append(
		transition.Events,
		Event{Kind: EventRepairRequested},
	)
	return nil
}

func validRepairKind(kind RepairKind) bool {
	switch kind {
	case RepairCompletion, RepairWorkspace, RepairDeclaration, RepairVerification:
		return true
	default:
		return false
	}
}

func applyInputResolved(
	transition *Transition,
	current State,
	command InputResolved,
) error {
	if err := requirePhase(current, command, PhaseAwaitingInput); err != nil {
		return err
	}
	if current.PendingInput == nil ||
		current.PendingInput.RequestID != command.RequestID {
		return illegal(current, command, "input request does not match")
	}
	transition.State.PendingInput = nil
	closeEffectByIdentity(
		transition,
		EffectAwaitInput,
		command.RequestID,
		true,
		"",
	)
	if len(current.OpenCalls) != 0 {
		move(transition, PhaseExecutingTools)
	} else {
		move(transition, PhaseSampling)
	}
	return nil
}

func applyToolResult(
	transition *Transition,
	current State,
	command ToolResultReceived,
) error {
	callAwaitingApproval := false
	for _, approval := range current.PendingApprovals {
		if approval.CallID == command.CallID {
			callAwaitingApproval = true
			break
		}
	}
	cancelingApproval := callAwaitingApproval && current.Cancellation.Accepted
	if err := requirePhase(
		current,
		command,
		PhaseExecutingTools,
		PhaseAwaitingApproval,
	); err != nil {
		return err
	}
	if callAwaitingApproval && !cancelingApproval {
		return illegal(
			current,
			command,
			"tool result cannot bypass a pending approval",
		)
	}
	if strings.TrimSpace(command.EffectID) == "" {
		return illegal(current, command, "tool effect id is empty")
	}
	call, ok := current.OpenCalls[command.CallID]
	if !ok {
		return illegal(current, command, "tool result does not match an open call")
	}
	effect, ok := current.PendingEffects[command.EffectID]
	if !ok ||
		effect.Kind != EffectExecuteTool ||
		effect.CallID != command.CallID ||
		effect.Status != EffectRunning {
		return illegal(current, command, "tool result effect does not match call")
	}
	if err := finishEffect(
		transition,
		command.EffectID,
		true,
		"",
	); err != nil {
		return illegal(current, command, err.Error())
	}
	delete(transition.State.OpenCalls, command.CallID)
	transition.State.ClosedCalls[command.CallID] = ToolResultState{
		ID: command.CallID, Name: call.Name, IsError: command.IsError,
	}
	if cancelingApproval {
		for requestID, approval := range current.PendingApprovals {
			if approval.CallID != command.CallID {
				continue
			}
			delete(transition.State.PendingApprovals, requestID)
			closeEffectByIdentity(
				transition,
				EffectAwaitApproval,
				requestID,
				false,
				current.Cancellation.Reason,
			)
		}
	}
	if command.IsError {
		transition.State.UnresolvedToolFailure = true
		transition.State.RecoveryToolSucceeded = false
	} else if current.UnresolvedToolFailure {
		transition.State.RecoveryToolSucceeded = true
	}
	transition.Events = append(transition.Events, Event{
		Kind: EventToolClosed, CallID: command.CallID,
	})
	if len(command.Changes) != 0 {
		transition.State.MutationRevision++
		transition.State.Changes = append(
			transition.State.Changes,
			command.Changes...,
		)
		transition.State.Completion = nil
		transition.State.Verification = VerificationState{
			Status: VerificationNotEvaluated,
		}
		transition.State.Journal = JournalOpen
		transition.Events = append(transition.Events, Event{
			Kind:     EventMutationObserved,
			Mutation: transition.State.MutationRevision,
		})
	}
	switch {
	case len(transition.State.PendingApprovals) != 0:
		move(transition, PhaseAwaitingApproval)
	case len(transition.State.OpenCalls) != 0:
		move(transition, PhaseExecutingTools)
	default:
		move(transition, PhaseSampling)
	}
	return nil
}

func applyAbortOpenCalls(
	transition *Transition,
	current State,
	command AbortOpenCalls,
) error {
	if err := requirePhase(
		current,
		command,
		PhaseExecutingTools,
		PhaseAwaitingApproval,
	); err != nil {
		return err
	}
	if strings.TrimSpace(command.Reason) == "" {
		return illegal(current, command, "abort reason is empty")
	}
	if len(current.OpenCalls) == 0 {
		return illegal(current, command, "no tool calls are open")
	}
	callIDs := make([]string, 0, len(current.OpenCalls))
	for callID := range current.OpenCalls {
		callIDs = append(callIDs, callID)
	}
	slices.Sort(callIDs)
	for _, callID := range callIDs {
		call := current.OpenCalls[callID]
		delete(transition.State.OpenCalls, callID)
		transition.State.ClosedCalls[callID] = ToolResultState{
			ID: callID, Name: call.Name, IsError: true,
		}
		closeEffectByCall(
			transition,
			EffectExecuteTool,
			callID,
			false,
			command.Reason,
		)
		transition.Events = append(transition.Events, Event{
			Kind: EventToolClosed, CallID: callID,
		})
	}
	clear(transition.State.PendingApprovals)
	closeEffectsByKind(
		transition,
		EffectAwaitApproval,
		false,
		command.Reason,
	)
	move(transition, PhaseSampling)
	return nil
}

func applyVerificationFinished(
	transition *Transition,
	current State,
	command VerificationFinished,
) error {
	if err := requirePhase(current, command, PhaseVerifying); err != nil {
		return err
	}
	switch command.Status {
	case VerificationPassed, VerificationFailed, VerificationUnavailable:
	default:
		return illegal(current, command, "verification status is not terminal")
	}
	effectID := command.EffectID
	if effectID == "" {
		for _, candidate := range sortedEffectIDs(current.PendingEffects) {
			if current.PendingEffects[candidate].Kind == EffectRunVerification {
				effectID = candidate
				break
			}
		}
	}
	if effectID == "" {
		return illegal(current, command, "verification effect is missing")
	}
	effect := current.PendingEffects[effectID]
	if effect.Kind != EffectRunVerification ||
		effect.Status != EffectRunning {
		return illegal(current, command, "verification effect is not running")
	}
	if err := finishEffect(transition, effectID, true, ""); err != nil {
		return illegal(current, command, err.Error())
	}
	action := VerificationActionPassed
	needsRepair := command.Status == VerificationFailed ||
		(command.Status == VerificationUnavailable &&
			current.Policy.VerificationMustPass)
	if needsRepair && current.Policy.VerificationRepairLimit != 0 {
		err := spendRepairBudget(
			transition,
			RepairVerification,
			command.RepairKey,
			current.Policy.VerificationRepairLimit,
		)
		switch {
		case err == nil:
			action = VerificationActionRepair
		case errors.Is(err, ErrRepairBudgetExhausted):
		default:
			return err
		}
	}
	if action != VerificationActionRepair {
		switch {
		case command.Status == VerificationPassed:
			action = VerificationActionPassed
		case current.Policy.VerificationMustPass:
			action = VerificationActionBlocked
		case current.Policy.VerificationMode == "soft":
			action = VerificationActionReported
		case current.Policy.VerificationOnFailure == "revert":
			action = VerificationActionReverted
		default:
			action = VerificationActionFailed
		}
	}
	transition.State.Verification = VerificationState{
		Status:         command.Status,
		Action:         action,
		Mutation:       current.MutationRevision,
		EvidenceCalls:  append([]string(nil), command.EvidenceCalls...),
		FailureMessage: command.Message,
	}
	if command.Status != VerificationPassed {
		transition.State.Completion = nil
	}
	transition.State.NextAction = StepActionNone
	transition.Events = append(transition.Events, Event{
		Kind: EventVerification, Mutation: current.MutationRevision,
	})
	move(transition, PhaseSampling)
	return nil
}

func applyCompletion(
	transition *Transition,
	current State,
	candidate CompletionCandidate,
) error {
	command := CompletionEvaluated{Candidate: candidate}
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if strings.TrimSpace(candidate.CompletionCall) == "" {
		return illegal(current, command, "completion call id is empty")
	}
	decision := CompletionDecision{
		Summary:        candidate.Summary,
		Mutation:       current.MutationRevision,
		ChangedPaths:   changedPaths(current.Changes),
		QualityCalls:   append([]string(nil), candidate.QualityCalls...),
		CompletionCall: candidate.CompletionCall,
	}
	switch {
	case candidate.BatchMutated:
		decision.Reason = "same_batch_mutation"
	case candidate.BatchSize != 1:
		decision.Reason = "declaration_must_be_only_call"
	case !candidate.DeclarationValid:
		decision.Reason = "invalid_declaration"
	case candidate.Status == "incomplete" &&
		strings.TrimSpace(candidate.Summary) != "" &&
		len(candidate.PendingActions) != 0:
		decision.Reason = "pending_actions"
	case candidate.ToolError ||
		candidate.Status != "complete" ||
		strings.TrimSpace(candidate.Summary) == "" ||
		len(candidate.PendingActions) != 0:
		decision.Reason = "incomplete_declaration"
	case current.Intent == protocol.TurnIntentWorkspaceChange &&
		current.MutationRevision == 0:
		decision.Reason = "no_observed_changes"
	case candidate.QualityRequired && len(candidate.QualityCalls) == 0:
		decision.Reason = "quality_verification_required"
	default:
		decision.Accepted = true
		decision.RequiredAction = "final_answer"
	}
	if !decision.Accepted {
		decision.RequiredAction = completionRejectionAction(decision.Reason)
	} else {
		// The accepted declaration owns the exact user-facing terminal output.
		// Any earlier model prose was progress narration and remains provisional.
		transition.State.ProvisionalOutput = []string{
			strings.TrimSpace(candidate.Summary),
		}
		transition.State.OutputEligibility = false
	}
	copy := decision
	copy.ChangedPaths = append([]string(nil), decision.ChangedPaths...)
	copy.QualityCalls = append([]string(nil), decision.QualityCalls...)
	transition.State.Completion = &copy
	transition.Events = append(transition.Events, Event{
		Kind:     EventCompletionDecided,
		Mutation: current.MutationRevision,
	})
	return nil
}

func applyCompletionInvalidated(
	transition *Transition,
	current State,
	command CompletionInvalidated,
) error {
	if err := requirePhase(current, command, PhaseSampling); err != nil {
		return err
	}
	if strings.TrimSpace(command.Reason) == "" {
		return illegal(current, command, "completion invalidation reason is empty")
	}
	if current.Completion == nil || !current.Completion.Accepted {
		return illegal(current, command, "accepted completion is unavailable")
	}
	transition.State.Completion = nil
	transition.Events = append(transition.Events, Event{
		Kind:     EventCompletionDecided,
		Mutation: current.MutationRevision,
	})
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
	default:
		return "correct_and_retry_turn_complete"
	}
}

func applyTerminalRequested(
	transition *Transition,
	current State,
	command TerminalRequested,
	decision TerminalDecision,
) error {
	if current.Phase == PhaseCommitting {
		return illegal(current, command, "terminal transaction is already active")
	}
	if len(current.OpenCalls) != 0 {
		return illegal(current, command, "tool calls remain open")
	}
	if len(current.PendingApprovals) != 0 {
		return illegal(current, command, "approval remains pending")
	}
	if current.PendingInput != nil {
		return illegal(current, command, "user input remains pending")
	}
	if current.ActiveSampleID != "" {
		return illegal(current, command, "model sample remains active")
	}
	if len(current.PendingEffects) != 0 {
		return illegal(current, command, "effects remain pending")
	}
	switch decision.Kind {
	case TerminalCompleted:
		if current.Phase != PhaseSampling {
			return illegal(current, command, "completion must begin from sampling")
		}
		if err := validateCompletionReadiness(current); err != nil {
			return illegal(current, command, err.Error())
		}
	case TerminalFailed, TerminalCanceled:
		if strings.TrimSpace(decision.Message) == "" {
			return illegal(current, command, "non-success terminal message is empty")
		}
	default:
		return illegal(current, command, "terminal kind is invalid")
	}
	copy := decision
	transition.State.PendingTerminal = &copy
	transition.State.Usage.Frozen = true
	transition.State.Context.Frozen = true
	move(transition, PhaseCommitting)
	transition.Events = append(transition.Events, Event{
		Kind: EventTerminalPrepared, Terminal: &copy,
	})
	if current.Journal == JournalOpen || current.Policy.JournalRequired {
		effect, _ := terminalJournalOutcome(current, decision)
		requestEffect(
			transition,
			effect,
			decision,
			"journal:"+string(decision.Kind),
			"",
		)
	}
	return nil
}

func applyJournalFinalized(
	transition *Transition,
	current State,
	status JournalStatus,
) error {
	command := JournalFinalized{Status: status}
	if err := requirePhase(current, command, PhaseCommitting); err != nil {
		return err
	}
	if current.PendingTerminal == nil {
		return illegal(current, command, "terminal transaction is missing")
	}
	effectKind, expected := terminalJournalOutcome(
		current,
		*current.PendingTerminal,
	)
	if current.Journal != JournalOpen || status != expected {
		return illegal(current, command, "journal result does not match terminal outcome")
	}
	transition.State.Journal = status
	closeFirstEffectByKind(transition, effectKind, true, "")
	return nil
}

func applyJournalResult(
	transition *Transition,
	current State,
	command JournalResultReceived,
) error {
	if err := requirePhase(current, command, PhaseCommitting); err != nil {
		return err
	}
	effect, ok := current.PendingEffects[command.EffectID]
	if !ok || effect.Status != EffectRunning {
		return illegal(current, command, "journal effect is not running")
	}
	if current.PendingTerminal == nil {
		return illegal(current, command, "terminal transaction is missing")
	}
	expectedKind, expectedStatus := terminalJournalOutcome(
		current,
		*current.PendingTerminal,
	)
	if effect.Kind != expectedKind || command.Status != expectedStatus {
		return illegal(current, command, "journal result does not match terminal outcome")
	}
	if current.MutationRevision == 0 {
		if !current.Policy.JournalRequired || current.Journal != JournalNone {
			return illegal(current, command, "unchanged turn has no open journal")
		}
	} else if current.Journal != JournalOpen {
		return illegal(current, command, "changed turn has no open journal")
	}
	if err := finishEffect(
		transition,
		command.EffectID,
		command.Error == "",
		command.Error,
	); err != nil {
		return illegal(current, command, err.Error())
	}
	if command.Error == "" {
		transition.State.Journal = command.Status
	}
	return nil
}

func applyFinishTerminal(transition *Transition, current State) error {
	command := FinishTerminal{}
	if err := requirePhase(current, command, PhaseCommitting); err != nil {
		return err
	}
	if current.PendingTerminal == nil {
		return illegal(current, command, "terminal transaction is missing")
	}
	if len(current.Changes) != 0 {
		_, expected := terminalJournalOutcome(current, *current.PendingTerminal)
		if current.Journal != expected {
			return illegal(current, command, "journal is not finalized")
		}
	} else {
		expected := JournalNone
		if current.Policy.JournalRequired {
			_, expected = terminalJournalOutcome(current, *current.PendingTerminal)
		}
		if current.Journal != expected {
			return illegal(current, command, "unchanged turn journal is not finalized")
		}
	}
	decision := *current.PendingTerminal
	transition.State.PendingTerminal = nil
	transition.State.Terminal = &decision
	transition.State.FinalOutput = append(
		[]string(nil),
		current.ProvisionalOutput...,
	)
	transition.State.ProvisionalOutput = nil
	switch decision.Kind {
	case TerminalCompleted:
		move(transition, PhaseCompleted)
	case TerminalFailed:
		move(transition, PhaseFailed)
	case TerminalCanceled:
		move(transition, PhaseCanceled)
	}
	transition.Events = append(transition.Events, Event{
		Kind: EventTerminalCommitted, Terminal: &decision,
	})
	return nil
}

func terminalJournalOutcome(
	state State,
	decision TerminalDecision,
) (EffectKind, JournalStatus) {
	switch {
	case decision.Kind == TerminalCompleted &&
		state.Verification.Action != VerificationActionReverted:
		return EffectCommitJournal, JournalCommitted
	case decision.Kind == TerminalFailed &&
		(state.Verification.Action == VerificationActionBlocked ||
			state.RecoveryRelation != nil &&
				state.RecoveryRelation.DraftResumed):
		return EffectSuspendJournal, JournalSuspended
	default:
		return EffectRollbackJournal, JournalRolledBack
	}
}

func validateCompletionReadiness(state State) error {
	if !state.OutputEligibility {
		return errors.New("final output is not eligible")
	}
	return validateCompletionContract(state)
}

func validateCompletionContract(state State) error {
	hasChanges := state.MutationRevision != 0
	if state.Intent == protocol.TurnIntentWorkspaceChange && !hasChanges {
		return errors.New("workspace_change has no observed mutation")
	}
	if !hasChanges {
		if state.Journal != JournalNone {
			return errors.New("unchanged turn has an open journal")
		}
	}
	if RequiresCompletion(state) {
		if state.Completion == nil || !state.Completion.Accepted {
			return errors.New("turn has no accepted completion decision")
		}
		if state.Completion.Mutation != state.MutationRevision {
			return errors.New("completion decision is stale")
		}
	}
	if !hasChanges {
		return nil
	}
	if state.Policy.VerificationRequired &&
		(state.Verification.Mutation != state.MutationRevision ||
			(state.Verification.Action != VerificationActionPassed &&
				state.Verification.Action != VerificationActionReported &&
				state.Verification.Action != VerificationActionReverted)) {
		return errors.New("mutation has no current completion verification")
	}
	if state.Journal != JournalOpen {
		return errors.New("mutation journal is not open")
	}
	return nil
}

func requestEffect(
	transition *Transition,
	kind EffectKind,
	payload any,
	idempotencyKey string,
	callID string,
) Effect {
	transition.State.NextEffectSequence++
	encoded, err := json.Marshal(payload)
	if err != nil {
		encoded = fmt.Appendf(nil, "%T", payload)
	}
	sum := sha256.Sum256(encoded)
	effect := Effect{
		ID: fmt.Sprintf(
			"effect-%016x",
			transition.State.NextEffectSequence,
		),
		Kind:           kind,
		Payload:        append(json.RawMessage(nil), encoded...),
		PayloadDigest:  "sha256:" + hex.EncodeToString(sum[:]),
		IdempotencyKey: idempotencyKey,
		Status:         EffectRequested,
		CallID:         callID,
	}
	transition.State.PendingEffects[effect.ID] = effect
	transition.Effects = append(transition.Effects, effect)
	transition.Events = append(transition.Events, Event{
		Kind: EventEffectRequested, EffectID: effect.ID, CallID: callID,
	})
	return effect
}

func finishEffect(
	transition *Transition,
	effectID string,
	success bool,
	message string,
) error {
	effect, ok := transition.State.PendingEffects[effectID]
	if !ok {
		if _, completed := transition.State.CompletedEffects[effectID]; completed {
			return errors.New("effect result is duplicated")
		}
		return errors.New("effect result does not match a pending effect")
	}
	if effect.Status != EffectRequested && effect.Status != EffectRunning {
		return errors.New("effect is not awaiting a result")
	}
	if !success && strings.TrimSpace(message) == "" {
		return errors.New("failed effect result has no error")
	}
	effect.Status = EffectSucceeded
	effect.Error = ""
	if !success {
		effect.Status = EffectFailed
		effect.Error = message
	}
	delete(transition.State.PendingEffects, effectID)
	transition.State.CompletedEffects[effectID] = effect
	transition.Events = append(transition.Events, Event{
		Kind: EventEffectFinished, EffectID: effect.ID, CallID: effect.CallID,
	})
	return nil
}

func closeEffectByCall(
	transition *Transition,
	kind EffectKind,
	callID string,
	success bool,
	message string,
) {
	for _, effectID := range sortedEffectIDs(transition.State.PendingEffects) {
		effect := transition.State.PendingEffects[effectID]
		if effect.Kind == kind && effect.CallID == callID {
			_ = finishEffect(transition, effectID, success, nonEmptyEffectError(success, message))
			return
		}
	}
}

func closeEffectByIdentity(
	transition *Transition,
	kind EffectKind,
	identity string,
	success bool,
	message string,
) {
	wantKey := ""
	switch kind {
	case EffectAwaitApproval:
		wantKey = "approval:" + identity
	case EffectAwaitInput:
		wantKey = "input:" + identity
	}
	for _, effectID := range sortedEffectIDs(transition.State.PendingEffects) {
		effect := transition.State.PendingEffects[effectID]
		if effect.Kind == kind && effect.IdempotencyKey == wantKey {
			_ = finishEffect(transition, effectID, success, nonEmptyEffectError(success, message))
			return
		}
	}
}

func closeFirstEffectByKind(
	transition *Transition,
	kind EffectKind,
	success bool,
	message string,
) {
	for _, effectID := range sortedEffectIDs(transition.State.PendingEffects) {
		if transition.State.PendingEffects[effectID].Kind == kind {
			_ = finishEffect(transition, effectID, success, nonEmptyEffectError(success, message))
			return
		}
	}
}

func closeEffectsByKind(
	transition *Transition,
	kind EffectKind,
	success bool,
	message string,
) {
	for _, effectID := range sortedEffectIDs(transition.State.PendingEffects) {
		if transition.State.PendingEffects[effectID].Kind == kind {
			_ = finishEffect(transition, effectID, success, nonEmptyEffectError(success, message))
		}
	}
}

func sortedEffectIDs(effects map[string]Effect) []string {
	ids := make([]string, 0, len(effects))
	for effectID := range effects {
		ids = append(ids, effectID)
	}
	slices.Sort(ids)
	return ids
}

func nonEmptyEffectError(success bool, message string) string {
	if success || strings.TrimSpace(message) != "" {
		return message
	}
	return "effect failed"
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

func changedPaths(changes []ObservedChange) []string {
	unique := make(map[string]struct{}, len(changes))
	for _, change := range changes {
		unique[change.Path] = struct{}{}
	}
	paths := make([]string, 0, len(unique))
	for path := range unique {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return paths
}

func samePaths(left, right []string) bool {
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}
