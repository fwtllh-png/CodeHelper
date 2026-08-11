package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// engineTurnKernel adapts the existing Engine loop to the authoritative
// TurnCoordinator. The state field is a read-only snapshot refreshed only
// after Coordinator accepts a durable transition.
type engineTurnKernel struct {
	mu          sync.Mutex
	state       turnkernel.State
	coordinator *turnkernel.TurnCoordinator
	dispatcher  *turnkernel.DurableEffectDispatcher
	recorder    *trace.Recorder
	parent      uint64
	sink        func(turnkernel.TransitionRecord)
	metrics     *telemetry.Metrics
	restored    bool
}

type kernelTurnIdentity struct {
	turnID          string
	profileRevision uint64
}

func newEngineTurnKernelForTurn(
	identity kernelTurnIdentity,
	intent protocol.TurnIntent,
	mode string,
	recorder *trace.Recorder,
	parent uint64,
	sink func(turnkernel.TransitionRecord),
	metrics *telemetry.Metrics,
	policy turnkernel.Policy,
	runtime turnkernel.CoordinatorRuntime,
) (*engineTurnKernel, error) {
	turnID := identity.turnID
	profileRevision := identity.profileRevision
	state := turnkernel.NewStateWithPolicy(
		intent,
		mode,
		profileRevision,
		policy,
	)
	if runtime == nil {
		return nil, fmt.Errorf("turn coordinator runtime is nil")
	}
	handle, err := runtime.Open(
		context.Background(),
		turnID,
		state,
	)
	if err != nil {
		return nil, err
	}
	coordinator := handle.Coordinator
	dispatcher := handle.Dispatcher
	kernel := &engineTurnKernel{
		state:       coordinator.Snapshot(),
		coordinator: coordinator,
		dispatcher:  dispatcher,
		recorder:    recorder,
		parent:      parent,
		sink:        sink,
		metrics:     metrics,
		restored:    handle.Restored,
	}
	if handle.Restored {
		return kernel, nil
	}
	if err := kernel.applyAuthoritative(turnkernel.StartTurn{}); err != nil {
		return nil, err
	}
	if err := kernel.applyAuthoritative(
		turnkernel.PreparationFinished{},
	); err != nil {
		return nil, err
	}
	return kernel, nil
}

func (s *engineTurnKernel) pendingSampleID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, effect := range s.dispatcher.PendingRouted(
		turnkernel.EffectSampleProvider,
	) {
		return effect.CallID
	}
	return ""
}

func (s *engineTurnKernel) hasSample(sampleID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, exists := s.state.SampleLedger[sampleID]
	return exists
}

func (s *engineTurnKernel) committingDecision() (
	turnkernel.TerminalDecision,
	bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Phase != turnkernel.PhaseCommitting ||
		s.state.PendingTerminal == nil {
		return turnkernel.TerminalDecision{}, false
	}
	return *s.state.PendingTerminal, true
}

func (s *engineTurnKernel) frozenOutput() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.state.FinalOutput, "")
}

func (s *engineTurnKernel) terminalDecision() (
	turnkernel.TerminalDecision,
	bool,
) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Phase.Terminal() || s.state.Terminal == nil {
		return turnkernel.TerminalDecision{}, false
	}
	return *s.state.Terminal, true
}

func (s *engineTurnKernel) pendingToolCalls() []provider.ToolCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	effects := s.dispatcher.PendingRouted(turnkernel.EffectExecuteTool)
	calls := make([]provider.ToolCall, 0, len(effects))
	for _, effect := range effects {
		call, ok := s.state.OpenCalls[effect.CallID]
		if !ok {
			continue
		}
		calls = append(calls, provider.ToolCall{
			ID: call.ID, Name: call.Name, Arguments: call.Arguments,
			CatalogID:         call.CatalogID,
			CatalogGeneration: call.CatalogGeneration,
			CatalogRevision:   call.CatalogRevision,
			CatalogAuthority:  call.CatalogAuthority,
		})
	}
	return calls
}

