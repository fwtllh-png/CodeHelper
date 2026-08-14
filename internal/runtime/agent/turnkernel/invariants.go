package turnkernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func Validate(state State) error {
	if !validPhase(state.Phase) {
		return fmt.Errorf("invalid phase %q", state.Phase)
	}
	if state.Intent == "" {
		return errors.New("turn intent is empty")
	}
	if state.OpenCalls == nil ||
		state.ClosedCalls == nil ||
		state.PendingApprovals == nil ||
		state.SampleLedger == nil ||
		state.PendingEffects == nil ||
		state.CompletedEffects == nil ||
		state.RepairBudgets == nil {
		return errors.New("turn ledgers are nil")
	}
	for sampleID, sample := range state.SampleLedger {
		if strings.TrimSpace(sampleID) == "" ||
			sample.ID != sampleID {
			return fmt.Errorf("invalid model sample %q", sampleID)
		}
		switch sample.Status {
		case SampleRequested:
		case SampleRunning, SampleCompleted, SampleFailed:
			if sample.Attempt == 0 {
				return fmt.Errorf("model sample %q has no attempt", sampleID)
			}
		default:
			return fmt.Errorf("invalid model sample status %q", sample.Status)
		}
		if sample.Status == SampleFailed && strings.TrimSpace(sample.Error) == "" {
			return fmt.Errorf("failed model sample %q has no error", sampleID)
		}
		if sample.Status != SampleFailed && sample.Error != "" &&
			sample.Status != SampleRunning {
			return fmt.Errorf("successful model sample %q has an error", sampleID)
		}
	}
	if state.ActiveSampleID != "" {
		sample, ok := state.SampleLedger[state.ActiveSampleID]
		if !ok || sample.Status != SampleRunning || state.Phase != PhaseSampling {
			return errors.New("active model sample is invalid")
		}
	}
	for effectID, effect := range state.PendingEffects {
		if err := validateEffect(effectID, effect, false); err != nil {
			return err
		}
		if _, completed := state.CompletedEffects[effectID]; completed {
			return fmt.Errorf("effect %q is both pending and completed", effectID)
		}
	}
	for effectID, effect := range state.CompletedEffects {
		if err := validateEffect(effectID, effect, true); err != nil {
			return err
		}
	}
	if state.Cancellation.Accepted && !state.Cancellation.Requested {
		return errors.New("accepted cancellation was not requested")
	}
	if state.Cancellation.Requested &&
		strings.TrimSpace(state.Cancellation.Reason) == "" {
		return errors.New("cancellation reason is empty")
	}
	if state.RecoveryRelation != nil &&
		(strings.TrimSpace(state.RecoveryRelation.SourceTurnID) == "" ||
			strings.TrimSpace(state.RecoveryRelation.RecoveryTurnID) == "" ||
			state.ProfileRevision == 0) {
		return errors.New("recovery relation is invalid")
	}
	if state.Usage.Frozen != state.Context.Frozen {
		return errors.New("usage and context freeze state disagree")
	}
	for kind, budget := range state.RepairBudgets {
		if !validRepairKind(kind) ||
			strings.TrimSpace(budget.ProgressKey) == "" ||
			budget.Consecutive > budget.Steps {
			return fmt.Errorf("invalid %s repair budget", kind)
		}
	}
	switch state.Progress.Stage {
	case ProgressStageNone,
		ProgressStageConverge,
		ProgressStageFinishOnly,
		ProgressStageExhausted:
	default:
		return fmt.Errorf(
			"invalid progress stage %q",
			state.Progress.Stage,
		)
	}
	if state.Progress.Signature == "" &&
		(state.Progress.ObservedSamples != 0 ||
			state.Progress.NoProgressSamples != 0 ||
			state.Progress.Stage != ProgressStageNone) {
		return errors.New("progress state has no signature")
	}
	if state.Progress.NoProgressSamples > state.Progress.ObservedSamples {
		return errors.New("no-progress samples exceed observed samples")
	}
	switch state.NextAction {
	case StepActionNone,
		StepActionRepairToolFailure,
		StepActionRepairCompletion,
		StepActionRepairWorkspace,
		StepActionRepairDeclaration,
		StepActionVerify,
		StepActionComplete:
	default:
		return fmt.Errorf("invalid next action %q", state.NextAction)
	}
	for id, call := range state.OpenCalls {
		if strings.TrimSpace(id) == "" || id != call.ID ||
			strings.TrimSpace(call.Name) == "" {
			return fmt.Errorf("invalid open tool call %q", id)
		}
		if _, closed := state.ClosedCalls[id]; closed {
			return fmt.Errorf("tool call %q is both open and closed", id)
		}
	}
	for id, result := range state.ClosedCalls {
		if strings.TrimSpace(id) == "" || id != result.ID ||
			strings.TrimSpace(result.Name) == "" {
			return fmt.Errorf("invalid closed tool call %q", id)
		}
	}
	if len(state.OpenCalls) != 0 &&
		state.Phase != PhaseExecutingTools &&
		state.Phase != PhaseAwaitingApproval &&
		state.Phase != PhaseAwaitingInput {
		return errors.New("open tool calls exist outside tool execution")
	}
	if state.Phase == PhaseExecutingTools && len(state.OpenCalls) == 0 {
		return errors.New("executing_tools has no open tool calls")
	}
	for requestID, approval := range state.PendingApprovals {
		if state.Phase != PhaseAwaitingApproval ||
			requestID == "" ||
			requestID != approval.RequestID {
			return errors.New("pending approval is invalid")
		}
		if _, ok := state.OpenCalls[approval.CallID]; !ok {
			return errors.New("pending approval does not reference an open call")
		}
	}
	if len(state.PendingApprovals) == 0 &&
		state.Phase == PhaseAwaitingApproval {
		return errors.New("awaiting_approval has no pending approval")
	}
	if state.PendingInput != nil {
		if state.Phase != PhaseAwaitingInput {
			return errors.New("pending input is outside awaiting_input")
		}
		if strings.TrimSpace(state.PendingInput.RequestID) == "" {
			return errors.New("pending input request id is empty")
		}
	} else if state.Phase == PhaseAwaitingInput {
		return errors.New("awaiting_input has no pending input")
	}
	if state.MutationRevision == 0 {
		if len(state.Changes) != 0 {
			return errors.New("changes exist without a mutation revision")
		}
		finalizingJournal := state.Policy.JournalRequired &&
			(state.Phase == PhaseCommitting || state.Phase.Terminal()) &&
			(state.Journal == JournalCommitted ||
				state.Journal == JournalRolledBack)
		if state.Journal != JournalNone && !finalizingJournal {
			return errors.New("unchanged turn has a journal")
		}
	} else {
		if len(state.Changes) == 0 {
			return errors.New("mutation revision has no changes")
		}
		if state.Journal == JournalNone {
			return errors.New("changed turn has no journal")
		}
	}
	for index, change := range state.Changes {
		if strings.TrimSpace(change.Path) == "" ||
			strings.TrimSpace(change.Kind) == "" {
			return fmt.Errorf("invalid observed change at index %d", index)
		}
	}
	if state.Completion != nil {
		if state.Completion.Accepted {
			if state.Completion.Mutation != state.MutationRevision {
				return errors.New("accepted completion is not bound to current mutation")
			}
			if strings.TrimSpace(state.Completion.CompletionCall) == "" {
				return errors.New("accepted completion has no call id")
			}
			if !samePaths(
				state.Completion.ChangedPaths,
				changedPaths(state.Changes),
			) {
				return errors.New("accepted completion paths do not match changes")
			}
		} else if strings.TrimSpace(state.Completion.Reason) == "" {
			return errors.New("rejected completion has no reason")
		}
	}
	switch state.Verification.Status {
	case VerificationNotEvaluated:
		if state.Verification.Mutation != 0 ||
			state.Verification.Action != "" {
			return errors.New("unevaluated verification has a mutation revision")
		}
	case VerificationPassed, VerificationFailed, VerificationUnavailable:
		if state.MutationRevision == 0 ||
			state.Verification.Mutation != state.MutationRevision {
			return errors.New("verification is not bound to current mutation")
		}
		switch state.Verification.Action {
		case VerificationActionPassed,
			VerificationActionRepair,
			VerificationActionReported,
			VerificationActionFailed,
			VerificationActionReverted:
		default:
			return errors.New("verification action is invalid")
		}
	default:
		return errors.New("verification status is invalid")
	}
	if state.Phase == PhaseVerifying && state.MutationRevision == 0 {
		return errors.New("verifying phase has no mutation")
	}
	if state.Phase == PhaseCommitting {
		if state.PendingTerminal == nil || state.Terminal != nil {
			return errors.New("committing phase has invalid terminal state")
		}
		if !state.Usage.Frozen || !state.Context.Frozen {
			return errors.New("committing phase has no frozen snapshot")
		}
	} else if state.PendingTerminal != nil {
		return errors.New("pending terminal exists outside committing")
	}
	if (state.Journal == JournalCommitted ||
		state.Journal == JournalRolledBack) &&
		state.Phase != PhaseCommitting &&
		!state.Phase.Terminal() {
		return errors.New("finalized journal exists outside terminal transaction")
	}
	if state.Phase.Terminal() {
		if state.Terminal == nil {
			return errors.New("terminal phase has no decision")
		}
		if len(state.OpenCalls) != 0 ||
			len(state.PendingApprovals) != 0 ||
			state.PendingInput != nil ||
			state.ActiveSampleID != "" ||
			len(state.PendingEffects) != 0 {
			return errors.New("terminal state has unfinished work")
		}
		if len(state.ProvisionalOutput) != 0 {
			return errors.New("terminal state retains provisional output")
		}
		if !state.Usage.Frozen || !state.Context.Frozen {
			return errors.New("terminal state has no frozen snapshot")
		}
		if phaseForTerminal(state.Terminal.Kind) != state.Phase {
			return errors.New("terminal phase and decision disagree")
		}
		if err := validateTerminalState(state); err != nil {
			return err
		}
	} else if state.Terminal != nil {
		return errors.New("terminal decision exists in non-terminal phase")
	}
	return nil
}

