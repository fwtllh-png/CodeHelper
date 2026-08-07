package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
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
	e.mu.Lock()
	defer e.mu.Unlock()
	if prompt == "" {
		return Result{}, errors.New("prompt is required")
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
	e.resetRollbackConflicts()
	e.evidence.BeginTurn(e.turn)
	if e.journal != nil {
		if err := e.journal.Begin(turnID); err != nil {
			return result, err
		}
	}
	transaction := cloneMessages(e.history)
	defer func() {
		if result.State == Canceled &&
			e.cancellationReason() == protocol.CancelReasonUserInterrupted {
			e.history = retainCanceledHistory(transaction)
		}
	}()
	terminal := newTerminalHandler(e.turn, emit)
	send := terminal.send
	defer terminal.finish(ctx, &result, &resultErr)
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
		Workspace: turnContext.Workspace, Sandbox: turnContext.Sandbox,
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
	var finalText string
	// sampled is what the turn's own sampling used, kept apart from what its
	// tools sampled: the turn is priced at its own route's rates, and a tool's
	// tokens belong to whichever model that tool used.
	var sampled provider.Usage
	var toolSpent toolSpend
	toolSpent.known = true
	gate := &verifyGate{engine: e}
	for step := 0; step < e.options.MaxSteps+gate.extraSteps(); step++ {
		e.appendSteering(&transaction)
		if err := send(CallingModel, Event{}); err != nil {
			return result, err
		}
		blocks, calls, usage, estimatedInput, err := e.modelStep(
			ctx, &transaction, result.Usage, emitState(send),
		)
		if err != nil {
			return result, err
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
			finalText += text
			outcome, err := gate.evaluate(ctx, send)
			if err != nil {
				return result, err
			}
			result.Verification = outcome.receipt
			switch outcome.action {
			case verifyActionRepair:
				transaction = append(transaction, verifyFeedback(outcome.receipt, e.turn))
				continue
			case verifyActionFailed:
				return result, protocol.NewProblem(
					protocol.CodeConflict, outcome.receipt.problemMessage(), false, nil,
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
			if err := send(Completed, Event{
				Text: finalText, Usage: &result.Usage, CostUSD: cost,
				CostKnown:            costKnown,
				EstimatedInputTokens: result.EstimatedInputTokens,
				InputTokenDelta:      result.InputTokenDelta,
				Verification:         outcome.receipt,
			}); err != nil {
				return result, err
			}
			e.history = cloneMessages(transaction)
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
		results, err := e.runTools(ctx, turnID, calls, executed, send)

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
