package engine

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
)

func (e *Engine) planTruthEntities(plan interact.Plan) []compact.TruthEntity {
	var entities []compact.TruthEntity
	goal := strings.TrimSpace(plan.Objective)
	if goal == "" {
		goal = strings.TrimSpace(plan.Title)
	}
	if goal != "" {
		entities = append(entities, compact.NewTruthEntity(
			compact.EntityGoal,
			"active",
			goal,
			"runtime.plan",
		))
	}
	for _, step := range plan.Steps {
		entity := compact.NewTruthEntity(
			compact.EntityTodo,
			strings.Join(strings.Fields(step.Title), " "),
			step.Title,
			"runtime.plan",
		)
		entity.Status = step.Status
		entity.Turn = e.turn
		entities = append(entities, entity)
	}
	return entities
}

func (e *Engine) pendingInputTruthEntities() []compact.TruthEntity {
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
	result := make([]compact.TruthEntity, 0, len(ids))
	for _, id := range ids {
		entity := compact.NewTruthEntity(
			compact.EntityPendingInput,
			id,
			fmt.Sprintf("pending %s request %s", pending[id], id),
			"runtime.input",
		)
		entity.Turn = e.turn
		result = append(result, entity)
	}
	return result
}

func evidenceTruthEntities(
	summary compact.Summary,
	delta evidence.Delta,
	workspaceDigests map[string]string,
) []compact.TruthEntity {
	result := make([]compact.TruthEntity, 0,
		len(summary.Changes)+len(delta.Facts)+len(delta.Handles))
	for _, change := range summary.Changes {
		entity := compact.NewTruthEntity(
			compact.EntityChange,
			change.Path,
			change.Path,
			"runtime.evidence",
		)
		entity.Turn, entity.Read = change.Turn, change.Read
		entity.Verified, entity.Diagnostics =
			change.Verified, change.Diagnostics
		if change.Verified {
			entity.VerificationSource = "runtime.evidence"
		}
		if digest := workspaceDigests[change.Path]; digest != "" {
			entity.WorkspacePath = change.Path
			entity.WorkspaceDigest = digest
			if change.Stale {
				entity.WorkspaceClaimStatus = compact.WorkspaceClaimStale
			} else {
				entity.WorkspaceClaimStatus = compact.WorkspaceClaimCurrent
			}
		} else if entity.Verified {
			entity.Verified = false
			entity.VerificationSource = ""
		}
		result = append(result, entity)
	}
	for _, fact := range delta.Facts {
		entity := compact.NewTruthEntity(
			compact.EntityFact,
			fmt.Sprintf("%s\x00%s\x00%d", fact.Kind, fact.Path, fact.Line),
			fact.Describe(),
			"runtime.evidence",
		)
		if digest := workspaceDigests[fact.Path]; digest != "" {
			entity.WorkspacePath = fact.Path
			entity.WorkspaceDigest = digest
			if fact.Stale {
				entity.WorkspaceClaimStatus = compact.WorkspaceClaimStale
			} else {
				entity.WorkspaceClaimStatus = compact.WorkspaceClaimCurrent
			}
		}
		result = append(result, entity)
	}
	for _, handle := range delta.Handles {
		entity := compact.NewTruthEntity(
			compact.EntityContentHandle,
			handle.ID,
			fmt.Sprintf("%s result handle %s", handle.Tool, handle.ID),
			"runtime.evidence",
		)
		entity.Turn, entity.Consumed = handle.Turn, handle.Consumed
		result = append(result, entity)
	}
	return result
}

func failureLine(value compact.Failure) string {
	line := value.Kind + " " + value.Name
	if value.Reason != "" {
		line += ": " + value.Reason
	}
	if value.Count > 1 {
		line += " (" + strconv.Itoa(value.Count) + " times)"
	}
	return line
}
