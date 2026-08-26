package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const compactScopeBodyAfterPrefix = "body_after_prefix"

func (e *Engine) compact() *CompactionReceipt {
	receipt := e.compactHistory(&e.history, false)
	e.reconcileWorldBaseline(e.history)
	return receipt
}

func (e *Engine) runCompactGate(
	ctx context.Context,
	history *[]provider.Message,
	input agentcontext.MessageSnapshot,
	outputReserve uint64,
	phase string,
	allowCurrentTurn bool,
	send func(State, Event) error,
) (tokenWindow, error) {
	input = input.WithHistory(*history)
	window, err := e.measureTokenWindow(input, outputReserve)
	if err != nil {
		return tokenWindow{}, err
	}
	recentTailMaxTokens := e.recentTailMaxTokens()
	forceTailBudget := phase == CompactionPhasePostTurn &&
		allowCurrentTurn &&
		agentcontext.EstimateMessageTokens(*history) > recentTailMaxTokens
	receipt := e.compactHistoryWithPolicy(
		history, forceTailBudget, allowCurrentTurn, input, outputReserve,
	)
	if receipt != nil {
		receipt.Phase = phase
		if err := send(Compacting, Event{Compaction: receipt}); err != nil {
			return tokenWindow{}, err
		}
	}
	var inlineReceipt *CompactionReceipt
	if e.options.Context.SemanticNarrative != "off" || phase == CompactionPhasePostTurn {
		inlineReceipt, err = e.completeInlineNarrative(ctx, history)
		if err != nil {
			return tokenWindow{}, err
		}
		if inlineReceipt != nil {
			inlineReceipt.Phase = phase
			if err := send(Compacting, Event{Compaction: inlineReceipt}); err != nil {
				return tokenWindow{}, err
			}
		}
	}
	if receipt != nil || inlineReceipt != nil {
		input = input.WithHistory(*history)
		window, err = e.measureTokenWindow(input, outputReserve)
	}
	return window, err
}

func (e *Engine) runTerminalCompactGate(
	history *[]provider.Message,
	allowCurrentTurn bool,
	send func(State, Event) error,
) error {
	input := agentcontext.NewMessageLedger(agentcontext.LedgerInput{Stable: e.promptMessages()}).Snapshot()
	window, err := e.runCompactGate(
		context.Background(),
		history, input, 0,
		CompactionPhasePostTurn, allowCurrentTurn, send,
	)
	if err == nil && window.total > window.hardLimit {
		err = compactionBudgetError(window)
	}
	return err
}

type tokenWindow struct {
	estimated, total, active, hardLimit, compactLimit uint64
	accounting                                        agentcontext.WindowProjection
}

func (e *Engine) measureTokenWindow(
	input agentcontext.MessageSnapshot,
	outputReserve uint64,
) (tokenWindow, error) {
	measured, err := input.Measure("", "", e.options.TokenEstimator)
	if err != nil {
		return tokenWindow{}, err
	}
	projected := e.projectTokenWindow(&measured, outputReserve)
	active := projected.FullActiveTokens
	if e.options.Context.Window.Scope == compactScopeBodyAfterPrefix {
		active = projected.BodyTokens
		if !projected.Observed {
			active = projected.PendingTokens
		}
	}
	return tokenWindow{
		estimated: measured.EstimatedTokens,
		total:     projected.FullActiveTokens + outputReserve,
		active:    active, hardLimit: projected.HardLimit,
		compactLimit: projected.AutoCompactLimit,
		accounting:   projected,
	}, nil
}

