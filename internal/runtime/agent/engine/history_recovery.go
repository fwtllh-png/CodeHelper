package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
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
	needed := historyBytes(*history) > e.options.MaxContextBytes
	receipt := e.compactHistoryForGate(history, false, false)
	if receipt == nil && e.contextTokenLimitReached(*history) {
		needed = true
		receipt = e.compactHistoryForGate(history, true, false)
	}
	if receipt == nil {
		if needed {
			return compactionBudgetError(*history, e.options.MaxContextBytes)
		}
		return nil
	}
	if historyBytes(*history) > e.options.MaxContextBytes ||
		e.contextTokenLimitReached(*history) {
		return compactionBudgetError(*history, e.options.MaxContextBytes)
	}
	receipt.Phase = CompactionPhasePreSampling
	return send(Compacting, Event{Compaction: receipt})
}

func (e *Engine) runMidTurnCompactGate(
	history *[]provider.Message,
	send func(State, Event) error,
) error {
	needed := historyBytes(*history) > e.options.MaxContextBytes
	receipt := e.compactHistoryForGate(history, false, true)
	if receipt == nil && e.contextTokenLimitReached(*history) {
		needed = true
		receipt = e.compactHistoryForGate(history, true, true)
	}
	if receipt == nil {
		if needed {
			return compactionBudgetError(*history, e.options.MaxContextBytes)
		}
		return nil
	}
	if historyBytes(*history) > e.options.MaxContextBytes ||
		e.contextTokenLimitReached(*history) {
		return compactionBudgetError(*history, e.options.MaxContextBytes)
	}
	receipt.Phase = CompactionPhaseMidTurn
	return send(Compacting, Event{Compaction: receipt})
}

