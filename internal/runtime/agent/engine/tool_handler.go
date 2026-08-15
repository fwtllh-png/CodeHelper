package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type toolResultCacheEntry struct {
	callID string
	result tool.Result
}

type toolResultCache struct {
	revision uint64
	entries  map[string]toolResultCacheEntry
}

func (e *Engine) runToolsWithCache(
	ctx context.Context,
	turnID string,
	calls []provider.ToolCall,
	executed map[string]tool.Result,
	cache *toolResultCache,
	kernel *engineTurnKernel,
	send func(State, Event) error,
) ([]tool.Result, error) {
	if kernel == nil {
		return nil, protocol.NewProblem(
			protocol.CodeInternal,
			"turn kernel is required for tool execution",
			false,
			nil,
		)
	}
	if err := send(RunningTools, Event{}); err != nil {
		return nil, err
	}
	identity := tool.InvocationIdentityFrom(ctx)
	if identity.SessionID == "" {
		identity.SessionID = e.options.SessionID
	}
	if identity.ThreadID == "" {
		identity.ThreadID = e.options.SessionID
	}
	if identity.TurnID == "" {
		identity.TurnID = turnID
	}
	toolCtx, cancel := context.WithCancelCause(ctx)
	toolCtx = tool.WithInvocationIdentity(toolCtx, identity)
	toolCtx = tool.WithInvocationSource(toolCtx, tool.InvocationSourceModel)

	toolCtx = withToolAccount(toolCtx, &toolAccount{
		engine: e,
		emit:   func(event Event) error { return send(RunningTools, event) },
	})
	stream := newToolStream(e.options.MaxToolStreamBytes, send)
	defer stream.close()

	e.setActiveCancel(cancel)
	defer e.clearActiveCancel()
	defer cancel(nil)

	scope := e.executionScope()
	if scope == nil {
		return nil, errors.New("turn scope is not active")
	}
	sched := scope.state.scheduler
	results := make([]tool.Result, len(calls))
	errorsByIndex := make([]error, len(calls))
	skipExecution := make([]bool, len(calls))
	alreadyExecuted := make([]bool, len(calls))
	fingerprints := make([]string, len(calls))
	cacheSources := make([]string, len(calls))
	batchOwners := make(map[string]int)
	duplicateOwners := make(map[int]int)
	if cache.entries == nil {
		cache.entries = make(map[string]toolResultCacheEntry)
	}
	for index, call := range calls {
		if previous, exists := executed[call.ID]; exists {
			results[index] = previous
			skipExecution[index] = true
			alreadyExecuted[index] = true
			continue
		}
		binding := bindingForCall(call)
		_, descriptor, _, err := e.options.Tools.ResolveBound(call.Name, binding)
		if err != nil || descriptor.RepeatPolicy != tool.RepeatReplaySameTurn {
			continue
		}
		fingerprint, err := resultCacheFingerprint(call, binding, cache.revision)
		if err != nil {
			continue
		}
		fingerprints[index] = fingerprint
		if cached, exists := cache.entries[fingerprint]; exists {
			results[index] = cachedToolResult(cached.result, cached.callID)
			cacheSources[index] = cached.callID
			skipExecution[index] = true
			continue
		}
		if owner, exists := batchOwners[fingerprint]; exists {
			duplicateOwners[index] = owner
			cacheSources[index] = calls[owner].ID
			skipExecution[index] = true
			continue
		}
		batchOwners[fingerprint] = index
	}
	plannedCalls := make([]provider.ToolCall, 0, len(calls))
	for _, call := range calls {
		if _, exists := executed[call.ID]; !exists {
			plannedCalls = append(plannedCalls, call)
		}
	}
	if err := kernel.validateToolStarts(plannedCalls); err != nil {
		return nil, err
	}
	publishedCalls := make([]provider.ToolCall, 0, len(calls))
	for _, call := range calls {
		if _, exists := executed[call.ID]; exists {
			continue
		}
		e.noteToolCall(call)
		callCopy := call
		if err := kernel.startTools([]provider.ToolCall{call}); err != nil {
			closeErr := e.publishAbortedToolResults(
				publishedCalls,
				"tool batch aborted during lifecycle registration",
				send,
			)
			abortErr := kernel.abortTools(
				"tool lifecycle registration failed",
			)
			return nil, errors.Join(
				err,
				closeErr,
				abortErr,
			)
		}
		if err := kernel.startTool(call.ID); err != nil {
			closeErr := e.publishAbortedToolResults(
				publishedCalls,
				"tool batch aborted before execution",
				send,
			)
			abortErr := kernel.abortTools(
				"tool durable start failed",
			)
			return nil, errors.Join(err, closeErr, abortErr)
		}
		if err := send(RunningTools, Event{ToolCall: &callCopy}); err != nil {
			closeErr := e.publishAbortedToolResults(
				publishedCalls,
				"tool batch aborted before execution",
				send,
			)
			abortErr := kernel.abortTools("tool start publication failed")
			return nil, errors.Join(
				err,
				abortErr,
				closeErr,
			)
		}
		publishedCalls = append(publishedCalls, call)
	}
	var group sync.WaitGroup
	for index, call := range calls {
		if skipExecution[index] {
			continue
		}
		group.Add(1)
		go func(index int, call provider.ToolCall) {
			defer group.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					errorsByIndex[index] = protocol.NewProblem(
						protocol.CodeInternal,
						"tool execution panicked",
						false,
						fmt.Errorf("tool %s panic: %v", call.Name, recovered),
					)
				}
			}()
			binding := bindingForCall(call)
			if finishOnlyEnabled(toolCtx) {
				canonical, descriptor, _, resolveErr :=
					e.options.Tools.ResolveBound(call.Name, binding)
				if resolveErr == nil &&
					!finishOnlyToolAllowed(canonical, descriptor) {
					results[index] = tool.Result{
						Content: "read-only exploration is disabled after 32 " +
							"model steps without structured progress; apply a " +
							"workspace change, run a quality tool, update the " +
							"plan, or call turn_complete",
						IsError: true,
						Metadata: map[string]any{
							"error_category":  "no_progress_finish_only",
							"required_action": "finish_current_batch",
							"retry_original":  false,
						},
					}
					return
				}
			}
			if !e.toolCallEnabled(call.Name, binding) {
				results[index] = tool.Result{
					Content: "tool disabled by Session Profile: " + call.Name,
					IsError: true,
					Metadata: map[string]any{
						"error_category": "tool_disabled",
					},
				}
				return
			}
			span := e.beginToolSpan(call)
			e.options.Metrics.ToolExecution()

			callCtx := tool.WithOutputObserver(toolCtx, stream.observe(call))
			callCtx = tool.WithExecutionAdmission(callCtx, sched.Admit)
			result, err := e.executeToolBound(
				callCtx, call.ID, call.Name, json.RawMessage(call.Arguments), binding,
			)
			e.endToolSpan(call, span, result, err)
			if err != nil {
				if content, recoverable := recoverableToolFailure(err); recoverable {
					results[index] = tool.Result{Content: content, IsError: true}
					if metadata := toolFailureRecoveryMetadata(err); metadata != nil {
						results[index].Metadata = metadata
					} else if category := toolFailureCategory(err); category != "" {
						results[index].Metadata = map[string]any{"error_category": category}
					}
					return
				}
				if errors.Is(err, context.Canceled) || errors.Is(toolCtx.Err(), context.Canceled) {
					results[index] = tool.Result{
						Content: "tool aborted: context canceled", IsError: true,
					}
					return
				}
				results[index], errorsByIndex[index] = result, err
				return
			}
			results[index], errorsByIndex[index] = result, err
		}(index, call)
	}
	group.Wait()
	for index, owner := range duplicateOwners {
		results[index] = cachedToolResult(results[owner], calls[owner].ID)
	}
	var batchErr error
	for index, err := range errorsByIndex {
		if err == nil {
			continue
		}
		if batchErr == nil {
			batchErr = fmt.Errorf("tool %s: %w", calls[index].Name, err)
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
		category := toolFailureCategory(err)
		if category == "" {
			category = "tool_execution_failed"
		}
		result.Metadata["error_category"] = category
		result.Metadata["fatal"] = true
		results[index] = result
	}
	for index := range results {
		results[index], _ = e.options.Tools.AdmitResult(
			calls[index].Name,
			results[index],
		)
	}
	batchMutated := false
	for _, result := range results {
		if len(observedFileChanges(result.Metadata)) != 0 {
			batchMutated = true
			break
		}
	}
	var resultPublishErr error
	for index := range calls {
		if alreadyExecuted[index] {
			continue
		}
		mutationRevision := kernel.mutationRevision()
		e.bindVerificationEvidence(
			calls[index],
			&results[index],
			batchMutated,
			mutationRevision,
		)
		copy := results[index]
		call := calls[index]
		if !copy.IsError {
			for _, change := range observedFileChanges(copy.Metadata) {
				scope.state.diff.Record(TurnDiffEntry{
					Path: change.Path, Tool: call.Name, Kind: change.Kind,
					Added: change.Added, Removed: change.Removed,
				})
				e.observePath(workingset.SourceEdited, change.Path)
				e.observeChangeEvidence(change)
			}
			e.observePath(workingset.SourceRead, observedFileRead(copy.Metadata))
			e.observeEvidence(call, copy)
		} else {
			e.observeToolFailure(call, copy)
		}
		var diagnosticReceipts []diagnostics.Receipt
		var fileChanges []toolguard.FileChange
		if copy.Metadata != nil {
			diagnosticReceipts, _ = copy.Metadata["diagnostics"].([]diagnostics.Receipt)
			fileChanges = observedFileChanges(copy.Metadata)
		}
		e.recordTurnDiagnostics(diagnosticReceipts)
		e.observeDiagnosticsEvidence(diagnosticReceipts)
		if err := kernel.closeTool(call, copy, fileChanges); err != nil {
			resultPublishErr = errors.Join(resultPublishErr, err)
			continue
		}
		if call.Name == toolsearch.ToolName && !copy.IsError {
			resultPublishErr = errors.Join(
				resultPublishErr,
				e.refreshScopeCatalog(),
			)
		}
		if call.Name == "turn_complete" {
			decision, err := kernel.evaluateCompletion(
				e.completionCandidate(
					call,
					results[index],
					batchMutated,
					len(calls),
					mutationRevision,
				),
			)
			if err != nil {
				resultPublishErr = errors.Join(resultPublishErr, err)
				continue
			}
			bindCompletionDecision(&results[index], decision)
			copy = results[index]
		}
		executed[calls[index].ID] = results[index]
		if err := send(RunningTools, Event{
			ToolCall: &call, Result: &copy, Diagnostics: diagnosticReceipts,
			FileChanges: fileChanges,
		}); err != nil {
			resultPublishErr = errors.Join(resultPublishErr, err)
			continue
		}
	}
	if resultPublishErr != nil {
		return results, resultPublishErr
	}
	if batchMutated {
		cache.revision++
		clear(cache.entries)
	} else {
		for index, fingerprint := range fingerprints {
			if fingerprint == "" || cacheSources[index] != "" || results[index].IsError {
				continue
			}
			cache.entries[fingerprint] = toolResultCacheEntry{
				callID: calls[index].ID,
				result: results[index],
			}
		}
	}
	if batchErr != nil {
		return results, batchErr
	}
	return results, nil
}