func (s *engineTurnKernel) startTools(calls []provider.ToolCall) error {
	if s == nil || len(calls) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	states := make([]turnkernel.ToolCallState, 0, len(calls))
	for _, call := range calls {
		if existing, ok := s.state.OpenCalls[call.ID]; ok {
			if existing.Name != call.Name {
				return fmt.Errorf(
					"tool call %q name changed from %q to %q",
					call.ID,
					existing.Name,
					call.Name,
				)
			}
			continue
		}
		states = append(states, turnkernel.ToolCallState{
			ID: call.ID, Name: call.Name,
			Arguments:         call.Arguments,
			CatalogID:         call.CatalogID,
			CatalogGeneration: call.CatalogGeneration,
			CatalogRevision:   call.CatalogRevision,
			CatalogAuthority:  call.CatalogAuthority,
		})
	}
	if len(states) == 0 {
		return nil
	}
	return s.applyAuthoritativeLocked(turnkernel.ToolCallsProposed{Calls: states})
}

func (s *engineTurnKernel) startTool(callID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.state.Phase
	effect, err := s.dispatcher.Start(
		turnkernel.EffectExecuteTool,
		callID,
	)
	if err != nil {
		return err
	}
	s.recordAcceptedLocked(turnkernel.EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	return nil
}

func (s *engineTurnKernel) validateToolStarts(
	calls []provider.ToolCall,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := make(map[string]struct{}, len(calls))
	for _, call := range calls {
		if call.ID == "" || call.Name == "" {
			return protocol.NewProblem(
				protocol.CodeConflict,
				"tool call identity is incomplete",
				false,
				nil,
			)
		}
		if _, duplicate := seen[call.ID]; duplicate {
			return protocol.NewProblem(
				protocol.CodeConflict,
				fmt.Sprintf("duplicate tool call id %q", call.ID),
				false,
				nil,
			)
		}
		seen[call.ID] = struct{}{}
		if open, exists := s.state.OpenCalls[call.ID]; exists {
			status := turnkernel.EffectLifecycleStatus("")
			for _, effect := range s.state.PendingEffects {
				if effect.Kind == turnkernel.EffectExecuteTool &&
					effect.CallID == call.ID {
					status = effect.Status
					break
				}
			}
			if open.Name != call.Name ||
				status != turnkernel.EffectRequested {
				return protocol.NewProblem(
					protocol.CodeConflict,
					fmt.Sprintf("tool call id %q is already open", call.ID),
					false,
					nil,
				)
			}
		}
		if _, closed := s.state.ClosedCalls[call.ID]; closed {
			return protocol.NewProblem(
				protocol.CodeConflict,
				fmt.Sprintf("tool call id %q is already closed", call.ID),
				false,
				nil,
			)
		}
	}
	return nil
}

func (s *engineTurnKernel) closeTool(
	call provider.ToolCall,
	result tool.Result,
	fileChanges []toolguard.FileChange,
) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	effect := routedEffectByCall(
		s.dispatcher.PendingRouted(turnkernel.EffectExecuteTool),
		call.ID,
	)
	if effect.ID == "" {
		return fmt.Errorf("tool effect for call %q is not routed", call.ID)
	}
	changes := kernelObservedChanges(fileChanges)
	command := turnkernel.ToolResultReceived{
		EffectID: effect.ID,
		CallID:   call.ID,
		IsError:  result.IsError,
		Changes:  changes,
	}
	from := s.state.Phase
	if err := s.dispatcher.Resolve(command); err != nil {
		return err
	}
	s.recordAcceptedLocked(command, from)
	return nil
}

func (s *engineTurnKernel) abortTools(reason string) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.OpenCalls) == 0 {
		return nil
	}
	for _, kind := range []turnkernel.EffectKind{
		turnkernel.EffectAwaitApproval,
		turnkernel.EffectExecuteTool,
	} {
		if err := s.dispatcher.Abort(kind, "", reason); err != nil {
			return fmt.Errorf("abort %s effect: %w", kind, err)
		}
	}
	s.state = s.coordinator.Snapshot()
	return s.applyAuthoritativeLocked(turnkernel.AbortOpenCalls{Reason: reason})
}

