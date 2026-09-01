package agentcontext

import "github.com/fwtllh-png/CodeHelper/internal/adapter/provider"

// DefaultRecentTailTurns is the public working-set bound for raw history.
// Older turns stay in the durable journal; the model sees only this tail.
const DefaultRecentTailTurns = 2

// RecentToolResultStart is the earliest message that still holds one of the
// keep most recent tool results. keep<=0 means no extra keep beyond the tail.
func RecentToolResultStart(history []provider.Message, keep int) int {
	if keep <= 0 {
		return len(history)
	}
	seen := 0
	for index := len(history) - 1; index >= 0; index-- {
		if !messageHasToolResult(history[index]) {
			continue
		}
		seen++
		if seen >= keep {
			return index
		}
	}
	return 0
}

func messageHasToolResult(message provider.Message) bool {
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolResult && block.ToolResult != nil {
			return true
		}
	}
	return false
}

func ResolveRecentTailTurns(turns int) int {
	if turns <= 0 {
		return DefaultRecentTailTurns
	}
	return turns
}

func SafeTailStart(history []provider.Message, turns int) int {
	start := recentTurnStart(history, ResolveRecentTailTurns(turns))
	if start <= 0 {
		return 0
	}
	for start > 0 && !safeToolBoundary(history, start) {
		start--
	}
	return start
}

// VisibleTailStart is the projector start after applying one persisted fold.
func VisibleTailStart(history []provider.Message, turns, foldStart int) int {
	start := SafeTailStart(history, turns)
	if foldStart <= start || foldStart > len(history) {
		return start
	}
	if foldStart < len(history) && !safeToolBoundary(history, foldStart) {
		return start
	}
	return foldStart
}

// OldestVisibleTailFold returns the next safe start after dropping the oldest
// closed turn in the current visible tail. Intra-turn message cuts are ignored
// so a fold cannot hide the current user request. ok is false when the visible
// tail is already a single turn (or empty).
func OldestVisibleTailFold(
	history []provider.Message,
	turns, foldStart int,
	allowCurrentTurn bool,
) (int, bool) {
	_ = allowCurrentTurn
	base := VisibleTailStart(history, turns, foldStart)
	for _, cut := range compactionCuts(history, false) {
		if cut <= base {
			continue
		}
		if foldHidesCurrentUser(history, cut) {
			continue
		}
		return cut, true
	}
	return base, false
}

func foldHidesCurrentUser(history []provider.Message, cut int) bool {
	current := lastNonWorldTurn(history)
	if current == 0 {
		return false
	}
	for _, message := range history[cut:] {
		if IsWorldStateMessage(message) {
			continue
		}
		if message.Turn == current && message.Role == provider.RoleUser {
			return false
		}
	}
	return true
}

// RawTailMessages is the model-visible raw history after start, excluding
// world partitions that stay mandatory regardless of the tail bound.
func RawTailMessages(history []provider.Message, start int) []provider.Message {
	if start < 0 {
		start = 0
	}
	var tail []provider.Message
	for index, message := range history {
		if index >= start && !IsWorldStateMessage(message) {
			tail = append(tail, message)
		}
	}
	return tail
}

// FillVisibleTailStart walks newest closed groups already inside the turn
// bound and drops the oldest ones until estimate(raw tail) fits maxTokens.
// The current user request is never hidden. limited=false leaves start
// unchanged so a missing capacity or operator ceiling is not invented.
func FillVisibleTailStart(
	history []provider.Message,
	turns, start int,
	maxTokens uint64,
	limited bool,
	estimate func([]provider.Message) uint64,
) int {
	if !limited || estimate == nil {
		return start
	}
	for {
		if estimate(RawTailMessages(history, start)) <= maxTokens {
			return start
		}
		next, ok := OldestVisibleTailFold(history, turns, start, false)
		if !ok || next <= start {
			return start
		}
		start = next
	}
}

func lastNonWorldTurn(history []provider.Message) uint64 {
	for index := len(history) - 1; index >= 0; index-- {
		if IsWorldStateMessage(history[index]) {
			continue
		}
		if history[index].Turn != 0 {
			return history[index].Turn
		}
	}
	return 0
}

// ProjectContextView returns the model-visible raw tail. Durable history is
// not modified.
func ProjectContextView(
	history []provider.Message,
	turns int,
) []provider.Message {
	return ProjectContextViewFrom(history, SafeTailStart(history, turns))
}

func ProjectContextViewFrom(
	history []provider.Message,
	start int,
) []provider.Message {
	if start <= 0 {
		return CloneMessages(history)
	}
	if start > len(history) {
		return nil
	}
	kept := make([]provider.Message, 0, len(history)-start+4)
	for index, message := range history {
		if index >= start || IsWorldStateMessage(message) {
			kept = append(kept, CloneMessage(message))
		}
	}
	return kept
}

func IsWorldStateMessage(message provider.Message) bool {
	_, _, ok := InspectWorldMessage(message)
	return ok
}