func (e *Engine) contextBudgetSnapshot(history []provider.Message) ContextBudgetSnapshot {
	value := agentcontext.LedgerInput{
		Stable: e.promptMessages(), History: history,
	}
	if scope := e.runningScope(); scope != nil {
		scope.mu.Lock()
		if scope.state.contextLedger != nil {
			snapshot := scope.state.contextLedger.Snapshot()
			value.Stable = snapshot.Partition(agentcontext.KindStable)
			value.Definitions = snapshot.Definitions()
		}
		scope.mu.Unlock()
	}
	input := agentcontext.NewMessageLedger(value).Snapshot()
	window, _ := e.measureTokenWindow(input, e.maxOutputFor(e.activeRoute()))
	capacity := e.contextCapacity()
	return ContextBudgetSnapshot{
		ActiveTokens: window.active, AutoCompactTokens: window.compactLimit,
		PrepareTokens:   e.prepareCompactLimit(),
		EmergencyTokens: e.emergencyCompactLimit(),
		EstimatedTokens: window.estimated, MaxContextTokens: window.hardLimit,
		HardInputTokens: capacity.HardInputTokens,
		LimitSource:     string(capacity.LimitSource),
		OutputSource:    capacity.OutputSource,
		WindowID:        window.accounting.ID, WindowNumber: window.accounting.Number,
		Observed:             window.accounting.Observed,
		FullActiveTokens:     window.accounting.FullActiveTokens,
		PrefillTokens:        window.accounting.PrefillTokens,
		BodyTokens:           window.accounting.BodyTokens,
		ToolDefinitionTokens: window.accounting.ToolDefinitionTokens,
		PendingTokens:        window.accounting.PendingTokens,
		OutputReserve:        window.accounting.OutputReserve,
		Compactions:          e.compactionTotal(),
	}
}

// Compact applies the auto budget policy under the engine lock.
func (e *Engine) Compact() *CompactionReceipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compact()
}

// CompactForced summarizes older turns even below the automatic token limit.
// Used by explicit thread.compact operations.
func (e *Engine) CompactForced() *CompactionReceipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	receipt := e.compactHistory(&e.history, true)
	e.reconcileWorldBaseline(e.history)
	return receipt
}

func (e *Engine) reconcileWorldBaseline(history []provider.Message) {
	e.context.ReconcileWorld(history)
}

func (e *Engine) compactHistory(history *[]provider.Message, force bool) *CompactionReceipt {
	input := agentcontext.NewMessageLedger(agentcontext.LedgerInput{Stable: e.promptMessages()}).Snapshot()
	return e.compactHistoryWithPolicy(
		history, force, false, input, 0,
	)
}