func (s *engineTurnKernel) mutationRevision() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.MutationRevision
}

func (s *engineTurnKernel) verificationMustPass() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Policy.VerificationMustPass
}

func (s *engineTurnKernel) completion() *turnkernel.CompletionDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Completion == nil {
		return nil
	}
	copy := *s.state.Completion
	copy.ChangedPaths = append([]string(nil), copy.ChangedPaths...)
	copy.QualityCalls = append([]string(nil), copy.QualityCalls...)
	return &copy
}

func (s *engineTurnKernel) completionDeclaration() *tool.CompletionDeclaration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Completion == nil || !s.state.Completion.Accepted {
		return nil
	}
	decision := s.state.Completion
	return &tool.CompletionDeclaration{
		Status:              "complete",
		Summary:             decision.Summary,
		ChangedPaths:        append([]string(nil), decision.ChangedPaths...),
		VerificationCallIDs: append([]string(nil), decision.QualityCalls...),
		MutationRevision:    decision.Mutation,
		CallID:              decision.CompletionCall,
	}
}

func (s *engineTurnKernel) evaluateCompletion(
	candidate turnkernel.CompletionCandidate,
) (turnkernel.CompletionDecision, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyAuthoritativeLocked(turnkernel.CompletionEvaluated{
		Candidate: candidate,
	}); err != nil {
		return turnkernel.CompletionDecision{}, err
	}
	if s.state.Completion == nil {
		return turnkernel.CompletionDecision{}, errors.New(
			"reducer did not produce a completion decision",
		)
	}
	decision := *s.state.Completion
	decision.ChangedPaths = append([]string(nil), decision.ChangedPaths...)
	decision.QualityCalls = append([]string(nil), decision.QualityCalls...)
	return decision, nil
}

func (s *engineTurnKernel) invalidateCompletion(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyAuthoritativeLocked(turnkernel.CompletionInvalidated{
		Reason: reason,
	})
}

func (s *engineTurnKernel) requireApproval(
	requestID string,
	callID string,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyAuthoritativeLocked(turnkernel.ApprovalRequired{
		RequestID: requestID,
		CallID:    callID,
	}); err != nil {
		return err
	}
	from := s.state.Phase
	effect, err := s.dispatcher.Start(
		turnkernel.EffectAwaitApproval,
		callID,
	)
	if err != nil {
		return err
	}
	s.recordAcceptedLocked(turnkernel.EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	return nil
}

func (s *engineTurnKernel) resolveApproval(
	requestID string,
	canceled bool,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, ok := s.state.PendingApprovals[requestID]
	if !ok {
		return fmt.Errorf("approval request %q is not pending", requestID)
	}
	effect, started, err := s.dispatcher.Routed(
		turnkernel.EffectAwaitApproval,
		approval.CallID,
	)
	if err != nil {
		return err
	}
	if !started {
		from := s.state.Phase
		effect, err = s.dispatcher.Start(
			turnkernel.EffectAwaitApproval,
			approval.CallID,
		)
		if err != nil {
			return err
		}
		s.recordAcceptedLocked(turnkernel.EffectStarted{
			EffectID: effect.ID,
			Attempt:  effect.Attempt,
		}, from)
	}
	command := turnkernel.ApprovalResultReceived{
		EffectID:  effect.ID,
		RequestID: requestID,
		Accepted:  true,
		Canceled:  canceled,
	}
	from := s.state.Phase
	if err := s.dispatcher.Resolve(command); err != nil {
		return err
	}
	s.recordAcceptedLocked(command, from)
	return nil
}

func (s *engineTurnKernel) requireInput(requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.applyAuthoritativeLocked(turnkernel.InputRequired{
		RequestID: requestID,
	}); err != nil {
		return err
	}
	from := s.state.Phase
	effect, err := s.dispatcher.Start(turnkernel.EffectAwaitInput, "")
	if err != nil {
		return err
	}
	s.recordAcceptedLocked(turnkernel.EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	return nil
}

func (s *engineTurnKernel) resolveInput(requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.state.PendingInput
	if pending == nil || pending.RequestID != requestID {
		return fmt.Errorf("input request %q is not pending", requestID)
	}
	effect, started, err := s.dispatcher.Routed(
		turnkernel.EffectAwaitInput,
		"",
	)
	if err != nil {
		return err
	}
	if !started {
		from := s.state.Phase
		effect, err = s.dispatcher.Start(turnkernel.EffectAwaitInput, "")
		if err != nil {
			return err
		}
		s.recordAcceptedLocked(turnkernel.EffectStarted{
			EffectID: effect.ID,
			Attempt:  effect.Attempt,
		}, from)
	}
	command := turnkernel.InputResultReceived{
		EffectID:  effect.ID,
		RequestID: requestID,
		Accepted:  true,
	}
	from := s.state.Phase
	if err := s.dispatcher.Resolve(command); err != nil {
		return err
	}
	s.recordAcceptedLocked(command, from)
	return nil
}

func (s *engineTurnKernel) requestCancel(reason string) error {
	return s.applyAuthoritative(turnkernel.CancelRequested{Reason: reason})
}

func (s *engineTurnKernel) cancellationReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Cancellation.Accepted {
		return ""
	}
	return s.state.Cancellation.Reason
}

func (s *engineTurnKernel) beginVerification() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyAuthoritativeLocked(turnkernel.VerificationStarted{})
}