func validateTerminalState(state State) error {
	hasChanges := state.MutationRevision != 0
	switch state.Terminal.Kind {
	case TerminalCompleted:
		if !state.OutputEligibility || len(state.FinalOutput) == 0 {
			return errors.New("completed turn has no eligible final output")
		}
		if state.Intent == protocol.TurnIntentWorkspaceChange && !hasChanges {
			return errors.New("completed workspace_change has no mutation")
		}
		if !hasChanges {
			if RequiresCompletion(state) &&
				(state.Completion == nil || !state.Completion.Accepted) {
				return errors.New("completed integration turn has no accepted completion")
			}
			expected := JournalNone
			if state.Policy.JournalRequired {
				expected = JournalCommitted
			}
			if state.Journal != expected {
				return errors.New("completed unchanged turn has invalid journal state")
			}
			return nil
		}
		if RequiresCompletion(state) &&
			(state.Completion == nil ||
				!state.Completion.Accepted ||
				state.Completion.Mutation != state.MutationRevision) {
			return errors.New("completed mutation has no current completion")
		}
		if state.Policy.VerificationRequired &&
			(state.Verification.Mutation != state.MutationRevision ||
				(state.Verification.Action != VerificationActionPassed &&
					state.Verification.Action != VerificationActionReported &&
					state.Verification.Action != VerificationActionReverted)) {
			return errors.New("completed mutation has no current verification")
		}
		expectedJournal := JournalCommitted
		if state.Verification.Action == VerificationActionReverted {
			expectedJournal = JournalRolledBack
		}
		if state.Journal != expectedJournal {
			return errors.New("completed mutation has no committed journal")
		}
	case TerminalFailed, TerminalCanceled:
		if len(state.FinalOutput) != 0 {
			return errors.New("failed or canceled turn has final output")
		}
		if strings.TrimSpace(state.Terminal.Message) == "" {
			return errors.New("non-success terminal message is empty")
		}
		if hasChanges && state.Journal != JournalRolledBack {
			return errors.New("failed or canceled mutation was not rolled back")
		}
		if !hasChanges {
			expected := JournalNone
			if state.Policy.JournalRequired {
				expected = JournalRolledBack
			}
			if state.Journal != expected {
				return errors.New("failed or canceled unchanged turn has invalid journal state")
			}
		}
	}
	return nil
}

