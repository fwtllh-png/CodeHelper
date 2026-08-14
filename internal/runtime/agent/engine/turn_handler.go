package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnexec"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func (e *Engine) Run(
	ctx context.Context, prompt string, emit func(Event) error,
) (Result, error) {
	return e.RunForTurn(ctx, "", prompt, emit)
}

func (e *Engine) RunForTurn(
	ctx context.Context, turnID, prompt string, emit func(Event) error,
) (result Result, resultErr error) {
	return e.RunForTurnWithAttachments(ctx, turnID, prompt, nil, emit)
}

// RunForTurnWithAttachments accepts Runtime-verified native images.
func (e *Engine) RunForTurnWithAttachments(
	ctx context.Context,
	turnID, prompt string,
	attachments []provider.Attachment,
	emit func(Event) error,
) (result Result, resultErr error) {
	return e.RunForTurnWithIntentAndAttachments(
		ctx,
		turnID,
		prompt,
		protocol.TurnIntentAnswer,
		attachments,
		emit,
	)
}

// RunForTurnWithIntentAndAttachments uses a host-supplied completion contract.
func (e *Engine) RunForTurnWithIntentAndAttachments(
	ctx context.Context,
	turnID, prompt string,
	intent protocol.TurnIntent,
	attachments []provider.Attachment,
	emit func(Event) error,
) (result Result, resultErr error) {
	return e.RunForTurnWithRequest(
		ctx,
		turnID,
		TurnRequest{
			Prompt: prompt, Intent: intent, Attachments: attachments,
		},
		emit,
	)
}

// RunForTurnWithRequest starts a Turn with host-validated recovery metadata.
func (e *Engine) RunForTurnWithRequest(
	ctx context.Context,
	turnID string,
	request TurnRequest,
	emit func(Event) error,
) (result Result, resultErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	spec, persistedTurnID, err := e.prepareTurnSpec(
		turnID,
		request,
	)
	if err != nil {
		return Result{}, err
	}
	factory := scopeFactory{
		engine: e, emit: emit, persistedTurnID: persistedTurnID,
	}
	scope, err := factory.Open(ctx, spec)
	if err != nil {
		return Result{}, err
	}
	defer scope.Close(context.WithoutCancel(ctx))
	return scope.Run(ctx)
}

func (e *Engine) prepareTurnSpec(
	turnID string,
	request TurnRequest,
) (TurnSpec, string, error) {
	persistedTurnID := turnID
	if request.Prompt == "" {
		return TurnSpec{}, "", errors.New("prompt is required")
	}
	request.Intent = protocol.NormalizeTurnIntent(request.Intent)
	if !request.Intent.Valid() {
		return TurnSpec{}, "", protocol.NewProblem(
			protocol.CodeInvalidArgument,
			fmt.Sprintf("turn intent %q is invalid", request.Intent),
			false,
			nil,
		)
	}
	if request.Recovery != nil {
		if err := request.Recovery.Validate(); err != nil {
			return TurnSpec{}, "", protocol.NewProblem(
				protocol.CodeInvalidArgument,
				err.Error(),
				false,
				err,
			)
		}
	}
	if turnID == "" {
		turnID = fmt.Sprintf("engine-turn-%d", e.turn+1)
	}
	spec, err := SnapshotTurnSpec(
		e.options,
		TurnIdentity{
			SessionID: e.options.SessionID, TurnID: turnID,
			ProfileRevision: e.options.ProfileRevision,
		},
		request,
	)
	if err != nil {
		return TurnSpec{}, "", err
	}
	spec.History = cloneMessages(e.history)
	spec.World = contextstore.CloneWorldBaseline(e.world)
	spec.Window = contextstore.CloneWindowLedger(e.window)
	return spec, persistedTurnID, nil
}

type executionScope = turnexec.Scope[TurnSpec, Result, ScopeSnapshot]

type scopeFactory struct {
	engine          *Engine
	emit            func(Event) error
	persistedTurnID string
}

func (f scopeFactory) Open(
	_ context.Context,
	spec TurnSpec,
) (*executionScope, error) {
	return f.open(spec)
}

