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
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const compactScopeBodyAfterPrefix = "body_after_prefix"

func (e *Engine) compact() *CompactionReceipt {
	receipt := e.compactHistory(&e.history, false)
	e.reconcileWorldBaseline(e.history)
	return receipt
}

func (e *Engine) runCompactGate(
	history *[]provider.Message,
	input contextstore.Snapshot,
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
	receipt := e.compactHistoryWithPolicy(
		history, false, allowCurrentTurn, input, outputReserve,
	)
	if receipt != nil {
		receipt.Phase = phase
		if err := send(Compacting, Event{Compaction: receipt}); err != nil {
			return tokenWindow{}, err
		}
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
	input := contextstore.New(contextstore.Input{Stable: e.promptMessages()}).Snapshot()
	window, err := e.runCompactGate(
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
	accounting                                        contextstore.WindowProjection
}

func (e *Engine) measureTokenWindow(
	input contextstore.Snapshot,
	outputReserve uint64,
) (tokenWindow, error) {
	measured, err := input.Measure("", "", e.options.TokenEstimator)
	if err != nil {
		return tokenWindow{}, err
	}
	projected := e.projectTokenWindow(&measured, outputReserve)
	active := projected.FullActiveTokens
	if e.options.CompactWindow.Scope == compactScopeBodyAfterPrefix {
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
	value := contextstore.Input{
		Stable: e.promptMessages(), History: history,
	}
	if scope := e.runningScope(); scope != nil {
		scope.mu.Lock()
		if scope.state.contextLedger != nil {
			snapshot := scope.state.contextLedger.Snapshot()
			value.Stable = snapshot.Partition(contextstore.KindStable)
			value.Definitions = snapshot.Definitions()
		}
		scope.mu.Unlock()
	}
	input := contextstore.New(value).Snapshot()
	window, _ := e.measureTokenWindow(input, e.maxOutputFor(e.activeRoute()))
	return ContextBudgetSnapshot{
		ActiveTokens: window.active, AutoCompactTokens: window.compactLimit,
		EstimatedTokens: window.estimated, MaxContextTokens: window.hardLimit,
		WindowID: window.accounting.ID, WindowNumber: window.accounting.Number,
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
	if !contextstore.WorldBaselineValid(history, e.world) {
		e.world = contextstore.WorldBaseline{}
	}
}

func (e *Engine) compactHistory(history *[]provider.Message, force bool) *CompactionReceipt {
	input := contextstore.New(contextstore.Input{Stable: e.promptMessages()}).Snapshot()
	return e.compactHistoryWithPolicy(
		history, force, false, input, 0,
	)
}

func (e *Engine) compactHistoryWithPolicy(
	history *[]provider.Message,
	force bool,
	allowCurrentTurn bool,
	input contextstore.Snapshot,
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
	input = input.WithHistory(*history)
	originalWindow, err := e.measureTokenWindow(input, outputReserve)
	if err != nil || !force && originalWindow.active < originalWindow.compactLimit &&
		originalWindow.total <= originalWindow.hardLimit {
		return nil
	}
	size := historyBytes(*history)
	originalMessages := len(*history)
	authority := e.buildTruthCapsule(e.buildCompactSummary(nil))
	authorityDigest, err := authority.AuthorityDigest()
	if err != nil {
		return nil
	}
	workingHistory := cloneMessages(*history)
	pruned, prunedWindow, err := e.pruneToolResultSurfaces(
		&workingHistory,
		input,
		outputReserve,
		force,
	)
	if err != nil {
		return nil
	}
	if pruned.results != 0 &&
		!toolPairIdentityEquivalent(*history, workingHistory) {
		return nil
	}
	pruningReceipt := func() *CompactionReceipt {
		return &CompactionReceipt{
			OriginalMessages:    originalMessages,
			OriginalBytes:       size,
			RetainedBytes:       historyBytes(workingHistory),
			OriginalTokens:      originalWindow.active,
			RetainedTokens:      prunedWindow.active,
			TruncationReason:    "tool_result_surface_pruning",
			PrunedToolResults:   pruned.results,
			PrunedBytes:         pruned.bytes,
			AuthorityDigest:     authorityDigest,
			AuthorityEquivalent: true,
			ContextReceipts:     e.contextReceipts(),
		}
	}
	pruningEnough := pruned.results != 0 &&
		(prunedWindow.active <= prunedWindow.compactLimit &&
			prunedWindow.total <= prunedWindow.hardLimit ||
			force && originalWindow.total <= originalWindow.hardLimit)
	if pruningEnough {
		*history = workingHistory
		return finish(pruningReceipt())
	}
	workingWindow := originalWindow
	if pruned.results != 0 {
		workingWindow = prunedWindow
	}
	target := originalWindow.compactLimit
	cuts := compactionCuts(workingHistory, allowCurrentTurn)
	if len(cuts) == 0 {
		if pruned.results != 0 {
			return finish(pruningReceipt())
		}
		return nil
	}
	var selected *compactionCandidate
	for _, cut := range cuts {
		candidate, buildErr := e.buildCompactionCandidate(
			workingHistory,
			cut,
			true,
		)
		if buildErr != nil {
			continue
		}
		input = input.WithHistory(candidate.history)
		window, estimateErr := e.measureTokenWindow(input, outputReserve)
		if estimateErr == nil && window.active > target {
			minimal, minimalErr := e.buildCompactionCandidate(
				workingHistory,
				cut,
				false,
			)
			if minimalErr == nil {
				minimalInput := input.WithHistory(minimal.history)
				minimalWindow, measureErr := e.measureTokenWindow(
					minimalInput,
					outputReserve,
				)
				if measureErr == nil && minimalWindow.active < window.active {
					candidate, window = minimal, minimalWindow
				}
			}
		}
		if estimateErr != nil ||
			window.active >= workingWindow.active &&
				window.total >= workingWindow.total {
			continue
		}
		if !toolPairsClosed(candidate.history) {
			continue
		}
		required := candidate.authority
		if err := candidate.capsule.ContainsAuthority(required); err != nil {
			continue
		}
		authorityDigest, err := required.AuthorityDigest()
		if err != nil {
			continue
		}
		candidate.authorityDigest = authorityDigest
		candidate.retainedTokens = window.active
		if force || window.active <= target ||
			e.options.CompactWindow.Scope == compactScopeBodyAfterPrefix &&
				originalWindow.total > originalWindow.hardLimit &&
				window.total <= window.hardLimit {
			selected = &candidate
			break
		}
	}
	if selected == nil {
		if pruned.results != 0 {
			*history = workingHistory
			return finish(pruningReceipt())
		}
		return nil
	}
	*history = selected.history
	workingSet, criticalPaths := e.compactionPaths()
	receipt := &CompactionReceipt{
		OriginalMessages: originalMessages, RemovedMessages: selected.cut,
		OriginalBytes: size, RetainedBytes: selected.retainedBytes,
		OriginalTokens: originalWindow.active, RetainedTokens: selected.retainedTokens,
		SummaryOriginalBytes: digestOriginalBytes(selected.toSummarize),
		SummaryRetainedBytes: len(selected.rendered),
		SummaryTruncated:     selected.summaryTruncated,
		Sections:             selected.sections,
		RemovedTurns:         uniqueMessageTurns(selected.removed),
		PrunedToolResults:    pruned.results,
		PrunedBytes:          pruned.bytes,
		TruthGeneration:      selected.truth.Generation,
		TruthEntities:        selected.truth.EntityCount,
		CriticalFacts:        selected.truth.CriticalEntityCount,
		CompatibilityHash:    selected.compatibilityHash,
		CompatibilityMatched: selected.truth.CompatibilityMatched,
		AuthorityDigest:      selected.authorityDigest,
		AuthorityEquivalent:  true,
		ModelDownshifted:     selected.truth.ModelDownshifted,
		DownshiftPolicy:      compact.DownshiftRuntimeTruthOnly,
		NarrativeIncluded:    selected.narrativeIncluded,
		CapsuleBytes:         selected.capsuleBytes,

		ContextReceipts: e.contextReceipts(),
		WorkingSet:      workingSet, CriticalPaths: criticalPaths,
	}
	if selected.summaryTruncated {
		receipt.TruncationReason = "summary_byte_budget"
	}
	return finish(receipt)
}

type compactionCandidate struct {
	cut               int
	history           []provider.Message
	removed           []provider.Message
	toSummarize       []provider.Message
	rendered          string
	retainedBytes     int
	retainedTokens    uint64
	summaryTruncated  bool
	sections          []string
	truth             compact.MergeReceipt
	capsule           compact.TruthCapsule
	authority         compact.TruthCapsule
	compatibilityHash string
	authorityDigest   string
	narrativeIncluded bool
	capsuleBytes      int
}

func (e *Engine) buildCompactionCandidate(
	history []provider.Message,
	cut int,
	includeNarrative bool,
) (compactionCandidate, error) {
	removed := cloneMessages(history[:cut])
	toSummarize := contextstore.StripWorldState(
		promptcontext.StripContextualFragments(cloneMessages(removed)),
	)
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
	previous, err := previousTruthCapsules(toSummarize)
	if err != nil {
		return compactionCandidate{}, err
	}
	current := e.buildTruthCapsule(summary)
	authority := current
	authority.Entities = append(
		[]compact.TruthEntity(nil),
		current.Entities...,
	)
	capsule, mergeReceipt, err := compact.MergeTruthCapsules(
		current,
		previous...,
	)
	if err != nil {
		return compactionCandidate{}, err
	}
	narrative := compact.Narrative{Lines: append([]string(nil), summary.Digest...)}
	if mergeReceipt.ModelDownshifted {
		narrative = compact.Narrative{}
	}
	renderSummary := summary
	if !includeNarrative {
		narrative = compact.Narrative{}
		renderSummary = compact.Summary{Window: summary.Window}
	}
	rendered, err := compact.RenderStructured(
		renderSummary,
		capsule,
		narrative,
		e.summaryBudget(),
	)
	if err != nil {
		return compactionCandidate{}, err
	}
	compacted := provider.TextMessage(provider.RoleSystem, rendered.Text)
	tail := contextstore.StripWorldState(
		promptcontext.StripContextualFragments(cloneMessages(history[cut:])),
	)
	candidate := append([]provider.Message{compacted}, tail...)
	if historyBytes(candidate) >= historyBytes(history) &&
		(rendered.NarrativeIncluded || len(rendered.Sections) > 1) {
		rendered, err = compact.RenderStructured(
			compact.Summary{Window: summary.Window},
			capsule,
			compact.Narrative{},
			e.summaryBudget(),
		)
		if err != nil {
			return compactionCandidate{}, err
		}
		compacted = provider.TextMessage(provider.RoleSystem, rendered.Text)
		candidate = append([]provider.Message{compacted}, tail...)
	}
	return compactionCandidate{
		cut: cut, history: candidate, removed: removed, toSummarize: toSummarize,
		rendered: rendered.Text, retainedBytes: historyBytes(candidate),
		summaryTruncated: rendered.Truncated, sections: rendered.Sections,
		truth: mergeReceipt, capsule: capsule, authority: authority,
		compatibilityHash: capsule.CompatibilityHash,
		narrativeIncluded: rendered.NarrativeIncluded,
		capsuleBytes:      rendered.CapsuleBytes,
	}, nil
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
	allowCurrentTurn bool,
) []int {
	var cuts []int
	currentTurn := history[len(history)-1].Turn
	for cut := 1; cut < len(history); cut++ {
		if !safeToolBoundary(history, cut) {
			continue
		}
		if !allowCurrentTurn && history[cut-1].Turn == currentTurn {
			continue
		}
		if history[cut-1].Turn != history[cut].Turn || allowCurrentTurn {
			cuts = append(cuts, cut)
		}
	}
	return cuts
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

type toolPairIdentity struct {
	name           string
	calls, results int
}

func toolPairIdentityEquivalent(
	before []provider.Message,
	after []provider.Message,
) bool {
	identities := func(messages []provider.Message) map[string]toolPairIdentity {
		result := make(map[string]toolPairIdentity)
		for _, message := range messages {
			for _, call := range messageToolCalls(message) {
				value := result[call.ID]
				value.name = call.Name
				value.calls++
				result[call.ID] = value
			}
			if id := messageToolResultID(message); id != "" {
				value := result[id]
				value.results++
				result[id] = value
			}
		}
		return result
	}
	left, right := identities(before), identities(after)
	if len(left) != len(right) {
		return false
	}
	for id, value := range left {
		if right[id] != value {
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
	e.reconcileHistoryTurns(e.history, "", 0)
	e.advanceTokenWindow()
	if !contextstore.WorldBaselineValid(e.history, e.world) {
		e.world = contextstore.WorldBaseline{}
	}
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
	forkWindow, err := createWindowLedger(1)
	if err != nil {
		forkWindow = fallbackWindowLedger(
			contextstore.WindowLedger{},
			fmt.Sprintf("%s:fork:%d", e.options.SessionID, e.turn),
		)
	}
	forked := &Engine{
		options: e.options, history: cloneMessages(e.history),
		mailboxHold:  append([]PendingInput(nil), e.mailboxHold...),
		turn:         e.turn,
		working:      e.working.Clone(),
		evidence:     e.evidence.Clone(),
		failures:     e.failures.Clone(),
		world:        contextstore.CloneWorldBaseline(e.world),
		window:       forkWindow,
		historyTurns: cloneHistoryTurns(e.historyTurns),

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
	e.advanceTokenWindow()
	e.reconcileWorldBaseline(e.history)
	delete(e.turnIDs, targetTurnID)
	delete(e.historyTurns, targetTurnID)
	return receipt, nil
}

func messageSize(message provider.Message) int {
	size := 0
	if message.Provenance != nil {
		size += len(message.Provenance.Adapter) +
			len(message.Provenance.Provider) +
			len(message.Provenance.Model)
		if message.Provenance.Replay != nil {
			size += len(message.Provenance.Replay.ContentDigest) +
				len(message.Provenance.Replay.Data)
		}
	}
	for _, block := range message.Blocks {
		size += len(block.Text)
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
	return contextstore.CloneMessages(messages)
}

func cloneBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	return contextstore.CloneBlocks(blocks)
}
