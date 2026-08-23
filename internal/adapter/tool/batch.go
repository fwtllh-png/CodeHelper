package tool

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type BatchExecution struct {
	Context         context.Context
	Calls           []provider.ToolCall
	Plan            ReplayPlan
	Execute         func(context.Context, provider.ToolCall) (Result, error)
	Recover         func(provider.ToolCall, Result, error) (Result, bool)
	FailureCategory func(error) string
}

type BatchOutcome struct {
	Results []Result
	Error   error
}

// ExecuteBatch owns bounded parallel dispatch and normalizes execution errors
// into model-visible tool results. Lifecycle publication remains with the
// runtime because it must be committed through the turn kernel.
func ExecuteBatch(input BatchExecution) BatchOutcome {
	results := input.Plan.Results
	errorsByIndex := make([]error, len(input.Calls))
	execute := func(index int, call provider.ToolCall) {
		defer func() {
			if recovered := recover(); recovered != nil {
				err := protocol.NewFault(
					protocol.CodeConflict,
					"tool execution stopped unexpectedly",
					true,
					protocol.FaultMetadata{
						Origin:         protocol.FaultOriginTool,
						Disposition:    protocol.FaultResumeTurn,
						SideEffects:    protocol.SideEffectUnknown,
						RecoveryAction: "inspect side effects and continue the retained draft",
					},
					fmt.Errorf("tool %s panic: %v", call.Name, recovered),
				)
				if input.Recover != nil {
					if result, ok := input.Recover(
						call,
						Result{},
						err,
					); ok {
						results[index] = result
						return
					}
				}
				errorsByIndex[index] = err
			}
		}()
		result, err := input.Execute(input.Context, call)
		if err != nil && input.Recover != nil {
			if recovered, ok := input.Recover(call, result, err); ok {
				results[index] = recovered
				return
			}
		}
		if err != nil &&
			(errors.Is(err, context.Canceled) ||
				errors.Is(input.Context.Err(), context.Canceled)) {
			result.Content = "tool aborted: context canceled"
			result.IsError = true
			if result.Outcome == nil {
				result.Outcome = &Outcome{Status: OutcomeCanceled}
			}
			results[index] = result
			return
		}
		results[index], errorsByIndex[index] = result, err
	}
	var group sync.WaitGroup
	for index, call := range input.Calls {
		if input.Plan.SkipExecution[index] {
			continue
		}
		if input.Plan.ParallelPolicies[index] == ParallelSerial {
			group.Wait()
			execute(index, call)
			continue
		}
		group.Add(1)
		go func(index int, call provider.ToolCall) {
			defer group.Done()
			execute(index, call)
		}(index, call)
	}
	group.Wait()
	for index, owner := range input.Plan.DuplicateOwners {
		results[index] = CachedResult(results[owner], input.Calls[owner].ID)
	}
	var batchErr error
	for index, err := range errorsByIndex {
		if err == nil {
			continue
		}
		if batchErr == nil {
			batchErr = fmt.Errorf(
				"tool %s: %w",
				input.Calls[index].Name,
				err,
			)
		}
		result := results[index]
		result.IsError = true
		if result.Content == "" {
			result.Content = err.Error()
		}
		result.Metadata = maps.Clone(result.Metadata)
		if result.Metadata == nil {
			result.Metadata = make(map[string]any)
		}
		category := ""
		if input.FailureCategory != nil {
			category = input.FailureCategory(err)
		}
		if category == "" {
			category = "tool_execution_failed"
		}
		result.Metadata["error_category"] = category
		result.Metadata["fatal"] =
			protocol.DispositionOf(err) == protocol.FaultResumeTurn
		EnsureOutcomeFacts(&result).Failure =
			&FailureFact{Category: category}
		results[index] = result
	}
	return BatchOutcome{Results: results, Error: batchErr}
}