func (s *engineTurnKernel) finishVerification(
	command turnkernel.VerificationFinished,
) (turnkernel.VerificationAction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := s.state.Phase
	effect, err := s.dispatcher.Start(
		turnkernel.EffectRunVerification,
		"",
	)
	if err != nil {
		return "", err
	}
	s.recordAcceptedLocked(turnkernel.EffectStarted{
		EffectID: effect.ID,
		Attempt:  effect.Attempt,
	}, from)
	command.EffectID = effect.ID
	from = s.state.Phase
	if err := s.dispatcher.Resolve(command); err != nil {
		return "", err
	}
	s.recordAcceptedLocked(command, from)
	return s.state.Verification.Action, nil
}

func (s *engineTurnKernel) bufferOutput(text string) error {
	if text == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyAuthoritativeLocked(turnkernel.ModelTextReceived{Text: text})
}

func (s *engineTurnKernel) releaseOutput() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	output := append([]string(nil), s.state.ProvisionalOutput...)
	if err := s.applyAuthoritativeLocked(
		turnkernel.ReleaseProvisionalOutput{},
	); err != nil {
		return nil, err
	}
	return output, nil
}

func (s *engineTurnKernel) discardOutput(reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.state.ProvisionalOutput) == 0 {
		return nil
	}
	return s.applyAuthoritativeLocked(turnkernel.DiscardProvisionalOutput{
		Reason: reason,
	})
}

func (s *engineTurnKernel) repairSteps(
	kind turnkernel.RepairKind,
) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.RepairBudgets[kind].Steps
}

func (s *engineTurnKernel) validateFinalReadiness() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Policy.CompletionRequired &&
		s.state.MutationRevision != 0 &&
		(s.state.Completion == nil || !s.state.Completion.Accepted) {
		return protocol.NewProblem(
			protocol.CodeConflict,
			"kernel final readiness requires accepted completion",
			false,
			nil,
		)
	}
	if s.state.Policy.VerificationRequired &&
		s.state.MutationRevision != 0 &&
		(s.state.Verification.Mutation != s.state.MutationRevision ||
			(s.state.Verification.Action != turnkernel.VerificationActionPassed &&
				s.state.Verification.Action != turnkernel.VerificationActionReported &&
				s.state.Verification.Action != turnkernel.VerificationActionReverted)) {
		return protocol.NewProblem(
			protocol.CodeConflict,
			"kernel final readiness requires passed verification",
			false,
			nil,
		)
	}
	return nil
}