func (e *Engine) publishAbortedToolResults(
	calls []provider.ToolCall,
	reason string,
	send func(State, Event) error,
) error {
	var publishErr error
	for index := range calls {
		call := calls[index]
		result := tool.Result{
			Content: reason,
			IsError: true,
			Metadata: map[string]any{
				"error_category": "tool_batch_aborted",
				"fatal":          true,
			},
		}
		result, _ = e.options.Tools.AdmitResult(call.Name, result)
		if err := send(RunningTools, Event{
			ToolCall: &call,
			Result:   &result,
		}); err != nil {
			publishErr = errors.Join(publishErr, err)
		}
	}
	return publishErr
}

func bindingForCall(call provider.ToolCall) tool.CatalogBinding {
	return tool.CatalogBinding{
		CatalogID: call.CatalogID, Generation: call.CatalogGeneration,
		Revision: call.CatalogRevision, Authority: call.CatalogAuthority,
	}
}

func resultCacheFingerprint(
	call provider.ToolCall,
	binding tool.CatalogBinding,
	revision uint64,
) (string, error) {
	decoder := json.NewDecoder(bytes.NewBufferString(call.Arguments))
	decoder.UseNumber()
	var arguments any
	if err := decoder.Decode(&arguments); err != nil {
		return "", err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(arguments)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%s",
		call.Name,
		binding.CatalogID,
		binding.Generation,
		binding.Revision,
		revision,
		binding.Authority,
		canonical,
	), nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("tool arguments contain multiple JSON values")
		}
		return err
	}
	return nil
}

func cachedToolResult(result tool.Result, sourceCallID string) tool.Result {
	copy := result
	copy.Metadata = maps.Clone(result.Metadata)
	copy.Outcome = tool.CloneOutcome(result.Outcome)
	copy.Execution = tool.CloneExecutionReceipt(result.Execution)
	if copy.Metadata == nil {
		copy.Metadata = make(map[string]any)
	}
	copy.Metadata["replayed_from_call_id"] = sourceCallID
	return copy
}

func (e *Engine) executeToolBound(
	ctx context.Context,
	callID, name string,
	arguments json.RawMessage,
	binding tool.CatalogBinding,
) (result tool.Result, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = protocol.NewProblem(
				protocol.CodeInternal,
				"tool execution panicked",
				false,
				fmt.Errorf("tool %s panic: %v", name, recovered),
			)
		}
	}()
	return e.guard.ExecuteBound(ctx, callID, name, arguments, binding)
}