func (e *Engine) compactHistoryWithPolicy(
	history *[]provider.Message,
	force bool,
	allowCurrentTurn bool,
	input agentcontext.MessageSnapshot,
	outputReserve uint64,
) *CompactionReceipt {
	if e.options.Hooks != nil {
		if err := e.options.Hooks.PreCompact(context.Background(), hooks.CompactInput{
			SessionID: e.options.SessionID, Forced: force, Messages: len(*history),
		}); err != nil {
			return nil
		}
	}
	if len(*history) <= 1 {
		return nil
	}
	finish := func(receipt *CompactionReceipt) *CompactionReceipt {
		e.noteCompaction()
		e.advanceTokenWindow()
		e.options.Metrics.Compaction(
			receipt.OriginalBytes - receipt.RetainedBytes,
		)
		if e.options.Hooks != nil {
			e.options.Hooks.PostCompact(context.Background(), hooks.CompactInput{
				SessionID: e.options.SessionID,
				Forced:    force,
				Messages:  len(*history),
			})
		}
		return receipt
	}
	authority := e.buildTruthCapsule(e.buildCompactSummary(nil))
	authorityDigest, err := authority.AuthorityDigest()
	if err != nil {
		return nil
	}
	selection, err := agentcontext.SelectCompaction(
		agentcontext.CompactionSelectionRequest{
			History: *history, Force: force,
			AllowCurrentTurn: allowCurrentTurn,
			Input:            input, OutputReserve: outputReserve,
			RecentTailTurns:     e.options.Context.RecentTailTurns,
			RecentTailMaxTokens: e.recentTailMaxTokens(),
			WindowScope:         e.options.Context.Window.Scope,
			EmergencyLimit:      e.emergencyCompactLimit(),
			AuthorityDigest:     authorityDigest,
			EstimateMessages:    agentcontext.EstimateMessageTokens,
			Measure: func(
				snapshot agentcontext.MessageSnapshot,
				reserve uint64,
			) (agentcontext.WindowMeasurement, error) {
				window, err := e.measureTokenWindow(snapshot, reserve)
				return agentcontext.WindowMeasurement{
					Estimated: window.estimated, Total: window.total,
					Active: window.active, HardLimit: window.hardLimit,
					CompactLimit: window.compactLimit,
					Projection:   window.accounting,
				}, err
			},
			Prune: func(
				history *[]provider.Message,
				snapshot agentcontext.MessageSnapshot,
				reserve uint64,
				forced bool,
			) (
				agentcontext.SurfacePruning,
				agentcontext.WindowMeasurement,
				error,
			) {
				stats, window, err := e.pruneToolResultSurfaces(
					history,
					snapshot,
					reserve,
					forced,
				)
				return agentcontext.SurfacePruning{
						Results: stats.results,
						Bytes:   stats.bytes,
					}, agentcontext.WindowMeasurement{
						Estimated: window.estimated, Total: window.total,
						Active: window.active, HardLimit: window.hardLimit,
						CompactLimit: window.compactLimit,
						Projection:   window.accounting,
					}, err
			},
			Build: e.buildCompactionCandidate,
		},
	)
	if err != nil {
		return nil
	}
	if selection.Candidate == nil {
		if selection.Pruning.Results == 0 {
			return nil
		}
		*history = selection.History
		return finish(promptcontext.NewPruningReceipt(
			selection,
			authorityDigest,
			e.contextReceipts(),
		))
	}
	selected := selection.Candidate
	*history = selected.History
	workingSet, criticalPaths := e.compactionPaths()
	receipt := promptcontext.NewCompactionReceipt(
		selection,
		summaryLineBytes,
		e.contextReceipts(),
		workingSet,
		criticalPaths,
	)
	finished := finish(receipt)
	durableRebase := e.options.Context.CommitRebase != nil || e.options.Context.CommitRebaseWithFacts != nil
	if (e.options.Context.SemanticNarrative == "off" && durableRebase) ||
		(e.options.Context.SemanticNarrative != "off" && selection.OriginalWindow.Active < e.emergencyCompactLimit()) {
		state := e.stageNarrativeCandidate(*selected)
		receipt.CompactionID, receipt.Status = state.ID, state.Phase
		receipt.Mode = e.options.Context.SemanticNarrative
		receipt.SourceWindowID, receipt.TargetWindowID = state.SourceWindowID, state.TargetWindowID
		receipt.FallbackReason = state.FallbackReason
	}
	return finished
}

type compactionCandidate = agentcontext.CompactionCandidate

func (e *Engine) buildCompactionCandidate(
	history []provider.Message,
	cut int,
	includeNarrative bool,
) (compactionCandidate, error) {
	removed := cloneMessages(history[:cut])
	toSummarize := agentcontext.StripWorldState(
		promptcontext.StripContextualFragments(cloneMessages(removed)),
	)
	summary := e.buildCompactSummary(toSummarize)
	if summary.Goal == "" {
		goal := agentcontext.ActiveTurnGoal(history)
		summary.Digest = agentcontext.RemoveGoalDigest(summary.Digest, goal)
		goal = strings.Join(strings.Fields(goal), " ")
		goalLimit := max(32, min(summaryLineBytes, e.summaryBudget()/2))
		if len(goal) > goalLimit {
			goal = agentcontext.TruncateUTF8(goal, goalLimit) + "..."
		}
		summary.Goal = goal
	}
	current := e.buildTruthCapsule(summary)
	tail := agentcontext.StripWorldState(
		promptcontext.StripContextualFragments(cloneMessages(history[cut:])),
	)
	return agentcontext.BuildCompactionCandidate(
		agentcontext.CompactionCandidateInput{
			Cut: cut, Removed: removed, ToSummarize: toSummarize,
			Tail: tail, OriginalHistory: history,
			Summary: summary, CurrentTruth: current,
			RetentionPolicy:  e.options.Context.TruthRetention,
			Turn:             e.turn,
			SummaryMaxBytes:  e.summaryBudget(),
			IncludeNarrative: includeNarrative,
		},
	)
}