func (s *engineTurnKernel) repairStepTotal() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := uint32(0)
	for _, budget := range s.state.RepairBudgets {
		total += budget.Steps
	}
	return int(total)
}

func (s *engineTurnKernel) repairProgressKey() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	completionCall := ""
	if s.state.Completion != nil {
		completionCall = s.state.Completion.CompletionCall
	}
	return fmt.Sprintf(
		"mutation=%d;closed=%d;completion=%s;verification=%s",
		s.state.MutationRevision,
		len(s.state.ClosedCalls),
		completionCall,
		s.state.Verification.Status,
	)
}

func kernelObservedChanges(
	fileChanges []toolguard.FileChange,
) []turnkernel.ObservedChange {
	changes := make([]turnkernel.ObservedChange, 0, len(fileChanges))
	for _, change := range fileChanges {
		changes = append(changes, turnkernel.ObservedChange{
			Path: change.Path, Kind: string(change.Kind),
		})
	}
	return changes
}

func (s *engineTurnKernel) recordAcceptedLocked(
	command turnkernel.Command,
	from turnkernel.Phase,
) {
	s.state = s.coordinator.Snapshot()
	digest, err := turnkernel.Digest(s.state)
	record := turnkernel.TransitionRecord{
		Command: turnkernel.CommandName(command),
		From:    from,
		To:      s.state.Phase,
	}
	if err != nil {
		record.Drift = err.Error()
	} else {
		record.StateDigest = digest
	}
	s.recordLocked(record)
}

func (s *engineTurnKernel) applyAuthoritative(
	command turnkernel.Command,
) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.applyAuthoritativeLocked(command)
}

func (s *engineTurnKernel) applyAuthoritativeLocked(
	command turnkernel.Command,
) error {
	from := s.state.Phase
	err := s.coordinator.Submit(context.Background(), command)
	record := turnkernel.TransitionRecord{
		Command: turnkernel.CommandName(command),
		From:    from,
		To:      from,
	}
	if err != nil {
		record.Rejection = err.Error()
		record.StateDigest, _ = turnkernel.Digest(s.state)
		s.recordLocked(record)
		code := protocol.CodeInternal
		retryable := false
		switch command.(type) {
		case turnkernel.ToolCallsProposed:
			code = protocol.CodeConflict
			retryable = true
		}
		return protocol.NewProblem(
			code,
			"turn kernel rejected authoritative command: "+err.Error(),
			retryable,
			err,
		)
	}
	s.state = s.coordinator.Snapshot()
	record.To = s.state.Phase
	record.StateDigest, err = turnkernel.Digest(s.state)
	if err != nil {
		record.Drift = err.Error()
		s.recordLocked(record)
		return protocol.NewProblem(
			protocol.CodeInternal,
			"turn kernel could not digest authoritative command",
			false,
			err,
		)
	}
	s.recordLocked(record)
	return nil
}

func (s *engineTurnKernel) recordLocked(record turnkernel.TransitionRecord) {
	s.metrics.TurnKernelObserver(
		record.Drift != "",
		record.StateDigest == "" && record.Drift != "",
	)
	span := s.recorder.Start(trace.NameTurnKernelTransition, s.parent, map[string]any{
		"command":      record.Command,
		"from_phase":   string(record.From),
		"to_phase":     string(record.To),
		"state_digest": record.StateDigest,
		"drift":        record.Drift,
		"rejection":    record.Rejection,
	})
	status := trace.StatusOK
	if record.Drift != "" || record.Rejection != "" {
		status = trace.StatusError
	}
	span.End(status)
	if s.sink == nil {
		return
	}
	func() {
		defer func() {
			if recover() != nil {
				_ = debug.Stack()
			}
		}()
		s.sink(record)
	}()
}
