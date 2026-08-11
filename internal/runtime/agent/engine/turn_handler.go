package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
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

// RunForTurnWithAttachments starts one turn with native image content blocks.
// Attachments have already been workspace-bound and digest-verified by Runtime.
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

// RunForTurnWithIntentAndAttachments starts one turn under an explicit
// completion contract. Intent is host-supplied structured data, never inferred
// from prompt text.
func (e *Engine) RunForTurnWithIntentAndAttachments(
	ctx context.Context,
	turnID, prompt string,
	intent protocol.TurnIntent,
	attachments []provider.Attachment,
	emit func(Event) error,
) (result Result, resultErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if prompt == "" {
		return Result{}, errors.New("prompt is required")
	}
	intent = protocol.NormalizeTurnIntent(intent)
	if !intent.Valid() {
		return Result{}, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			fmt.Sprintf("turn intent %q is invalid", intent),
			false,
			nil,
		)
	}
	if emit == nil {
		emit = func(Event) error { return nil }
	}
	if err := e.beginTurn(); err != nil {
		return Result{}, err
	}
	defer e.endTurn()
	releaseWorkspace, err := e.options.WorkspaceTurnGate.Acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer releaseWorkspace()

	persistedTurnID := turnID
	if turnID == "" {
		turnID = fmt.Sprintf("engine-turn-%d", e.turn+1)
	}

	turnContext, err := SnapshotTurnContext(e.options, turnID)
	if err != nil {
		return result, err
	}
	e.setTurnRoute(turnContext.Route)
	defer e.clearTurnRoute()

	recorder, turnSpan := e.beginTrace(turnContext.Purpose)
	defer func() {
		e.endTrace(context.WithoutCancel(ctx), recorder, turnSpan, persistedTurnID, result.State)
	}()
	kernel, err := newEngineTurnKernelForTurn(
		kernelTurnIdentity{
			turnID:          turnID,
			profileRevision: e.options.ProfileRevision,
		},
		intent,
		string(turnContext.Mode),
		recorder,
		turnSpan.ID(),
		e.options.TurnKernelObserver,
		e.options.Metrics,
		turnkernel.Policy{
			CompletionRequired: e.options.RequireCompletionDeclaration,
			VerificationRequired: e.options.Verify.enabled() ||
				protocol.NormalizeTurnIntent(intent) ==
					protocol.TurnIntentWorkspaceChange ||
				e.options.RequireCompletionDeclaration,
			VerificationMustPass: protocol.NormalizeTurnIntent(intent) ==
				protocol.TurnIntentWorkspaceChange ||
				e.options.RequireCompletionDeclaration,
			VerificationMode:        e.options.Verify.Mode,
			VerificationOnFailure:   e.options.Verify.OnFailure,
			CompletionRepairLimit:   maxCompletionRepairs,
			WorkspaceRepairLimit:    maxWorkspaceChangeRepairs,
			DeclarationRepairLimit:  maxDeclarationRepairs,
			VerificationRepairLimit: uint32(max(e.options.Verify.MaxRepairSteps, 0)),
			JournalRequired:         e.journal != nil,
		},
		e.options.TurnCoordinatorRuntime,
	)
	if err != nil {
		return result, err
	}
	e.setTurnKernel(kernel)
	defer e.clearTurnKernel(kernel)
	defer func() {
		_ = e.options.TurnCoordinatorRuntime.Release(
			context.WithoutCancel(ctx),
			turnID,
		)
	}()
	if e.options.SkillSnapshot != nil {
		names := make([]string, 0, len(turnContext.Skills))
		for _, summary := range turnContext.Skills {
			names = append(names, summary.Name)
		}
		ctx = skilltool.WithAllowedNames(ctx, names)
	}
	if e.guard != nil && turnContext.Policy != nil {
		sessionPolicy := e.guard.SwapPolicy(turnContext.Policy)
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
	e.resetToolSpend()
	result.Turn = e.turn
	kernelTerminalFinalized := false
	kernelTerminalStarted := false
	journalRevert := false
	e.turnDiff.Reset()
	e.resetTurnDiagnostics()
	e.resetVerificationEvidence()
	e.resetRollbackConflicts()
	e.evidence.BeginTurn(e.turn)
	if e.journal != nil {
		if err := e.journal.Begin(turnID); err != nil {
			return result, err
		}
	}
	transaction := cloneMessages(e.history)
	terminal := newTurnEmitter(e.turn, emit)
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
			transaction, false, canceled, send,
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
			if kind == turnkernel.EffectCommitJournal {
				status = turnkernel.JournalCommitted
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
	if err := send(Preparing, Event{
		Provider: turnContext.Provider, Model: turnContext.Model,
		Purpose: string(turnContext.Purpose),
		Mode:    string(turnContext.Mode), Posture: string(turnContext.Posture),
		Workspace:          turnContext.Workspace,
		WorkspaceIsolation: e.options.WorkspaceIsolation,
		Sandbox:            turnContext.Sandbox,
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
	if err := e.runPreSamplingCompactGate(&transaction, send); err != nil {
		return result, err
	}
	executed := make(map[string]tool.Result)
	cache := &toolResultCache{}
	var finalText string
	if recoveredCalls := kernel.pendingToolCalls(); len(recoveredCalls) != 0 {
		blocks := make([]provider.ContentBlock, 0, len(recoveredCalls))
		for _, call := range recoveredCalls {
			callCopy := call
			blocks = append(blocks, provider.ContentBlock{
				Type: provider.ContentToolCall, ToolCall: &callCopy,
			})
		}
		transaction = append(transaction, provider.Message{
			Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
		})
		results, err := e.runToolsWithCache(
			ctx,
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
			data, err := json.Marshal(results[index])
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
	// sampled is what the turn's own sampling used, kept apart from what its
	// tools sampled: the turn is priced at its own route's rates, and a tool's
	// tokens belong to whichever model that tool used.
	var sampled provider.Usage
	var toolSpent toolSpend
	toolSpent.known = true
	gate := &verifyGate{
		engine: e,
		kernel: kernel,
	}
	invalidateCompletion := func(reason string) error {
		current := kernel.completion()
		if current == nil || !current.Accepted {
			return nil
		}
		return kernel.invalidateCompletion(reason)
	}
	for step := 0; step <
		e.options.MaxSteps+kernel.repairStepTotal(); step++ {
		if e.appendSteering(&transaction) && kernel.completion() != nil {
			if err := invalidateCompletion("turn_steered"); err != nil {
				return result, err
			}
		}
		if err := send(CallingModel, Event{}); err != nil {
			return result, err
		}
		sampleID := kernel.pendingSampleID()
		if sampleID == "" {
			sampleID = fmt.Sprintf("turn-%d-step-%d", e.turn, step+1)
			for kernel.hasSample(sampleID) {
				sampleID += "-recovered"
			}
		}
		if err := kernel.beginModelSample(sampleID); err != nil {
			return result, err
		}
		var modelOutputContinued bool
		var pendingInputInjected bool
		modelSend := func(state State, event Event) error {
			if event.ProviderRetry != nil {
				if err := kernel.providerRetry(
					sampleID,
					event.ProviderRetry.Category,
				); err != nil {
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
		blocks, calls, usage, estimatedInput, err := e.modelStep(
			ctx,
			&transaction,
			result.Usage,
			&modelOutputContinued,
			&pendingInputInjected,
			modelSend,
		)
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
		if estimatedInput > result.EstimatedInputTokens {
			result.EstimatedInputTokens = estimatedInput
		}
		text := blocksText(blocks)
		result.Reasoning += blocksReasoning(blocks)
		result.ReasoningSignature += blocksSignature(blocks)
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
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
				}
				finalText += text
				continue
			}
			transaction = append(transaction, provider.Message{
				Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
			})
			if err := e.runMidTurnCompactGate(&transaction, send); err != nil {
				return result, err
			}
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
				continue
			case turnkernel.StepActionRepairCompletion:
				if err := kernel.discardOutput("completion_repair"); err != nil {
					return result, err
				}
				transaction = append(transaction, completionFeedback(e.turn))
				continue
			case turnkernel.StepActionRepairWorkspace:
				if err := kernel.discardOutput("workspace_change_repair"); err != nil {
					return result, err
				}
				transaction = append(
					transaction,
					workspaceChangeRequiredFeedback(e.turn),
				)
				continue
			case turnkernel.StepActionRepairDeclaration:
				if err := kernel.discardOutput("completion_declaration_repair"); err != nil {
					return result, err
				}
				transaction = append(
					transaction,
					completionDeclarationFeedback(e.turn),
				)
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
					continue
				case verifyActionFailed:
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
			costKnown := pricing.Known && (toolSpent.samples == 0 || toolSpent.known)
			result.CostUSD = cost

			result.InputTokenDelta = int64(sampled.InputTokens) - int64(result.EstimatedInputTokens)
			result.Text, result.State = finalText, Completed
			journalRevert = outcome.action == verifyActionReverted
			if e.journal == nil && journalRevert {
				return result, errors.New("verification requested rollback without a workspace journal")
			}
			if outcome.receipt != nil && outcome.receipt.Workspace == nil {
				outcome.receipt.Workspace = &VerificationWorkspace{Status: "changed"}
			}
			previousHistory := cloneMessages(e.history)
			snapshot, err := e.finalizeTerminalContext(
				transaction, true, false, send,
			)
			if err != nil {
				return result, err
			}
			contextFinalized = true
			terminal.setContextBudget(snapshot)
			if e.completionGateRequired(intent) {
				if _, err := kernel.releaseOutput(); err != nil {
					return result, err
				}
			} else {
				if _, err := kernel.releaseOutput(); err != nil {
					return result, err
				}
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
				CostKnown:            costKnown,
				EstimatedInputTokens: result.EstimatedInputTokens,
				InputTokenDelta:      result.InputTokenDelta,
				Verification:         outcome.receipt,
				Completion:           kernel.completionDeclaration(),
			}); err != nil {
				e.history = previousHistory
				contextFinalized = false
				return result, err
			}
			e.usage.Add(result.Usage)
			e.costUSD += cost
			return result, nil
		}
		if err := send(PreparingTools, Event{}); err != nil {
			return result, err
		}
		for _, call := range calls {
			callCopy := call
			blocks = append(blocks, provider.ContentBlock{Type: provider.ContentToolCall, ToolCall: &callCopy})
		}
		transaction = append(transaction, provider.Message{
			Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
		})
		results, err := e.runToolsWithCache(
			ctx,
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
			data, err := json.Marshal(results[index])
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
		if err := e.runMidTurnCompactGate(&transaction, send); err != nil {
			return result, err
		}
	}
	return result, protocol.NewProblem(
		protocol.CodeResourceExhausted,
		fmt.Sprintf(
			"engine exceeded %d steps (raise execution.max_steps, CODEHELPER_MAX_STEPS, or --max-steps)",
			e.options.MaxSteps,
		),
		false,
		nil,
	)
}

const maxCompletionRepairs = 2
const maxWorkspaceChangeRepairs = 1
const maxDeclarationRepairs = 2

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
			"Call turn_complete with status=complete and a non-empty summary. "+
			"The runtime binds changed paths and accepted verification evidence automatically. "+
			"Do not describe future work; execute every pending action before declaring completion.",
	)
	message.Turn = turn
	return message
}

func completionVerifiedFeedback(turn uint64) provider.Message {
	message := provider.TextMessage(
		provider.RoleUser,
		"[completion_verified]\n"+
			"required_action=final_answer\n"+
			"pending_actions=0\n"+
			"Structured completion and verification passed. Provide one concise "+
			"user-facing final answer without calling another tool.",
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

func emitState(send func(State, Event) error) func(State, Event) error {
	return send
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