// runTerminalCompactGate enforces the same byte and token limits for every
// terminal path. Failed turns pass durable history while completed and canceled
// turns pass a tool-pair-safe transaction candidate.
func (e *Engine) runTerminalCompactGate(
	history *[]provider.Message,
	allowCurrentTurn bool,
	send func(State, Event) error,
) error {
	needed := historyBytes(*history) > e.options.MaxContextBytes
	receipt := e.compactHistoryForGate(history, false, allowCurrentTurn)
	if receipt == nil && e.contextTokenLimitReached(*history) {
		needed = true
		receipt = e.compactHistoryForGate(history, true, allowCurrentTurn)
	}
	if receipt == nil {
		if needed {
			return compactionBudgetError(*history, e.options.MaxContextBytes)
		}
		return nil
	}
	if historyBytes(*history) > e.options.MaxContextBytes ||
		e.contextTokenLimitReached(*history) {
		return compactionBudgetError(*history, e.options.MaxContextBytes)
	}
	receipt.Phase = CompactionPhasePostTurn
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

func (e *Engine) contextBudgetSnapshot(history []provider.Message) ContextBudgetSnapshot {
	snapshot := ContextBudgetSnapshot{
		HistoryBytes:     historyBytes(history),
		MaxHistoryBytes:  e.options.MaxContextBytes,
		Compactions:      e.compactionTotal(),
		MaxContextTokens: e.activeRoute().Model().Limits.ContextTokens,
	}
	messages := append(e.promptMessages(), cloneMessages(history)...)
	if estimated, err := e.options.TokenEstimator.Estimate(messages); err == nil {
		snapshot.EstimatedTokens = estimated
	}
	return snapshot
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
	return e.compactHistoryWithPolicy(history, force, false, false)
}

func (e *Engine) compactHistoryForGate(
	history *[]provider.Message,
	force bool,
	allowCurrentTurn bool,
) *CompactionReceipt {
	return e.compactHistoryWithPolicy(history, force, allowCurrentTurn, true)
}

func (e *Engine) compactHistoryWithPolicy(
	history *[]provider.Message,
	force bool,
	allowCurrentTurn bool,
	requireBudget bool,
) *CompactionReceipt {
	if e.options.Hooks != nil {
		if err := e.options.Hooks.PreCompact(context.Background(), hooks.CompactInput{
			SessionID: e.options.SessionID, Forced: force, Messages: len(*history),
		}); err != nil {
			return nil
		}
	}
	size := historyBytes(*history)
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
	cuts := compactionCuts(*history, target, allowCurrentTurn)
	if len(cuts) == 0 {
		return nil
	}
	var selected *compactionCandidate
	for _, cut := range cuts {
		candidate := e.buildCompactionCandidate(*history, cut)
		if candidate.retainedBytes >= size {
			continue
		}
		if requireBudget && size > e.options.MaxContextBytes &&
			candidate.retainedBytes > e.options.MaxContextBytes {
			continue
		}
		selected = &candidate
		if !requireBudget || candidate.retainedBytes <= target {
			break
		}
	}
	if selected == nil {
		return nil
	}
	*history = selected.history
	workingSet, criticalPaths := e.compactionPaths()
	e.noteCompaction()
	receipt := &CompactionReceipt{
		OriginalMessages: originalMessages, RemovedMessages: selected.cut,
		OriginalBytes: size, RetainedBytes: selected.retainedBytes,
		SummaryOriginalBytes: digestOriginalBytes(selected.toSummarize),
		SummaryRetainedBytes: len(selected.rendered),
		SummaryTruncated:     selected.summaryTruncated,
		Sections:             selected.sections,
		RemovedTurns:         uniqueMessageTurns(selected.removed),

		PromptContextReceipts: e.contextReceipts(),
		WorkingSet:            workingSet, CriticalPaths: criticalPaths,
	}
	if selected.summaryTruncated {
		receipt.TruncationReason = "summary_byte_budget"
	}
	e.options.Metrics.Compaction(size - selected.retainedBytes)
	if e.options.Hooks != nil {
		e.options.Hooks.PostCompact(context.Background(), hooks.CompactInput{
			SessionID: e.options.SessionID, Forced: force, Messages: len(*history),
		})
	}
	e.resetBudgetReminder()
	return receipt
}

type compactionCandidate struct {
	cut              int
	history          []provider.Message
	removed          []provider.Message
	toSummarize      []provider.Message
	rendered         string
	retainedBytes    int
	summaryTruncated bool
	sections         []string
}

func (e *Engine) buildCompactionCandidate(
	history []provider.Message,
	cut int,
) compactionCandidate {
	removed := cloneMessages(history[:cut])
	toSummarize := promptcontext.StripContextualFragments(cloneMessages(removed))
	summary := e.buildCompactSummary(toSummarize)
	if summary.Goal == "" {
		goal := activeTurnGoal(history)
		summary.Digest = removeGoalDigest(summary.Digest, goal)
		goal = strings.Join(strings.Fields(goal), " ")
		goalLimit := max(32, min(summaryLineBytes, e.summaryBudget()/2))
		if len(goal) > goalLimit {
			goal = truncateUTF8(goal, goalLimit) + "..."
		}
		summary.Goal = goal
	}
	rendered, truncated, sections := summary.Render(e.summaryBudget())
	compacted := provider.TextMessage(provider.RoleSystem, rendered)
	tail := promptcontext.StripContextualFragments(cloneMessages(history[cut:]))
	candidate := append([]provider.Message{compacted}, tail...)
	return compactionCandidate{
		cut: cut, history: candidate, removed: removed, toSummarize: toSummarize,
		rendered: rendered, retainedBytes: historyBytes(candidate),
		summaryTruncated: truncated, sections: sections,
	}
}

func activeTurnGoal(history []provider.Message) string {
	if len(history) == 0 {
		return ""
	}
	activeTurn := history[len(history)-1].Turn
	for _, message := range history {
		if message.Turn == activeTurn && message.Role == provider.RoleUser &&
			message.Text() != "" {
			return message.Text()
		}
	}
	return ""
}

func removeGoalDigest(digest []string, goal string) []string {
	goal = "user: " + strings.Join(strings.Fields(goal), " ")
	for index, line := range digest {
		if line == goal {
			return append(append([]string(nil), digest[:index]...), digest[index+1:]...)
		}
	}
	return digest
}

func compactionCuts(
	history []provider.Message,
	target int,
	allowCurrentTurn bool,
) []int {
	seen := make(map[int]struct{})
	var cuts []int
	if cut := turnGroupCut(history, target); cut > 0 {
		cuts = append(cuts, cut)
		seen[cut] = struct{}{}
	}
	if allowCurrentTurn {
		for cut := 1; cut < len(history); cut++ {
			if _, exists := seen[cut]; exists || !safeToolBoundary(history, cut) {
				continue
			}
			cuts = append(cuts, cut)
		}
	}
	return cuts
}

func turnGroupCut(history []provider.Message, target int) int {
	tailSize := 0
	cut := len(history)
	lastTurn := history[len(history)-1].Turn
	for cut > 0 {
		groupStart := cut - 1
		turn := history[groupStart].Turn
		for groupStart > 0 && history[groupStart-1].Turn == turn {
			groupStart--
		}
		groupSize := historyBytes(history[groupStart:cut])
		if cut < len(history) && tailSize+groupSize > target {
			break
		}
		tailSize += groupSize
		cut = groupStart
		if turn == lastTurn && cut == 0 {
			return 0
		}
	}
	return cut
}

func safeToolBoundary(history []provider.Message, cut int) bool {
	if cut <= 0 || cut >= len(history) {
		return false
	}
	return toolPairsClosed(history[:cut]) && toolPairsClosed(history[cut:])
}

func toolPairsClosed(messages []provider.Message) bool {
	calls := make(map[string]int)
	results := make(map[string]int)
	for _, message := range messages {
		for _, call := range messageToolCalls(message) {
			calls[call.ID]++
		}
		if id := messageToolResultID(message); id != "" {
			results[id]++
		}
	}
	if len(calls) != len(results) {
		return false
	}
	for id, count := range calls {
		if count != 1 || results[id] != 1 {
			return false
		}
	}
	return true
}

func historyBytes(messages []provider.Message) int {
	size := 0
	for _, message := range messages {
		size += messageSize(message)
	}
	return size
}

func compactionBudgetError(
	history []provider.Message,
	maxBytes int,
) error {
	return protocol.NewProblem(
		protocol.CodeResourceExhausted,
		fmt.Sprintf(
			"history compaction could not reduce %d bytes to the %d-byte budget",
			historyBytes(history),
			maxBytes,
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
	e.scopeMu.Lock()
	defer e.scopeMu.Unlock()
	e.planMu.Lock()
	defer e.planMu.Unlock()
	forked := &Engine{
		options: e.options, history: cloneMessages(e.history),
		mailboxHold: append([]PendingInput(nil), e.mailboxHold...),
		turn:        e.turn,
		working:     e.working.Clone(),
		evidence:    e.evidence.Clone(),
		failures:    e.failures.Clone(),

		planText: e.planText,
		plan:     e.plan.Clone(),
	}
	forked.lastScope = &Scope{
		engine: forked,
		state:  newScopeState(e),
	}
	if e.planReceipt != nil {
		receipt := *e.planReceipt
		forked.planReceipt = &receipt
	}
	return forked
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
