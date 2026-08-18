package engine

import (
	"maps"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// recoveryBaseHistory removes the exact source Turn group represented by the
// new recovery request. The recovery prompt already carries its canonical
// source request, unfinished output, and evidence capsule; retaining the group
// as well would inject that same authority twice and grow every Continue.
func (e *Engine) recoveryBaseHistory(
	recovery *protocol.TurnRecoveryContext,
) []provider.Message {
	history := cloneMessages(e.history)
	if recovery == nil || e.historyTurns == nil {
		return history
	}
	sourceTurn, ok := e.historyTurns[string(recovery.SourceTurnID)]
	if !ok || sourceTurn == 0 {
		return history
	}
	filtered := history[:0]
	for _, message := range history {
		if message.Turn == sourceTurn {
			continue
		}
		filtered = append(filtered, message)
	}
	return filtered
}

func (e *Engine) reconcileHistoryTurns(
	history []provider.Message,
	turnID string,
	turn uint64,
) {
	if e.historyTurns == nil {
		e.historyTurns = make(map[string]uint64)
	}
	visible := make(map[uint64]struct{}, len(history))
	for _, message := range history {
		if message.Turn != 0 {
			visible[message.Turn] = struct{}{}
		}
	}
	reconcileHistoryTurnBindingsFromSet(e.historyTurns, visible)
	if turnID == "" || turn == 0 {
		return
	}
	if _, ok := visible[turn]; ok {
		e.historyTurns[turnID] = turn
	}
}

func reconcileHistoryTurnBindings(
	bindings map[string]uint64,
	messageTurns []uint64,
) {
	visible := make(map[uint64]struct{}, len(messageTurns))
	for _, turn := range messageTurns {
		if turn != 0 {
			visible[turn] = struct{}{}
		}
	}
	reconcileHistoryTurnBindingsFromSet(bindings, visible)
}

func reconcileHistoryTurnBindingsFromSet(
	bindings map[string]uint64,
	visible map[uint64]struct{},
) {
	for turnID, turn := range bindings {
		if _, ok := visible[turn]; !ok {
			delete(bindings, turnID)
		}
	}
}

func historyContainsTurn(history []provider.Message, turn uint64) bool {
	for _, message := range history {
		if message.Turn == turn {
			return true
		}
	}
	return false
}

func cloneHistoryTurns(source map[string]uint64) map[string]uint64 {
	return maps.Clone(source)
}
