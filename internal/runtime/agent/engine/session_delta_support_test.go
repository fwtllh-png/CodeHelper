package engine

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
)

func prepareSessionDeltaForTest(
	turnID string,
	baseRevision uint64,
	history []provider.Message,
	usage provider.Usage,
	cost float64,
	states ...SessionStateDelta,
) (agentcontext.SessionDelta, error) {
	var state SessionStateDelta
	if len(states) != 0 {
		state = states[0]
	}
	turn := state.Turn
	for _, message := range history {
		turn = max(turn, message.Turn)
	}
	historyTurns := agentcontext.CloneHistoryTurns(state.HistoryTurns)
	agentcontext.ReconcileHistoryTurns(&historyTurns, history, turnID, turn)
	window := agentcontext.CloneWindowLedger(state.Window)
	if !window.Valid() {
		var err error
		window, err = agentcontext.CreateWindowLedger(1)
		if err != nil {
			return agentcontext.SessionDelta{}, err
		}
	}
	workspace := state.Workspace
	if workspace.WorkspaceIdentity == "" {
		workspace.WorkspaceIdentity = "workspace:test"
	}
	snapshot := agentcontext.ContextSnapshot{
		Epoch: max(uint64(1), state.Epoch), Revision: baseRevision + 1,
		Turn: turn, History: history, HistoryTurns: historyTurns,
		WorkingSet: state.WorkingSet, Evidence: state.Evidence,
		Failures: state.Failures, Compaction: state.Compaction,
		Plan: state.Plan, World: state.World, Workspace: workspace,
		Window: window,
	}
	if err := snapshot.Seal(); err != nil {
		return agentcontext.SessionDelta{}, err
	}
	accounting, err := agentcontext.PrepareAccountingDelta(turnID, usage, cost)
	if err != nil {
		return agentcontext.SessionDelta{}, err
	}
	return agentcontext.NewSessionDelta(snapshot, accounting, state.Manifest)
}