func (f scopeFactory) open(spec TurnSpec) (*executionScope, error) {
	if f.engine == nil {
		return nil, errors.New("turn scope engine is required")
	}
	emit := f.emit
	if emit == nil {
		emit = func(Event) error { return nil }
	}
	scope := &Scope{
		engine: f.engine, spec: spec, emit: emit,
		persistedTurnID: f.persistedTurnID,
		state:           newScopeState(f.engine),
	}
	scope.state.world = contextstore.CloneWorldBaseline(spec.World)
	scope.state.window = contextstore.CloneWindowLedger(spec.Window)
	f.engine.publishScope(scope)
	f.engine.attachPending(scope)
	return turnexec.NewScope(
		scope.Spec(),
		scope.Run,
		scope.Control(),
		scope.Snapshot,
		func(context.Context) error { scope.Close(); return nil },
	)
}

// Run owns one frozen TurnSpec.
func (s *Scope) Run(ctx context.Context) (result Result, resultErr error) {
	e := s.engine
	spec := s.spec
	emit := s.emit
	turnID := spec.Identity.TurnID
	prompt := spec.Request.Prompt
	intent := spec.Request.Intent
	attachments := spec.Request.Attachments
	persistedTurnID := s.persistedTurnID
	releaseWorkspace, err := e.options.WorkspaceTurnGate.Acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer releaseWorkspace()

	recorder, turnSpan := e.beginTrace(spec.Purpose)
	defer func() {
		e.endTrace(context.WithoutCancel(ctx), recorder, turnSpan, persistedTurnID, result.State)
	}()
	draftTurnID := ""
	if e.journal != nil &&
		spec.Request.Recovery != nil &&
		spec.Request.Recovery.Action == protocol.TurnRecoveryContinue {
		sourceTurnID := string(spec.Request.Recovery.SourceTurnID)
		switch {
		case e.journal.HasDraft(sourceTurnID):
			draftTurnID = sourceTurnID
		case e.journal.HasDraft(turnID):
			// A restarted recovery Turn owns the same draft under its new ID.
			draftTurnID = turnID
		}
	}
	draftResumed := draftTurnID != ""
	var (
		draftChanges       []workspacejournal.Change
		kernelDraftChanges []turnkernel.ObservedChange
	)
	if draftResumed {
		draftChanges = e.journal.DraftChanges(draftTurnID)
		kernelDraftChanges = make(
			[]turnkernel.ObservedChange,
			0,
			len(draftChanges),
		)
		for _, change := range draftChanges {
			kernelDraftChanges = append(
				kernelDraftChanges,
				turnkernel.ObservedChange{
					Path: change.Path, Kind: change.Kind,
				},
			)
		}
	}
	kernel, err := newEngineTurnKernelForTurn(
		kernelTurnIdentity{
			turnID:          turnID,
			profileRevision: spec.Identity.ProfileRevision,
		},
		intent,
		string(spec.Mode),
		spec.Request.Recovery,
		draftResumed,
		kernelDraftChanges,
		recorder,
		turnSpan.ID(),
		e.options.TurnKernelObserver,
		e.options.Metrics,
		spec.Kernel,
		e.options.TurnCoordinatorRuntime,
	)
	if err != nil {
		return result, err
	}
	s.mu.Lock()
	s.state.kernel = kernel
	s.mu.Unlock()
	defer func() {
		_ = e.options.TurnCoordinatorRuntime.Release(
			context.WithoutCancel(ctx),
			turnID,
		)
	}()
	if len(spec.Skills) != 0 {
		names := make([]string, 0, len(spec.Skills))
		for _, summary := range spec.Skills {
			names = append(names, summary.Name)
		}
		ctx = skilltool.WithAllowedNames(ctx, names)
	}
	if e.guard != nil && spec.Policy != nil {
		sessionPolicy := e.guard.SwapPolicy(spec.Policy)
		defer e.guard.SwapPolicy(sessionPolicy)
	}
	if e.options.Hooks != nil {
		if err := e.options.Hooks.MessageSubmit(ctx, hooks.MessageSubmitInput{
			SessionID: e.options.SessionID, TurnID: turnID, Message: prompt,
		}); err != nil {
			return Result{}, err
		}
		defer func() {
			status := string(result.State)
			if status == "" {
				status = string(Failed)
			}
			e.options.Hooks.TurnEnd(context.WithoutCancel(ctx), hooks.TurnEndInput{
				SessionID: e.options.SessionID, TurnID: turnID, Status: status,
			})
		}()
	}
	e.setApprovalEmit(func(event Event) error {
		event.State, event.Turn = AwaitingApproval, e.turn
		return emit(event)
	})
	defer e.setApprovalEmit(nil)
	disconnectInput := e.connectInputHost(kernel, emit)
	defer disconnectInput()
	e.options.Metrics.AgentTurn()
	e.turn++
	result.Turn = e.turn
	kernelTerminalFinalized := false
	kernelTerminalStarted := false
	journalRevert := false
	e.evidenceSet().BeginTurn(e.turn)
	_, restoredTerminal := kernel.terminalDecision()
	if e.journal != nil && !restoredTerminal {
		var journalErr error
		switch {
		case draftResumed:
			journalErr = e.journal.ResumeDraft(draftTurnID, turnID)
		case spec.Request.Recovery != nil &&
			spec.Request.Recovery.Action == protocol.TurnRecoveryRetry &&
			e.journal.HasDraft(string(spec.Request.Recovery.SourceTurnID)):
			_, journalErr = e.journal.Revert(
				context.Background(),
				string(spec.Request.Recovery.SourceTurnID),
			)
			if journalErr == nil {
				journalErr = e.journal.Begin(turnID)
			}
		default:
			journalErr = e.journal.Begin(turnID)
		}
		if journalErr != nil {
			return result, journalErr
		}
		for _, change := range draftChanges {
			s.state.diff.Record(TurnDiffEntry{
				Path: change.Path, Tool: "recovery_draft", Kind: change.Kind,
			})
			e.observePath(workingset.SourceEdited, change.Path)
			e.observeChangeEvidence(toolguard.FileChange{
				Path: change.Path, Kind: change.Kind,
			})
		}
	}
	transaction := cloneMessages(e.history)
	terminal := newTurnEmitter(e.turn, emit)
	terminal.setCommitted(e.applySessionDelta)
	terminal.setCancelReason(func() string {
		if reason := kernel.cancellationReason(); reason != "" {
			return reason
		}
		return e.cancellationReason()
	})
	terminal.setTerminalDecision(kernel.terminalDecision)
	send := terminal.send
	defer terminal.finish(ctx, &result, &resultErr)
	contextFinalized := false
	defer func() {
		if contextFinalized {
			return
		}
		canceled := errors.Is(resultErr, context.Canceled) ||
			errors.Is(ctx.Err(), context.Canceled)
		var decision *policy.DecisionError
		if errors.As(resultErr, &decision) &&
			decision.Code == "approval_canceled" {
			canceled = true
		}
		if canceled &&
			e.cancellationReason() != protocol.CancelReasonUserInterrupted {
			canceled = false
		}
		terminal.setPrimary(resultErr)
		snapshot, err := e.finalizeTerminalContext(
			transaction, false, canceled, provider.Usage{}, 0, send,
		)
		terminal.setContextBudget(snapshot)
		if err != nil {
			terminal.addSecondary("terminal_context", err)
			resultErr = errors.Join(resultErr, err)
		}
	}()
	finalizeKernel := func(
		request turnkernel.TerminalRequested,
		resumed *turnkernel.TerminalDecision,
	) error {
		if kernelTerminalFinalized {
			return nil
		}
		if resumed == nil {
			if request.FailureMessage != "" || request.CancelReason != "" {
				reason := request.FailureMessage
				if reason == "" {
					reason = request.CancelReason
				}
				if err := kernel.abortForTerminal(reason); err != nil {
					return err
				}
				if err := kernel.discardOutput("terminal_failure"); err != nil {
					return err
				}
			}
			_, err := kernel.requestTerminal(request)
			if err != nil {
				return err
			}
			kernelTerminalStarted = true
		}
		kind, hasJournal := kernel.journalEffectKind()
		if hasJournal {
			effect, err := kernel.startJournal(kind)
			if err != nil {
				return err
			}
			var receipt workspacejournal.Receipt
			var journalErr error
			switch kind {
			case turnkernel.EffectCommitJournal:
				if e.journal == nil {
					journalErr = errors.New("workspace journal is unavailable")
				} else {
					journalErr = e.journal.Commit(turnID)
				}
			case turnkernel.EffectSuspendJournal:
				if e.journal == nil {
					journalErr = errors.New("workspace journal is unavailable")
				} else {
					journalErr = e.journal.Suspend(turnID)
				}
				if result.Verification != nil {
					result.Verification.Workspace = &VerificationWorkspace{
						Status: "draft",
						Note:   "workspace changes are retained as a resumable, unverified draft",
					}
				}
			case turnkernel.EffectRollbackJournal:
				if e.journal == nil {
					journalErr = errors.New("workspace journal is unavailable")
				} else {
					receipt, journalErr = e.journal.Rollback(
						context.Background(),
						turnID,
					)
				}
				if result.Verification != nil {
					result.Verification.Workspace = verificationWorkspace(receipt)
				}
				e.recordRollbackConflicts(receipt)
			default:
				journalErr = fmt.Errorf("unsupported journal effect %q", kind)
			}
			status := turnkernel.JournalRolledBack
			switch kind {
			case turnkernel.EffectCommitJournal:
				status = turnkernel.JournalCommitted
			case turnkernel.EffectSuspendJournal:
				status = turnkernel.JournalSuspended
			}
			if err := kernel.finishJournal(
				effect,
				status,
				journalErr,
			); err != nil {
				return errors.Join(journalErr, err)
			}
			if journalErr != nil {
				return journalErr
			}
		}
		if err := kernel.finishTerminal(); err != nil {
			return err
		}
		kernelTerminalFinalized = true
		return nil
	}
	defer func() {
		if kernelTerminalFinalized || kernelTerminalStarted {
			return
		}
		_, _, request := terminal.terminalRequest(ctx, resultErr)
		if err := finalizeKernel(request, nil); err != nil {
			terminal.addSecondary("journal", err)
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if decision, resuming := kernel.committingDecision(); resuming {
		kernelTerminalStarted = true
		contextFinalized = true
		terminal.setContextBudget(ContextBudgetSnapshot{})
		if err := finalizeKernel(
			turnkernel.TerminalRequested{},
			&decision,
		); err != nil {
			return result, err
		}
		switch decision.Kind {
		case turnkernel.TerminalCompleted:
			result.Text = kernel.frozenOutput()
			result.State = Completed
			if err := send(Completed, Event{Text: result.Text}); err != nil {
				return result, err
			}
			return result, nil
		case turnkernel.TerminalCanceled:
			result.State = Canceled
			if err := send(Canceled, Event{
				CancelReason: decision.Message,
			}); err != nil {
				return result, err
			}
			return result, nil
		default:
			result.State = Failed
			if err := send(Failed, Event{Error: decision.Message}); err != nil {
				return result, err
			}
			return result, nil
		}
	}
	if decision, terminalized := kernel.terminalDecision(); terminalized {
		kernelTerminalStarted = true
		kernelTerminalFinalized = true
		contextFinalized = true
		terminal.setContextBudget(ContextBudgetSnapshot{})
		switch decision.Kind {
		case turnkernel.TerminalCompleted:
			result.Text = kernel.frozenOutput()
			result.State = Completed
			if err := send(Completed, Event{Text: result.Text}); err != nil {
				return result, err
			}
		case turnkernel.TerminalCanceled:
			result.State = Canceled
			if err := send(Canceled, Event{
				CancelReason: decision.Message,
			}); err != nil {
				return result, err
			}
		default:
			result.State = Failed
			if err := send(Failed, Event{
				ErrorCode: protocol.ErrorCode(decision.Code),
				Error:     decision.Message,
			}); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if err := send(Preparing, Event{
		Provider: spec.Provider, Model: spec.Model,
		Purpose: string(spec.Purpose),
		Mode:    string(spec.Mode), Posture: string(spec.Posture),
		Workspace:          spec.Workspace,
		WorkspaceIsolation: e.options.WorkspaceIsolation,
		Sandbox:            spec.Sandbox,
	}); err != nil {
		return result, err
	}
	user := provider.TextMessage(provider.RoleUser, prompt)
	for index := range attachments {
		attachment := attachments[index]
		user.Blocks = append(user.Blocks, provider.ContentBlock{
			Type: provider.ContentImage, Attachment: &attachment,
		})
	}
	user.Turn = e.turn
	transaction = append(transaction, user)
	executed := make(map[string]tool.Result)
	cache := &toolResultCache{}
	var finalText string
	progress := kernel.progressObservation()
	if recoveredCalls := kernel.pendingToolCalls(); len(recoveredCalls) != 0 {
		blocks := make([]provider.ContentBlock, 0, len(recoveredCalls))
		for _, call := range recoveredCalls {
			callCopy := call
			blocks = append(blocks, provider.ContentBlock{
				Type: provider.ContentToolCall, ToolCall: &callCopy,
			})
		}
		transaction = append(
			transaction,
			provider.ProducedAssistant(spec.Route, blocks, e.turn, nil),
		)
		toolCtx := ctx
		if progress.stage == turnkernel.ProgressStageFinishOnly {
			toolCtx = withFinishOnly(ctx)
		}
		results, err := e.runToolsWithCache(
			toolCtx,
			turnID,
			recoveredCalls,
			executed,
			cache,
			kernel,
			send,
		)
		if err != nil {
			return result, err
		}
		for index, call := range recoveredCalls {
			data, err := json.Marshal(tool.ModelResult(call.Name, results[index]))
			if err != nil {
				return result, err
			}
			transaction = append(transaction, provider.Message{
				Role: provider.RoleTool, Turn: e.turn,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolResult,
					ToolResult: &provider.ToolResult{
						CallID: call.ID, Content: string(data),
						IsError: results[index].IsError,
					},
				}},
			})
		}
	}
	// Tool-model usage is accounted separately from this route.
	var sampled provider.Usage
	var toolSpent toolSpend
	toolSpent.known = true
	gate := &verifyGate{
		engine: e,
		kernel: kernel,
	}
	sampleReason := promptcontext.SampleNormal
	invalidateCompletion := func(reason string) error {
		current := kernel.completion()
		if current == nil || !current.Accepted {
			return nil
		}
		return kernel.invalidateCompletion(reason)
	}
	for step := 0; step <
		spec.Limits.MaxSteps+kernel.repairStepTotal(); step++ {
		if e.appendSteering(&transaction) && kernel.completion() != nil {
			if err := invalidateCompletion("turn_steered"); err != nil {
				return result, err
			}
		}
		if remaining := stepBudgetWarningRemaining(spec.Limits.MaxSteps, step); remaining > 0 {
			transaction = append(transaction, stepBudgetFeedback(e.turn, remaining))
		}
		progress, err = kernel.observeProgress(e.progressSignature(kernel))
		if err != nil {
			return result, err
		}
		if progress.stage == turnkernel.ProgressStageExhausted {
			return result, noProgressProblem(progress)
		}
		if progress.stageChanged &&
			progress.stage != turnkernel.ProgressStageNone {
			transaction = append(
				transaction,
				noProgressFeedback(e.turn, progress),
			)
		}
		sampleID := kernel.pendingSampleID()
		if sampleID == "" {
			sampleID = fmt.Sprintf("turn-%d-step-%d", e.turn, step+1)
			for kernel.hasSample(sampleID) {
				sampleID += "-recovered"
			}
		}
		if err := send(CallingModel, Event{
			ModelExecution: &ModelExecution{
				Kind: "model_sample", SampleID: sampleID,
				Reason: sampleReason,
			},
		}); err != nil {
			return result, err
		}
		if err := kernel.beginModelSample(ctx, sampleID); err != nil {
			return result, err
		}
		var modelOutputContinued bool
		var pendingInputInjected bool
		var modelReplay *provider.ReplayState
		modelSend := func(state State, event Event) error {
			if event.ProviderRetry != nil {
				if err := kernel.providerRetry(
					sampleID,
					*event.ProviderRetry,
				); err != nil {
					return err
				}
				if err := kernel.beginModelSample(ctx, sampleID); err != nil {
					return err
				}
			}
			if state == Streaming &&
				event.Block != nil &&
				event.Block.Type == provider.ContentText {
				return nil
			}
			return send(state, event)
		}
		blocks, calls, usage, _, err := e.modelStep(
			ctx,
			&transaction,
			result.Usage,
			sampleID,
			sampleReason,
			kernel.providerRetries(sampleID),
			progress.stage == turnkernel.ProgressStageFinishOnly &&
				turnkernel.IsResearchIntent(kernel.intent()),
			&modelOutputContinued,
			&pendingInputInjected,
			&modelReplay,
			modelSend,
		)
		sampleReason = promptcontext.SampleNormal
		if finishErr := kernel.finishModelSample(
			sampleID,
			blocksText(blocks),
			calls,
			usage,
			modelOutputContinued,
			err,
		); finishErr != nil {
			return result, errors.Join(err, finishErr)
		}
		if err != nil {
			return result, err
		}
		if pendingInputInjected && kernel.completion() != nil {
			if err := invalidateCompletion("input_injected"); err != nil {
				return result, err
			}
		}
		result.Usage.Add(usage)
		sampled.Add(usage)
		text := blocksText(blocks)
		result.Reasoning += blocksReasoning(blocks)
		for _, block := range blocks {
			if block.Type == provider.ContentSearch && block.Search != nil {
				result.Searches = append(result.Searches, *block.Search)
			}
			if block.Type == provider.ContentCitation && block.Citation != nil {
				result.Citations = append(result.Citations, *block.Citation)
			}
		}
		if len(calls) == 0 {
			if e.appendSteering(&transaction) {
				if kernel.completion() != nil {
					if err := invalidateCompletion("turn_steered"); err != nil {
						return result, err
					}
				}
				if len(blocks) != 0 {
					transaction = append(
						transaction,
						provider.ProducedAssistant(
							spec.Route, blocks, e.turn, modelReplay,
						),
					)
				}
				finalText += text
				continue
			}
			transaction = append(
				transaction,
				provider.ProducedAssistant(
					spec.Route, blocks, e.turn, modelReplay,
				),
			)
			var outcome verifyOutcome
			action, actionErr := kernel.evaluateTurnStep(
				kernel.repairProgressKey(),
			)
			if actionErr != nil {
				var exhausted *turnkernel.RepairBudgetExhaustedError
				if errors.As(actionErr, &exhausted) &&
					exhausted.Kind == turnkernel.RepairWorkspace {
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						"workspace_change turn produced no observed workspace changes",
						false,
						actionErr,
					)
				}
				if errors.Is(actionErr, turnkernel.ErrRepairBudgetExhausted) {
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						"turn repair made no progress",
						true,
						actionErr,
					)
				}
				return result, actionErr
			}
			switch action {
			case turnkernel.StepActionRepairToolFailure:
				if err := kernel.discardOutput("tool_failure_repair"); err != nil {
					return result, err
				}
				transaction = append(transaction, toolFailureCompletionFeedback(e.turn))
				sampleReason = promptcontext.SampleToolFailureRepair
				continue
			case turnkernel.StepActionRepairCompletion:
				if err := kernel.discardOutput("completion_repair"); err != nil {
					return result, err
				}
				transaction = append(transaction, completionFeedback(e.turn))
				sampleReason = promptcontext.SampleCompletionRepair
				continue
			case turnkernel.StepActionRepairWorkspace:
				if err := kernel.discardOutput("workspace_change_repair"); err != nil {
					return result, err
				}
				transaction = append(
					transaction,
					workspaceChangeRequiredFeedback(e.turn),
				)
				sampleReason = promptcontext.SampleWorkspaceRepair
				continue
			case turnkernel.StepActionRepairDeclaration:
				if err := kernel.discardOutput("completion_declaration_repair"); err != nil {
					return result, err
				}
				transaction = append(
					transaction,
					completionDeclarationFeedback(e.turn),
				)
				sampleReason = promptcontext.SampleDeclarationRepair
				continue
			case turnkernel.StepActionVerify:
				outcome, err = gate.evaluate(ctx, send)
				if err != nil {
					return result, err
				}
				result.Verification = outcome.receipt
				switch outcome.action {
				case verifyActionRepair:
					if err := kernel.discardOutput("verification_repair"); err != nil {
						return result, err
					}
					transaction = append(
						transaction, verifyFeedback(outcome.receipt, e.turn),
					)
					sampleReason = promptcontext.SampleVerificationRepair
					continue
				case verifyActionBlocked, verifyActionFailed:
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						outcome.receipt.problemMessage(),
						false,
						nil,
					)
				}
			case turnkernel.StepActionComplete:
			default:
				return result, protocol.NewProblem(
					protocol.CodeInternal,
					fmt.Sprintf("kernel returned unsupported step action %q", action),
					false,
					nil,
				)
			}
			finalText += text
			result.Verification = outcome.receipt
			if err := kernel.validateFinalReadiness(); err != nil {
				return result, err
			}
			pricing := e.activeRoute().Model().Pricing
			cost := estimateCost(pricing, sampled) + toolSpent.cost
			costKnown := pricingKnown(pricing, sampled) &&
				(toolSpent.samples == 0 || toolSpent.known)
			result.CostUSD = cost

			result.Text, result.State = finalText, Completed
			journalRevert = outcome.action == verifyActionReverted
			if e.journal == nil && journalRevert {
				return result, errors.New("verification requested rollback without a workspace journal")
			}
			if outcome.receipt != nil && outcome.receipt.Workspace == nil {
				outcome.receipt.Workspace = &VerificationWorkspace{Status: "changed"}
			}
			snapshot, err := e.finalizeTerminalContext(
				transaction, true, false, result.Usage, cost, send,
			)
			if err != nil {
				return result, err
			}
			contextFinalized = true
			terminal.setContextBudget(snapshot)
			if _, err := kernel.releaseOutput(); err != nil {
				return result, err
			}
			if err := finalizeKernel(
				turnkernel.TerminalRequested{},
				nil,
			); err != nil {
				return result, err
			}
			if !journalRevert && e.journal != nil {
				e.turnIDs[turnID] = e.turn
			}
			if err := send(Completed, Event{
				Text: finalText, Usage: &result.Usage, CostUSD: cost,
				CostKnown: costKnown, Verification: outcome.receipt,
				Completion: kernel.completionDeclaration(),
			}); err != nil {
				contextFinalized = false
				return result, err
			}
			return result, nil
		}
		if err := send(PreparingTools, Event{}); err != nil {
			return result, err
		}
		for _, call := range calls {
			callCopy := call
			blocks = append(blocks, provider.ContentBlock{Type: provider.ContentToolCall, ToolCall: &callCopy})
		}
		transaction = append(
			transaction,
			provider.ProducedAssistant(
				spec.Route, blocks, e.turn, modelReplay,
			),
		)
		toolCtx := ctx
		if progress.stage == turnkernel.ProgressStageFinishOnly {
			toolCtx = withFinishOnly(ctx)
		}
		results, err := e.runToolsWithCache(
			toolCtx,
			turnID,
			calls,
			executed,
			cache,
			kernel,
			send,
		)

		spend := e.drainToolSpend()
		result.Usage.Add(spend.usage)
		toolSpent.usage.Add(spend.usage)
		toolSpent.cost += spend.cost
		toolSpent.samples += spend.samples
		if spend.samples != 0 {
			toolSpent.known = toolSpent.known && spend.known
		}
		if err != nil {
			return result, err
		}
		result.Tools = append(result.Tools, calls...)
		if err := send(FeedingResults, Event{}); err != nil {
			return result, err
		}
		for index, call := range calls {
			data, err := json.Marshal(tool.ModelResult(call.Name, results[index]))
			if err != nil {
				return result, err
			}
			transaction = append(transaction, provider.Message{
				Role: provider.RoleTool, Turn: e.turn,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolResult,
					ToolResult: &provider.ToolResult{
						CallID: call.ID, Content: string(data), IsError: results[index].IsError,
					},
				}},
			})
		}
	}
	return result, protocol.NewProblem(
		protocol.CodeResourceExhausted,
		fmt.Sprintf(
			"engine exceeded %d steps (raise execution.max_steps, CODEHELPER_MAX_STEPS, or --max-steps)",
			spec.Limits.MaxSteps,
		),
		false,
		nil,
	)
}