func compactionBudgetError(window tokenWindow) error {
	return protocol.NewProblem(
		protocol.CodeResourceExhausted,
		fmt.Sprintf(
			"context compaction could not reduce %d tokens below the %d-token hard limit",
			window.total, window.hardLimit,
		),
		false,
		nil,
	)
}

func (e *Engine) History() []provider.Message {
	e.mu.Lock()
	defer e.mu.Unlock()
	return cloneMessages(e.history)
}

// ReplaceHistory installs a compacted replacement window as the model-visible history.
func (e *Engine) ReplaceHistory(messages []provider.Message) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.history = cloneMessages(messages)
	agentcontext.ReconcileHistoryTurns(&e.historyTurns, e.history, "", 0)
	e.advanceTokenWindow()
	e.context.ReconcileWorld(e.history)
	var maxTurn uint64
	for _, message := range e.history {
		if message.Turn > maxTurn {
			maxTurn = message.Turn
		}
	}
	if maxTurn > e.turn {
		e.turn = maxTurn
	}
}

func (e *Engine) Fork() (*Engine, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	e.planMu.Lock()
	defer e.planMu.Unlock()
	options := e.options
	options.Guard = nil
	forked, err := New(options)
	if err != nil {
		return nil, fmt.Errorf("construct forked engine: %w", err)
	}
	forkWindow, err := agentcontext.CreateWindowLedger(1)
	if err != nil {
		forkWindow = agentcontext.FallbackWindowLedger(
			agentcontext.WindowLedger{},
			fmt.Sprintf("%s:fork:%d", e.options.SessionID, e.turn),
		)
	}
	forked.history = cloneMessages(e.history)
	forked.mailboxHold = append([]PendingInput(nil), e.mailboxHold...)
	forked.turn = e.turn
	forked.context = e.context.Clone()
	forked.context.SetWindow(forkWindow)
	forked.historyTurns = agentcontext.CloneHistoryTurns(e.historyTurns)
	forked.planText = e.planText
	forked.plan = e.plan.Clone()
	forked.lastScope = &Scope{
		engine: forked,
		state:  newScopeState(forked),
	}
	if e.planReceipt != nil {
		receipt := *e.planReceipt
		forked.planReceipt = &receipt
	}
	return forked, nil
}

// LastTurnID returns the workspace journal turn id with the highest turn number.
func (e *Engine) LastTurnID() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.turnIDs) == 0 {
		return "", errors.New("no turn to revert")
	}
	var bestID string
	var bestTurn uint64
	for id, turn := range e.turnIDs {
		if bestID == "" || turn >= bestTurn {
			bestID = id
			bestTurn = turn
		}
	}
	return bestID, nil
}

func (e *Engine) RevertWorkspace(
	ctx context.Context, targetTurnID string,
) (workspacejournal.Receipt, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.journal == nil {
		return workspacejournal.Receipt{}, errors.New("workspace journal is not configured")
	}
	turn, exists := e.turnIDs[targetTurnID]
	if !exists {
		return workspacejournal.Receipt{}, errors.New("target turn is not present in workspace history")
	}
	receipt, err := e.journal.Revert(ctx, targetTurnID)
	if err != nil {
		return receipt, err
	}
	history := e.history[:0]
	for _, message := range e.history {
		if message.Turn != turn {
			history = append(history, message)
		}
	}
	e.history = history
	e.advanceTokenWindow()
	e.reconcileWorldBaseline(e.history)
	delete(e.turnIDs, targetTurnID)
	delete(e.historyTurns, targetTurnID)
	return receipt, nil
}

func cloneMessages(messages []provider.Message) []provider.Message {
	return agentcontext.CloneMessages(messages)
}

func cloneBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	return agentcontext.CloneBlocks(blocks)
}