func Digest(state State) (string, error) {
	if err := Validate(state); err != nil {
		return "", err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func cloneState(state State) State {
	cloned := state
	cloned.OpenCalls = make(map[string]ToolCallState, len(state.OpenCalls))
	for id, call := range state.OpenCalls {
		cloned.OpenCalls[id] = call
	}
	cloned.ClosedCalls = make(map[string]ToolResultState, len(state.ClosedCalls))
	for id, result := range state.ClosedCalls {
		cloned.ClosedCalls[id] = result
	}
	cloned.Changes = append([]ObservedChange(nil), state.Changes...)
	cloned.ProvisionalOutput = append(
		[]string(nil),
		state.ProvisionalOutput...,
	)
	cloned.FinalOutput = append([]string(nil), state.FinalOutput...)
	cloned.Verification.EvidenceCalls = append(
		[]string(nil),
		state.Verification.EvidenceCalls...,
	)
	cloned.PendingApprovals = make(
		map[string]ApprovalState,
		len(state.PendingApprovals),
	)
	for requestID, approval := range state.PendingApprovals {
		cloned.PendingApprovals[requestID] = approval
	}
	cloned.RepairBudgets = make(
		map[RepairKind]RepairBudget,
		len(state.RepairBudgets),
	)
	for kind, budget := range state.RepairBudgets {
		cloned.RepairBudgets[kind] = budget
	}
	cloned.SampleLedger = make(
		map[string]ModelSampleState,
		len(state.SampleLedger),
	)
	for sampleID, sample := range state.SampleLedger {
		cloned.SampleLedger[sampleID] = sample
	}
	cloned.PendingEffects = make(map[string]Effect, len(state.PendingEffects))
	for effectID, effect := range state.PendingEffects {
		cloned.PendingEffects[effectID] = effect
	}
	cloned.CompletedEffects = make(
		map[string]Effect,
		len(state.CompletedEffects),
	)
	for effectID, effect := range state.CompletedEffects {
		cloned.CompletedEffects[effectID] = effect
	}
	if state.PendingInput != nil {
		value := *state.PendingInput
		cloned.PendingInput = &value
	}
	if state.Completion != nil {
		value := *state.Completion
		value.ChangedPaths = append([]string(nil), state.Completion.ChangedPaths...)
		value.QualityCalls = append([]string(nil), state.Completion.QualityCalls...)
		cloned.Completion = &value
	}
	if state.PendingTerminal != nil {
		value := *state.PendingTerminal
		cloned.PendingTerminal = &value
	}
	if state.Terminal != nil {
		value := *state.Terminal
		cloned.Terminal = &value
	}
	if state.RecoveryRelation != nil {
		value := *state.RecoveryRelation
		cloned.RecoveryRelation = &value
	}
	return cloned
}

func validateEffect(effectID string, effect Effect, completed bool) error {
	if strings.TrimSpace(effectID) == "" ||
		effect.ID != effectID ||
		effect.Kind == "" ||
		len(effect.Payload) == 0 ||
		!strings.HasPrefix(effect.PayloadDigest, "sha256:") ||
		strings.TrimSpace(effect.IdempotencyKey) == "" {
		return fmt.Errorf("invalid effect %q", effectID)
	}
	sum := sha256.Sum256(effect.Payload)
	if effect.PayloadDigest !=
		"sha256:"+hex.EncodeToString(sum[:]) {
		return fmt.Errorf("effect %q payload digest mismatch", effectID)
	}
	if completed {
		if effect.Status != EffectSucceeded && effect.Status != EffectFailed {
			return fmt.Errorf("completed effect %q has invalid status", effectID)
		}
		if effect.Status == EffectFailed && strings.TrimSpace(effect.Error) == "" {
			return fmt.Errorf("failed effect %q has no error", effectID)
		}
		return nil
	}
	if effect.Status != EffectRequested && effect.Status != EffectRunning {
		return fmt.Errorf("pending effect %q has invalid status", effectID)
	}
	if effect.Status == EffectRunning && effect.Attempt == 0 {
		return fmt.Errorf("running effect %q has no attempt", effectID)
	}
	return nil
}

func validPhase(phase Phase) bool {
	switch phase {
	case PhaseCreated,
		PhasePreparing,
		PhaseSampling,
		PhaseExecutingTools,
		PhaseAwaitingApproval,
		PhaseAwaitingInput,
		PhaseVerifying,
		PhaseCommitting,
		PhaseCompleted,
		PhaseFailed,
		PhaseCanceled:
		return true
	default:
		return false
	}
}

func phaseForTerminal(kind TerminalKind) Phase {
	switch kind {
	case TerminalCompleted:
		return PhaseCompleted
	case TerminalFailed:
		return PhaseFailed
	case TerminalCanceled:
		return PhaseCanceled
	default:
		return ""
	}
}
