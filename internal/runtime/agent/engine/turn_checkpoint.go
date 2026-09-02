package engine

import (
	"strings"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool/interact"
	turnhistory "github.com/fwtllh-png/QCode/internal/adapter/tool/turnhistory"
	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
)

func (e *Engine) registerTurnHistoryTool() error {
	if e.options.Tools == nil {
		return nil
	}
	return turnhistory.Register(e.options.Tools, e.lookupTurnHistory)
}

func (e *Engine) lookupTurnHistory(turn uint64) ([]provider.Message, error) {
	return agentcontext.MessagesForTurn(e.cloneHistoryForLookup(), turn), nil
}

func (e *Engine) cloneHistoryForLookup() []provider.Message {
	if e.mu.TryLock() {
		defer e.mu.Unlock()
		return cloneMessages(e.history)
	}
	return cloneMessages(e.history)
}

func (e *Engine) currentTurn() uint64 {
	if e.mu.TryLock() {
		defer e.mu.Unlock()
		return e.turn
	}
	return e.turn
}

func (e *Engine) sessionStateHintMissing(history []provider.Message) bool {
	return e.omittedTurnHintMissing(history) || e.resumeHintMissing(history)
}

func (e *Engine) resumeHintMissing(history []provider.Message) bool {
	hint := e.resumeHintText()
	if hint == "" {
		return false
	}
	return !agentcontext.HistoryHasSessionStateHint(history, hint)
}

func (e *Engine) resumeHintText() string {
	return agentcontext.FormatResumeHint(
		e.currentPlan(),
		e.resumeReadPaths(),
		e.locatedSites(),
	)
}

func (e *Engine) currentPlan() agentcontext.Plan {
	e.planMu.Lock()
	defer e.planMu.Unlock()
	return e.plan.Clone()
}

func (e *Engine) resumeReadPaths() []string {
	return agentcontext.ReadPathsFromWorkingSet(
		e.workingLedger().Select(e.currentTurn(), e.options.WorkingSetLimit),
	)
}

func (e *Engine) resumeTruthEntities() []agentcontext.TruthEntity {
	entity, ok := agentcontext.ResumeRetrievalEntity(
		e.currentPlan(),
		e.resumeReadPaths(),
		e.locatedSites(),
	)
	if !ok {
		return nil
	}
	return []agentcontext.TruthEntity{entity}
}

func (e *Engine) omittedTurnHintMissing(history []provider.Message) bool {
	hint := agentcontext.FormatOmittedTurnHint(
		agentcontext.OmittedTurnIDs(history, e.recentTailTurns()),
	)
	if hint == "" {
		return false
	}
	return !agentcontext.HistoryHasSessionStateHint(history, hint)
}

func (e *Engine) omittedTurnTruthEntities(
	history []provider.Message,
) []agentcontext.TruthEntity {
	if len(history) == 0 {
		history = e.cloneHistoryForLookup()
	}
	turns := agentcontext.OmittedTurnIDs(history, e.recentTailTurns())
	entity, ok := agentcontext.OmittedTurnRetrievalEntity(turns)
	if !ok {
		return nil
	}
	return []agentcontext.TruthEntity{entity}
}

func (e *Engine) ensureClosedTurnCheckpoints() {
	history := e.cloneHistoryForLookup()
	current := e.currentTurn()
	e.checkpointMu.Lock()
	have := make(map[uint64]struct{}, len(e.turnCheckpoints))
	for _, checkpoint := range e.turnCheckpoints {
		have[checkpoint.Turn] = struct{}{}
	}
	e.checkpointMu.Unlock()
	budget := e.checkpointBudget()
	for _, turn := range agentcontext.UniqueMessageTurns(history) {
		if turn == 0 || turn >= current {
			continue
		}
		if _, exists := have[turn]; exists {
			continue
		}
		checkpoint, err := agentcontext.RenderTurnCheckpoint(
			agentcontext.CheckpointRenderInput{
				Turn:   turn,
				Status: agentcontext.CheckpointCompleted,
				Budget: budget,
			},
		)
		if err != nil {
			continue
		}
		e.checkpointMu.Lock()
		duplicate := false
		for _, existing := range e.turnCheckpoints {
			if existing.Turn == turn {
				duplicate = true
				break
			}
		}
		if !duplicate {
			e.turnCheckpoints = append(e.turnCheckpoints, checkpoint)
			have[turn] = struct{}{}
		}
		e.checkpointMu.Unlock()
	}
}