const maxCompletionRepairs = 2
const maxWorkspaceChangeRepairs = 1
const maxDeclarationRepairs = 2

func stepBudgetWarningRemaining(maxSteps, step int) int {
	if maxSteps < 64 {
		return 0
	}
	warning := min(32, max(16, maxSteps/4))
	if step != maxSteps-warning {
		return 0
	}
	return warning
}

func stepBudgetFeedback(turn uint64, remaining int) provider.Message {
	message := provider.TextMessage(
		provider.RoleUser,
		fmt.Sprintf(
			"[step_budget]\nremaining_steps=%d\nhard_limit=true\n"+
				"Prioritize the requested deliverable now. Stop broad exploration, "+
				"finish the smallest coherent verified result, and call turn_complete. "+
				"If required work cannot fit, call turn_complete with status=incomplete "+
				"and concrete pending_actions instead of waiting for forced termination.",
			remaining,
		),
	)
	message.Turn = turn
	return message
}

func contextWindowFeedback(turn uint64) provider.Message {
	message := provider.TextMessage(provider.RoleUser,
		"[context_window]\nStop broad exploration. Complete the smallest coherent "+
			"verified result and declare any concrete remaining work.")
	message.Turn = turn
	return message
}

func workspaceChangeRequiredFeedback(turn uint64) provider.Message {
	message := provider.TextMessage(
		provider.RoleUser,
		"[completion_check]\n"+
			"required_action=perform_workspace_mutation\n"+
			"observed_changes=0\n"+
			"retry_original=false\n"+
			"The workspace_change contract is not complete. Use a guarded mutation tool, "+
			"then verify the observed changed paths before answering.",
	)
	message.Turn = turn
	return message
}

