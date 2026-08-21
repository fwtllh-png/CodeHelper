package engine

import (
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
)

func (e *Engine) buildTruthCapsule(
	summary compact.Summary,
) compact.TruthCapsule {
	route := e.activeRoute()
	model := route.Model()
	maxDigest := e.options.MaxDigestEntries
	if maxDigest <= 0 {
		maxDigest = defaultMaxDigestEntries
	}
	compatibility := compact.Compatibility{
		SchemaVersion: compact.TruthSchemaVersion,
		Adapter:       string(route.Adapter()), Provider: route.ProviderID(),
		Model: model.ID, ContextTokens: model.Limits.ContextTokens,
		ToolCalls:       model.Capabilities.ToolCalls,
		Reasoning:       model.Capabilities.Reasoning,
		ImageInput:      model.Capabilities.ImageInput || model.Capabilities.Vision,
		SummaryMaxBytes: e.summaryBudget(), MaxDigestEntries: maxDigest,
		DownshiftPolicy: compact.DownshiftRuntimeTruthOnly,
	}
	capsule := compact.TruthCapsule{
		SchemaVersion: compact.TruthSchemaVersion, Generation: 1,
		CompatibilityHash: compatibility.Hash(),
		ModelID:           model.ID, ContextTokens: model.Limits.ContextTokens,
		DownshiftPolicy: compact.DownshiftRuntimeTruthOnly,
	}
	e.planMu.Lock()
	plan := e.plan.Clone()
	e.planMu.Unlock()
	capsule.Entities = append(capsule.Entities, e.planTruthEntities(plan)...)
	if len(capsule.Entities) == 0 && strings.TrimSpace(summary.Goal) != "" {
		capsule.Entities = append(capsule.Entities, compact.NewTruthEntity(
			compact.EntityGoal,
			"active",
			strings.TrimSpace(summary.Goal),
			"runtime.user_input",
		))
	}
	for _, failure := range summary.Failures {
		key := strings.Join(
			[]string{failure.Kind, failure.Name, failure.Reason},
			"\x00",
		)
		entity := compact.NewTruthEntity(
			compact.EntityFailure, key,
			failureLine(failure),
			"runtime.failure_ledger",
		)
		entity.Turn, entity.Count = failure.Turn, failure.Count
		capsule.Entities = append(capsule.Entities, entity)
	}
	evidenceDelta := e.evidenceSet().RetainedDelta(
		e.options.Context.TruthRetention.FactMaxEntities,
		e.options.Context.TruthRetention.VerifiedChangeRetentionTurns,
		e.options.Context.TruthRetention.HandleMaxEntities,
	)
	workspaceDigests := make(map[string]string)
	if binding, err := e.captureWorkspaceBindingFor(evidenceDelta); err == nil {
		for _, path := range binding.BoundPaths {
			workspaceDigests[path.Path] = path.ContentDigest
		}
	}
	capsule.Entities = append(
		capsule.Entities,
		evidenceTruthEntities(summary, evidenceDelta, workspaceDigests)...,
	)
	for _, path := range summary.CriticalPaths {
		capsule.Entities = append(capsule.Entities, compact.NewTruthEntity(
			compact.EntityCriticalPath, path, path, "runtime.working_set",
		))
	}
	capsule.Entities = append(
		capsule.Entities,
		e.pendingInputTruthEntities()...,
	)
	capsule.Seal()
	return capsule
}

func previousTruthCapsules(
	messages []provider.Message,
) ([]compact.TruthCapsule, error) {
	var result []compact.TruthCapsule
	for _, message := range messages {
		capsule, found, err := compact.ParseTruthCapsule(message.Text())
		if err != nil {
			return nil, err
		}
		if found {
			result = append(result, capsule)
		}
	}
	return result, nil
}
