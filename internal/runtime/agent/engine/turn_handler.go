package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
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
	if e.options.InputHost != nil {
		e.options.InputHost.SetEmitter(func(ctx context.Context, request interact.Request) error {
			copy := request
			return emit(Event{State: AwaitingInput, Turn: e.turn, Input: &copy})
		})
		defer e.options.InputHost.SetEmitter(nil)
	}
	e.options.Metrics.AgentTurn()
	e.turn++
	e.resetToolSpend()
	result.Turn = e.turn
	journalCommitted := false
	journalRolledBack := false
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
	terminal := newTerminalHandler(e.turn, emit)
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
	if e.journal != nil {
		defer func() {
			if journalRolledBack {
				return
			}
			var receipt workspacejournal.Receipt
			var rollbackErr error
			if journalCommitted {
				if resultErr == nil {
					return
				}
				receipt, rollbackErr = e.journal.Revert(context.Background(), turnID)
			} else {
				receipt, rollbackErr = e.journal.Rollback(context.Background(), turnID)
			}
			if result.Verification != nil {
				result.Verification.Workspace = verificationWorkspace(receipt)
			}
			e.recordRollbackConflicts(receipt)
			if rollbackErr != nil {
				resultErr = errors.Join(resultErr, fmt.Errorf(
					"automatic workspace rollback (%d restored, %d conflicts): %w",
					len(receipt.Restored), len(receipt.Conflicts), rollbackErr,
				))
			}
		}()
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
	replay := &toolReplayCache{}
	var finalText string
	// sampled is what the turn's own sampling used, kept apart from what its
	// tools sampled: the turn is priced at its own route's rates, and a tool's
	// tokens belong to whichever model that tool used.
	var sampled provider.Usage
	var toolSpent toolSpend
	toolSpent.known = true
	gate := &verifyGate{
		engine:        e,
		requirePassed: intent == protocol.TurnIntentWorkspaceChange,
	}
	completionRepairSteps := 0
	completionRepairNoProgress := 0
	workspaceChangeRepairs := 0
	declarationRepairSteps := 0
	declarationNoProgress := 0
	declarationProgressKey := e.completionProgressKey()
	unresolvedToolFailure := false
	recoveryToolSucceeded := false
	var completionVerification *verifyOutcome
	var verifiedCompletionCallID string
	var verifiedCompletionRevision uint64
	invalidateCompletion := func() {
		e.clearCompletionDeclaration()
		completionVerification = nil
		verifiedCompletionCallID = ""
		verifiedCompletionRevision = 0
	}
	for step := 0; step <
		e.options.MaxSteps+gate.extraSteps()+completionRepairSteps+
			workspaceChangeRepairs+declarationRepairSteps; step++ {
		if e.appendSteering(&transaction) && e.completionDeclaration != nil {
			invalidateCompletion()
		}
		if err := send(CallingModel, Event{}); err != nil {
			return result, err
		}
		var modelOutputContinued bool
		var pendingInputInjected bool
		blocks, calls, usage, estimatedInput, err := e.modelStep(
			ctx,
			&transaction,
			result.Usage,
			&modelOutputContinued,
			&pendingInputInjected,
			emitState(send),
		)
		if err != nil {
			return result, err
		}
		if pendingInputInjected && e.completionDeclaration != nil {
			invalidateCompletion()
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
				if e.completionDeclaration != nil {
					invalidateCompletion()
				}
				if len(blocks) != 0 {
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
				}
				finalText += text
				continue
			}
			if unresolvedToolFailure {
				if completionRepairNoProgress >= maxCompletionRepairs {
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						"model stopped after a tool failure without resolving it or producing a complete final answer",
						true,
						nil,
					)
				}
				if len(blocks) != 0 {
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
				}
				transaction = append(transaction, toolFailureCompletionFeedback(e.turn))
				if recoveryToolSucceeded ||
					intent == protocol.TurnIntentAnswer ||
					intent == protocol.TurnIntentPlan {
					unresolvedToolFailure = false
					recoveryToolSucceeded = false
				}
				completionRepairNoProgress++
				completionRepairSteps++
				continue
			}
			if modelOutputContinued && len(result.Tools) != 0 {
				if completionRepairNoProgress >= maxCompletionRepairs {
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						"model stopped after an interrupted post-tool response without producing a complete final answer",
						true,
						nil,
					)
				}
				if len(blocks) != 0 {
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
				}
				transaction = append(transaction, completionFeedback(e.turn))
				completionRepairNoProgress++
				completionRepairSteps++
				continue
			}
			if strings.TrimSpace(text) == "" {
				if completionRepairNoProgress >= maxCompletionRepairs {
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						"model stopped without producing a complete final answer",
						true,
						nil,
					)
				}
				if len(blocks) != 0 {
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
				}
				transaction = append(transaction, completionFeedback(e.turn))
				completionRepairNoProgress++
				completionRepairSteps++
				continue
			}
			if intent == protocol.TurnIntentWorkspaceChange &&
				len(e.TurnDiff()) == 0 {
				if workspaceChangeRepairs < maxWorkspaceChangeRepairs {
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
					transaction = append(
						transaction,
						workspaceChangeRequiredFeedback(e.turn),
					)
					workspaceChangeRepairs++
					continue
				}
				return result, protocol.NewProblem(
					protocol.CodeConflict,
					"workspace_change turn produced no observed workspace changes",
					false,
					nil,
				)
			}
			if intent == protocol.TurnIntentWorkspaceChange &&
				e.options.RequireCompletionDeclaration &&
				!e.hasCurrentCompletionDeclaration() {
				progressKey := e.completionProgressKey()
				if progressKey != declarationProgressKey {
					declarationProgressKey = progressKey
					declarationNoProgress = 0
				}
				if declarationNoProgress >= maxDeclarationRepairs {
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						"completion_repair_no_progress: workspace_change completion requires an accepted turn_complete declaration",
						true,
						nil,
					)
				}
				if len(blocks) != 0 {
					transaction = append(transaction, provider.Message{
						Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
					})
				}
				transaction = append(
					transaction,
					completionDeclarationFeedback(e.turn),
				)
				declarationNoProgress++
				declarationRepairSteps++
				continue
			}
			transaction = append(transaction, provider.Message{
				Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
			})
			finalText += text
			if err := e.runMidTurnCompactGate(&transaction, send); err != nil {
				return result, err
			}
			var outcome verifyOutcome
			if intent == protocol.TurnIntentWorkspaceChange &&
				e.options.RequireCompletionDeclaration {
				if completionVerification == nil ||
					e.completionDeclaration == nil ||
					e.completionDeclaration.CallID != verifiedCompletionCallID ||
					e.completionDeclaration.MutationRevision != verifiedCompletionRevision {
					return result, protocol.NewProblem(
						protocol.CodeConflict,
						"workspace_change final answer was not preverified",
						true,
						nil,
					)
				}
				outcome = *completionVerification
			} else {
				outcome, err = gate.evaluate(ctx, send)
				if err != nil {
					return result, err
				}
				result.Verification = outcome.receipt
				switch outcome.action {
				case verifyActionRepair:
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
			}
			result.Verification = outcome.receipt
			if intent == protocol.TurnIntentWorkspaceChange &&
				(outcome.action != verifyActionPassed ||
					outcome.receipt == nil ||
					outcome.receipt.Status != verify.StatusPassed) {
				message := "workspace_change completion requires a passed verification receipt"
				if outcome.receipt != nil && outcome.receipt.Message != "" {
					message += ": " + outcome.receipt.Message
				}
				return result, protocol.NewProblem(
					protocol.CodeConflict, message, false, nil,
				)
			}
			pricing := e.activeRoute().Model().Pricing
			cost := estimateCost(pricing, sampled) + toolSpent.cost
			costKnown := pricing.Known && (toolSpent.samples == 0 || toolSpent.known)
			result.CostUSD = cost

			result.InputTokenDelta = int64(sampled.InputTokens) - int64(result.EstimatedInputTokens)
			result.Text, result.State = finalText, Completed
			if e.journal != nil {

				if outcome.action == verifyActionReverted {
					receipt, err := e.journal.Rollback(context.Background(), turnID)
					outcome.receipt.Workspace = verificationWorkspace(receipt)
					e.recordRollbackConflicts(receipt)
					if err != nil {
						return result, fmt.Errorf(
							"verification rollback (%d restored, %d conflicts): %w",
							len(receipt.Restored), len(receipt.Conflicts), err,
						)
					}
					journalRolledBack = true
				} else {
					if err := e.journal.Commit(turnID); err != nil {
						return result, err
					}
					journalCommitted = true
					e.turnIDs[turnID] = e.turn
				}
			} else if outcome.action == verifyActionReverted {
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
			if err := send(Completed, Event{
				Text: finalText, Usage: &result.Usage, CostUSD: cost,
				CostKnown:            costKnown,
				EstimatedInputTokens: result.EstimatedInputTokens,
				InputTokenDelta:      result.InputTokenDelta,
				Verification:         outcome.receipt,
				Completion:           e.completionDeclaration,
			}); err != nil {
				e.history = previousHistory
				contextFinalized = false
				return result, err
			}
			e.usage.Add(result.Usage)
			e.costUSD += cost
			return result, nil
		}
		completionRepairNoProgress = 0
		if err := send(PreparingTools, Event{}); err != nil {
			return result, err
		}
		if e.completionDeclaration != nil {
			invalidateCompletion()
		}
		for _, call := range calls {
			callCopy := call
			blocks = append(blocks, provider.ContentBlock{Type: provider.ContentToolCall, ToolCall: &callCopy})
		}
		transaction = append(transaction, provider.Message{
			Role: provider.RoleAssistant, Blocks: blocks, Turn: e.turn,
		})
		results, err := e.runToolsWithReplay(ctx, turnID, calls, executed, replay, send)

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
		batchFailed := false
		for _, toolResult := range results {
			if toolResult.IsError {
				batchFailed = true
			}
		}
		if batchFailed {
			unresolvedToolFailure = true
			recoveryToolSucceeded = false
		} else if unresolvedToolFailure {
			recoveryToolSucceeded = true
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
		if intent == protocol.TurnIntentWorkspaceChange &&
			e.options.RequireCompletionDeclaration &&
			e.hasCurrentCompletionDeclaration() {
			outcome, err := gate.evaluate(ctx, send)
			if err != nil {
				return result, err
			}
			result.Verification = outcome.receipt
			switch outcome.action {
			case verifyActionRepair:
				if outcome.receipt != nil &&
					outcome.receipt.Scope == verify.ScopeQuality &&
					outcome.receipt.Status == verify.StatusUnavailable {
					e.qualityEvidenceRequired = true
				}
				e.clearCompletionDeclaration()
				completionVerification = nil
				verifiedCompletionCallID = ""
				verifiedCompletionRevision = 0
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
			completionVerification = &outcome
			verifiedCompletionCallID = e.completionDeclaration.CallID
			verifiedCompletionRevision = e.completionDeclaration.MutationRevision
			transaction = append(
				transaction, completionVerifiedFeedback(e.turn),
			)
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
			"Call turn_complete with status=complete and pending_actions=[]. "+
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
