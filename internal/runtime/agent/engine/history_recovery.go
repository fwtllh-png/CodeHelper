package engine

import (
	"context"
	"errors"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"unicode/utf8"
)

func (e *Engine) compact() *CompactionReceipt {
	return e.compactHistory(&e.history, false)
}

// runPreSamplingCompactGate compresses history before the first model sample
// when byte or context-token budgets are exceeded.
func (e *Engine) runPreSamplingCompactGate(
	history *[]provider.Message,
	send func(State, Event) error,
) error {
	receipt := e.compactHistory(history, false)
	if receipt == nil && e.contextTokenLimitReached(*history) {
		receipt = e.compactHistory(history, true)
	}
	if receipt == nil {
		return nil
	}
	receipt.Phase = CompactionPhasePreSampling
	return send(Compacting, Event{Compaction: receipt})
}

func (e *Engine) runMidTurnCompactGate(
	history *[]provider.Message,
	send func(State, Event) error,
) error {
	receipt := e.compactHistory(history, false)
	if receipt == nil {
		return nil
	}
	receipt.Phase = CompactionPhaseMidTurn
	return send(Compacting, Event{Compaction: receipt})
}

func (e *Engine) contextTokenLimitReached(history []provider.Message) bool {
	route := e.activeRoute()
	limit := route.Model().Limits.ContextTokens
	if limit == 0 {
		return false
	}
	messages := append(e.promptMessages(), cloneMessages(history)...)
	estimated, err := e.options.TokenEstimator.Estimate(messages)
	if err != nil {
		return false
	}
	return estimated+e.maxOutputFor(route) > limit
}

// Compact applies the auto budget policy under the engine lock.
func (e *Engine) Compact() *CompactionReceipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compact()
}

// CompactForced summarizes older turns even when history is under MaxContextBytes.
// Used by explicit thread.compact operations.
func (e *Engine) CompactForced() *CompactionReceipt {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.compactHistory(&e.history, true)
}