func (e *Engine) closedTurnSealStatus() (string, bool) {
	turn := e.currentTurn()
	if turn == 0 {
		return "", false
	}
	e.checkpointMu.Lock()
	defer e.checkpointMu.Unlock()
	for _, checkpoint := range e.turnCheckpoints {
		if checkpoint.Turn == turn {
			return checkpoint.Status, true
		}
	}
	return "", false
}

func (e *Engine) closedTurnCheckpointMessages() []provider.Message {
	e.checkpointMu.Lock()
	defer e.checkpointMu.Unlock()
	return agentcontext.CheckpointMessages(e.turnCheckpoints)
}

func (e *Engine) checkpointBudget() int {
	return agentcontext.ResolveCheckpointBudget(
		e.options.Context.CheckpointMaxBytes,
		e.options.SummaryMaxBytes,
		e.options.Context.NarrativeLimits.ItemMaxBytes,
	)
}

func (e *Engine) promoteOpenWork(artifact agentcontext.NarrativeArtifact) {
	e.planMu.Lock()
	current := e.plan.Clone()
	e.planMu.Unlock()
	promoted := agentcontext.PromoteNarrativeOpenWork(current, artifact)
	if planStepsEqual(current, promoted) {
		return
	}
	e.setPlan(promoted)
}

func planStepsEqual(left, right interact.Plan) bool {
	if len(left.Steps) != len(right.Steps) {
		return false
	}
	for index := range left.Steps {
		if left.Steps[index] != right.Steps[index] {
			return false
		}
	}
	return true
}

func (e *Engine) sealClosedTurnMemory(
	status string,
	artifact *agentcontext.NarrativeArtifact,
	failure string,
) {
	if e == nil {
		return
	}
	turn := e.currentTurn()
	if turn == 0 {
		return
	}
	if status == "" {
		status = agentcontext.CheckpointCompleted
	}
	if artifact != nil && status == agentcontext.CheckpointCompleted {
		e.promoteOpenWork(*artifact)
	}
	e.checkpointMu.Lock()
	for _, existing := range e.turnCheckpoints {
		if existing.Turn == turn {
			e.checkpointMu.Unlock()
			return
		}
	}
	e.checkpointMu.Unlock()
	e.planMu.Lock()
	plan := e.plan.Clone()
	e.planMu.Unlock()
	var items []agentcontext.NarrativeItem
	if artifact != nil {
		items = artifact.Body.Items
	}
	var readPaths []string
	if status == agentcontext.CheckpointCanceled {
		readPaths = agentcontext.ReadPathsFromWorkingSet(
			e.workingLedger().Select(turn, e.options.WorkingSetLimit),
		)
	}
	checkpoint, err := agentcontext.RenderTurnCheckpoint(
		agentcontext.CheckpointRenderInput{
			Turn:      turn,
			Status:    status,
			Plan:      plan,
			Items:     items,
			Failure:   strings.TrimSpace(failure),
			ReadPaths: readPaths,
			Budget:    e.checkpointBudget(),
		},
	)
	if err != nil {
		return
	}
	e.checkpointMu.Lock()
	defer e.checkpointMu.Unlock()
	for _, existing := range e.turnCheckpoints {
		if existing.Turn == turn {
			return
		}
	}
	e.turnCheckpoints = append(e.turnCheckpoints, checkpoint)
}
