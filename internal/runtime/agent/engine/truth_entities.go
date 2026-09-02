package engine

import (
	"fmt"
	"sort"

	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
)

func (e *Engine) pendingInputTruthEntities() []agentcontext.TruthEntity {
	scope := e.runningScope()
	if scope == nil {
		return nil
	}
	pending := scope.state.requests.Pending()
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]agentcontext.TruthEntity, 0, len(ids))
	for _, id := range ids {
		entity := agentcontext.NewTruthEntity(
			agentcontext.EntityPendingInput,
			id,
			fmt.Sprintf("pending %s request %s", pending[id], id),
			"runtime.input",
		)
		entity.Turn = e.turn
		result = append(result, entity)
	}
	return result
}