func completionDeclarationFeedback(turn uint64) provider.Message {
	message := provider.TextMessage(
		provider.RoleUser,
		"[completion_declaration_required]\n"+
			"required_action=turn_complete\n"+
			"retry_original=false\n"+
			"Report the actual work state through turn_complete. Use status=complete, "+
			"a non-empty summary, and pending_actions=[] only when every requested action "+
			"is finished. If any work remains, use status=incomplete and list each concrete "+
			"pending action; the runtime will continue this same turn. "+
			"The runtime binds any changed paths and accepted verification evidence automatically. "+
			"Do not move requested work to a future turn.",
	)
	message.Turn = turn
	return message
}

func completionFeedback(turn uint64) provider.Message {
	message := provider.TextMessage(provider.RoleUser, `[completion_required]
Your previous response did not contain a complete user-facing final answer.
Do not stop at reasoning or narration of future work. Either call the required
tool now, or provide a concise final answer based on the available evidence.`)
	message.Turn = turn
	return message
}

func toolFailureCompletionFeedback(turn uint64) provider.Message {
	message := provider.TextMessage(provider.RoleUser, `[tool_failure_resolution_required]
The latest tool batch contained an explicit failure. Do not stop after
describing a future retry. Either call the required tool now to resolve the
failure, or provide a concise final answer that clearly reports the unresolved
failure and its impact.`)
	message.Turn = turn
	return message
}

func errorText(err error) string {
	if err == nil {
		return "turn failed"
	}
	return err.Error()
}

func verificationWorkspace(receipt workspacejournal.Receipt) *VerificationWorkspace {
	conflicts := make([]string, 0, len(receipt.Conflicts))
	for _, conflict := range receipt.Conflicts {
		conflicts = append(conflicts, conflict.Path)
	}
	status := "restored"
	if len(conflicts) != 0 {
		status = "conflicted"
	}
	return &VerificationWorkspace{
		Status: status, Restored: append([]string(nil), receipt.Restored...),
		Conflicts:                  conflicts,
		NonFileSideEffectsReverted: receipt.NonFileSideEffectsReverted,
		Note:                       receipt.NonFileSideEffectsNote,
	}
}