func (e *Engine) compactHistory(history *[]provider.Message, force bool) *CompactionReceipt {
	if e.options.Hooks != nil {
		if err := e.options.Hooks.PreCompact(context.Background(), hooks.CompactInput{
			SessionID: e.options.SessionID, Forced: force, Messages: len(*history),
		}); err != nil {
			return nil
		}
	}
	size := 0
	for _, message := range *history {
		size += messageSize(message)
	}
	if len(*history) <= 1 {
		return nil
	}
	if !force && size <= e.options.MaxContextBytes {
		return nil
	}
	originalMessages := len(*history)
	target := max(1, e.options.MaxContextBytes*3/4)
	if force && size <= e.options.MaxContextBytes {

		target = max(1, size/4)
	}
	tailSize := 0
	cut := len(*history)
	lastTurn := (*history)[len(*history)-1].Turn
	for cut > 0 {
		groupStart := cut - 1
		turn := (*history)[groupStart].Turn
		for groupStart > 0 && (*history)[groupStart-1].Turn == turn {
			groupStart--
		}
		groupSize := 0
		for _, message := range (*history)[groupStart:cut] {
			groupSize += messageSize(message)
		}
		if cut < len(*history) && tailSize+groupSize > target {
			break
		}
		tailSize += groupSize
		cut = groupStart
		if turn == lastTurn && cut == 0 {
			return nil
		}
	}
	if cut == 0 {
		return nil
	}
	toSummarize := promptcontext.StripContextualFragments(cloneMessages((*history)[:cut]))

	summary := e.buildCompactSummary(toSummarize)

	workingSet, criticalPaths := e.compactionPaths()
	rendered, summaryTruncated, sections := summary.Render(e.summaryBudget())
	removed := cloneMessages((*history)[:cut])
	compacted := provider.TextMessage(provider.RoleSystem, rendered)
	tail := promptcontext.StripContextualFragments(cloneMessages((*history)[cut:]))
	*history = append([]provider.Message{compacted}, tail...)
	retainedBytes := 0
	for _, message := range *history {
		retainedBytes += messageSize(message)
	}
	e.compactions++
	receipt := &CompactionReceipt{
		OriginalMessages: originalMessages, RemovedMessages: cut,
		OriginalBytes: size, RetainedBytes: retainedBytes,
		SummaryOriginalBytes: digestOriginalBytes(toSummarize),
		SummaryRetainedBytes: len(rendered),
		SummaryTruncated:     summaryTruncated,
		Sections:             sections,
		RemovedTurns:         uniqueMessageTurns(removed),

		PromptContextReceipts: e.contextReceipts(),
		WorkingSet:            workingSet, CriticalPaths: criticalPaths,
	}
	if summaryTruncated {
		receipt.TruncationReason = "summary_byte_budget"
	}
	e.options.Metrics.Compaction(max(0, size-retainedBytes))
	if e.options.Hooks != nil {
		e.options.Hooks.PostCompact(context.Background(), hooks.CompactInput{
			SessionID: e.options.SessionID, Forced: force, Messages: len(*history),
		})
	}
	e.resetBudgetReminder()
	return receipt
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
	e.resetBudgetReminder()
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

func (e *Engine) Fork() *Engine {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.steerMu.Lock()
	defer e.steerMu.Unlock()
	e.planMu.Lock()
	defer e.planMu.Unlock()
	forked := &Engine{
		options: e.options, history: cloneMessages(e.history),
		pending:     append([]PendingInput(nil), e.pending...),
		mailboxHold: append([]PendingInput(nil), e.mailboxHold...),
		turn:        e.turn,
		scheduler:   NewToolScheduler(e.options.MaxToolConcurrent),
		turnDiff:    NewTurnDiffTracker(),

		working:  e.working.Clone(),
		evidence: e.evidence.Clone(),
		failures: e.failures.Clone(),

		planText: e.planText,
		plan:     e.plan.Clone(),
	}
	if e.planReceipt != nil {
		receipt := *e.planReceipt
		forked.planReceipt = &receipt
	}
	return forked
}

func (e *Engine) Undo() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.history) == 0 {
		return false
	}
	lastTurn := e.history[len(e.history)-1].Turn
	if lastTurn != 0 {
		start := len(e.history) - 1
		for start > 0 && e.history[start-1].Turn == lastTurn {
			start--
		}
		e.history = e.history[:start]
		return true
	}
	start := -1
	for index := len(e.history) - 1; index >= 0; index-- {
		if e.history[index].Role == provider.RoleUser {
			start = index
			break
		}
	}
	if start < 0 {
		return false
	}
	e.history = e.history[:start]
	return true
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
	delete(e.turnIDs, targetTurnID)
	return receipt, nil
}

func messageSize(message provider.Message) int {
	size := 0
	for _, block := range message.Blocks {
		size += len(block.Text) + len(block.Signature) + len(block.ProviderData)
		if block.ToolCall != nil {
			size += len(block.ToolCall.ID) + len(block.ToolCall.Name) + len(block.ToolCall.Arguments)
		}
		if block.ToolResult != nil {
			size += len(block.ToolResult.CallID) + len(block.ToolResult.Content)
		}
	}
	return size
}

func uniqueMessageTurns(messages []provider.Message) []uint64 {
	seen := make(map[uint64]struct{})
	var turns []uint64
	for _, message := range messages {
		if message.Turn == 0 {
			continue
		}
		if _, exists := seen[message.Turn]; exists {
			continue
		}
		seen[message.Turn] = struct{}{}
		turns = append(turns, message.Turn)
	}
	return turns
}

func truncateUTF8(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func cloneMessages(messages []provider.Message) []provider.Message {
	cloned := make([]provider.Message, len(messages))
	for index, message := range messages {
		cloned[index] = message
		cloned[index].Blocks = cloneBlocks(message.Blocks)
	}
	return cloned
}

func cloneBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	cloned := make([]provider.ContentBlock, len(blocks))
	for index, block := range blocks {
		cloned[index] = block
		if block.ToolCall != nil {
			copy := *block.ToolCall
			cloned[index].ToolCall = &copy
		}
		if block.ToolResult != nil {
			copy := *block.ToolResult
			cloned[index].ToolResult = &copy
		}
		if block.Search != nil {
			copy := *block.Search
			copy.Sources = append([]provider.Source(nil), block.Search.Sources...)
			cloned[index].Search = &copy
		}
		if block.Citation != nil {
			copy := *block.Citation
			cloned[index].Citation = &copy
		}
		cloned[index].ProviderData = append([]byte(nil), block.ProviderData...)
	}
	return cloned
}
