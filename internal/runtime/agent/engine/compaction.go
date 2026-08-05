package engine

import (
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
)

// Compaction defaults. The history threshold lives in Options because a provider
// window is the thing it has to fit; these two are about the summary itself.
const (
	// defaultSummaryMaxBytes caps a rendered summary when nothing configures it.
	defaultSummaryMaxBytes = 8 << 10
	// defaultMaxDigestEntries bounds the per-message running record. Past a
	// hundred-odd lines the record stops being read and starts being scrolled.
	defaultMaxDigestEntries = 120
	// summaryLineBytes caps one flattened message.
	summaryLineBytes = 512
)

// buildCompactSummary assembles what a compaction keeps out of the ledgers the
// thread has been filling all along, plus a running record of the messages that
// are about to be dropped.
//
// Only the running record has to be carried across compactions. The other
// sections are read live from ledgers that already span the whole session, so
// regenerating them is not an approximation of the previous summary — it is the
// same answer, current as of now.
func (e *Engine) buildCompactSummary(removed []provider.Message) compact.Summary {
	summary := compact.Summary{Window: len(removed)}
	e.planMu.Lock()
	plan := e.plan
	e.planMu.Unlock()
	summary.Goal = plan.Objective
	if summary.Goal == "" {
		summary.Goal = plan.Title
	}
	open, done := plan.OutstandingSteps()
	summary.DoneTodos = done
	for _, step := range open {
		summary.Todos = append(summary.Todos, compact.Todo{Title: step.Title, Status: step.Status})
	}
	summary.Failures = e.failures.List()
	for _, change := range e.evidence.Changes() {
		summary.Changes = append(summary.Changes, compact.Change{
			Path: change.Path, Turn: change.Turn, Read: change.Read,
			Verified: change.Verified, Diagnostics: change.Diagnostics,
		})
	}
	compact.SortChanges(summary.Changes)
	_, summary.CriticalPaths = e.compactionPaths()
	snapshot := e.evidence.Snapshot(e.options.EvidenceLimit)
	for _, fact := range snapshot.Facts {
		summary.Facts = append(summary.Facts, compact.Fact{Line: fact.Describe()})
	}
	summary.OmittedFacts = snapshot.OmittedFacts
	summary.Digest, summary.Carried = e.digestRemoved(removed)
	return summary
}

// digestRemoved flattens the messages leaving the window, newest first, and picks
// out a previous summary to pass through whole.
//
// A previous summary flattened like an ordinary message loses everything the last
// compaction paid for: it is one long system message, so a per-message line
// budget cuts it to its first sentence. Recognising it is what makes a second
// compaction non-destructive.
func (e *Engine) digestRemoved(removed []provider.Message) ([]string, string) {
	limit := e.options.MaxDigestEntries
	if limit <= 0 {
		limit = defaultMaxDigestEntries
	}
	var digest []string
	var carried string
	for index := len(removed) - 1; index >= 0; index-- {
		message := removed[index]
		if body, ok := compact.Carry(message.Text()); ok {
			// Two carried summaries in one window can only happen if history was
			// spliced; the newest is the one that already absorbed the older.
			if carried == "" {
				carried = body
			}
			continue
		}
		if len(digest) == limit {
			continue
		}
		digest = append(digest, summaryLine(message))
	}
	return digest, carried
}

// summaryBudget is how many bytes a rendered summary may occupy.
//
// An explicit setting wins. Without one the budget follows the history threshold,
// so a small thread does not get a summary larger than the history it replaces.
func (e *Engine) summaryBudget() int {
	if e.options.SummaryMaxBytes > 0 {
		return e.options.SummaryMaxBytes
	}
	return max(64, min(defaultSummaryMaxBytes, e.options.MaxContextBytes/4))
}

// digestOriginalBytes is what the removed messages would have cost as flat lines.
// The receipt reports it against the retained size so a host can say how much a
// compaction actually saved.
func digestOriginalBytes(removed []provider.Message) int {
	total := 0
	for _, message := range removed {
		total += len(summaryLine(message)) + 1
	}
	return total
}

// summaryLine flattens one message to a single line. It is the last resort of a
// compaction: everything a section can state structurally is better stated there,
// because a line of transcript is only as useful as the reader's patience.
func summaryLine(message provider.Message) string {
	line := string(message.Role) + ": "
	switch {
	case message.Text() != "":
		line += strings.Join(strings.Fields(message.Text()), " ")
	case len(messageToolCalls(message)) != 0:
		calls := make([]string, 0, len(messageToolCalls(message)))
		for _, call := range messageToolCalls(message) {
			arguments := strings.Join(strings.Fields(call.Arguments), " ")
			if len(arguments) > 160 {
				arguments = truncateUTF8(arguments, 160) + "..."
			}
			calls = append(calls, call.ID+" "+call.Name+" "+arguments)
		}
		line += "tool calls " + strings.Join(calls, ", ")
	case messageToolResultID(message) != "":
		line += "tool result " + messageToolResultID(message)
	default:
		line += strings.Join(strings.Fields(blocksReasoning(message.Blocks)), " ")
	}
	if len(line) > summaryLineBytes {
		line = truncateUTF8(line, summaryLineBytes) + "..."
	}
	return line
}

// ContextBudget reports how many bytes of history the thread is carrying and the
// threshold that triggers the next compaction, so a host can show how close the
// thread is to losing the transcript the model is relying on.
//
// Like the other receipt accessors it runs unlocked, on the turn goroutine that
// already holds the engine: the receipt is assembled inside the turn, so taking
// the lock here would deadlock against it.
func (e *Engine) ContextBudget() (int, int) {
	if e == nil {
		return 0, 0
	}
	size := 0
	for _, message := range e.history {
		size += messageSize(message)
	}
	return size, e.options.MaxContextBytes
}

// Compactions is how many times this thread's history has been replaced by a
// summary. It shares ContextBudget's calling convention.
func (e *Engine) Compactions() int {
	if e == nil {
		return 0
	}
	return e.compactions
}
