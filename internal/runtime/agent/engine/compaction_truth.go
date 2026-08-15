package engine

import (
	"fmt"
	"strconv"
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
	goal := strings.TrimSpace(plan.Objective)
	goalSource := "runtime.plan"
	if goal == "" {
		goal = strings.TrimSpace(plan.Title)
	}
	if goal == "" {
		goal = strings.TrimSpace(summary.Goal)
		goalSource = "runtime.user_input"
	}
	if goal != "" {
		capsule.Entities = append(capsule.Entities, compact.NewTruthEntity(
			compact.EntityGoal, "active", goal, goalSource,
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
		capsule.Entities = append(capsule.Entities, entity)
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
	for _, change := range summary.Changes {
		entity := compact.NewTruthEntity(
			compact.EntityChange, change.Path, change.Path,
			"runtime.evidence",
		)
		entity.Turn, entity.Read = change.Turn, change.Read
		entity.Verified, entity.Diagnostics = change.Verified, change.Diagnostics
		if change.Verified {
			entity.VerificationSource = "runtime.evidence"
		}
		capsule.Entities = append(capsule.Entities, entity)
	}
	for _, path := range summary.CriticalPaths {
		capsule.Entities = append(capsule.Entities, compact.NewTruthEntity(
			compact.EntityCriticalPath, path, path, "runtime.working_set",
		))
	}
	evidenceDelta := e.evidenceSet().Delta()
	for _, fact := range evidenceDelta.Facts {
		value := fact.Describe()
		capsule.Entities = append(capsule.Entities, compact.NewTruthEntity(
			compact.EntityFact,
			fmt.Sprintf("%s\x00%s\x00%d", fact.Kind, fact.Path, fact.Line),
			value,
			"runtime.evidence",
		))
	}
	for _, handle := range evidenceDelta.Handles {
		entity := compact.NewTruthEntity(
			compact.EntityContentHandle,
			handle.ID,
			fmt.Sprintf("%s result handle %s", handle.Tool, handle.ID),
			"runtime.evidence",
		)
		entity.Turn, entity.Consumed = handle.Turn, handle.Consumed
		capsule.Entities = append(capsule.Entities, entity)
	}
	capsule.Seal()
	return capsule
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
