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
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type toolReplayEntry struct {
	callID string
	result tool.Result
}

type toolReplayCache struct {
	revision uint64
	entries  map[string]toolReplayEntry
}

func (e *Engine) runTools(
	ctx context.Context,
	turnID string,
	calls []provider.ToolCall,
	executed map[string]tool.Result,
	send func(State, Event) error,
) ([]tool.Result, error) {
	return e.runToolsWithReplay(
		ctx, turnID, calls, executed, &toolReplayCache{}, send,
	)
}

func (e *Engine) runToolsWithReplay(
	ctx context.Context,
	turnID string,
	calls []provider.ToolCall,
	executed map[string]tool.Result,
	replay *toolReplayCache,
	send func(State, Event) error,
) ([]tool.Result, error) {
	if err := send(RunningTools, Event{}); err != nil {
		return nil, err
	}
	identity := tool.InvocationIdentityFrom(ctx)
	if identity.ThreadID == "" {
		identity.ThreadID = e.options.SessionID
	}
	if identity.TurnID == "" {
		identity.TurnID = turnID
	}
	toolCtx, cancel := context.WithCancel(ctx)
	toolCtx = tool.WithInvocationIdentity(toolCtx, identity)

	toolCtx = withToolAccount(toolCtx, &toolAccount{
		engine: e,
		emit:   func(event Event) error { return send(RunningTools, event) },
	})
	stream := newToolStream(e.options.MaxToolStreamBytes, send)
	defer stream.close()

	e.setActiveCancel(cancel)
	defer e.clearActiveCancel()
	defer cancel()

	sched := e.scheduler
	if sched == nil {
		sched = NewToolScheduler(e.options.MaxToolConcurrent)
	}
	results := make([]tool.Result, len(calls))
	errorsByIndex := make([]error, len(calls))
	skipExecution := make([]bool, len(calls))
	fingerprints := make([]string, len(calls))
	replaySources := make([]string, len(calls))
	batchOwners := make(map[string]int)
	duplicateOwners := make(map[int]int)
	if replay.entries == nil {
		replay.entries = make(map[string]toolReplayEntry)
	}
	for index, call := range calls {
		if previous, exists := executed[call.ID]; exists {
			results[index] = previous
			skipExecution[index] = true
			continue
		}
		binding := bindingForCall(call)
		_, descriptor, _, err := e.options.Tools.ResolveBound(call.Name, binding)
		if err != nil || descriptor.RepeatPolicy != tool.RepeatReplaySameTurn {
			continue
		}
		fingerprint, err := replayFingerprint(call, binding, replay.revision)
		if err != nil {
			continue
		}
		fingerprints[index] = fingerprint
		if cached, exists := replay.entries[fingerprint]; exists {
			results[index] = replayedToolResult(cached.result, cached.callID)
			replaySources[index] = cached.callID
			skipExecution[index] = true
			continue
		}
		if owner, exists := batchOwners[fingerprint]; exists {
			duplicateOwners[index] = owner
			replaySources[index] = calls[owner].ID
			skipExecution[index] = true
			continue
		}
		batchOwners[fingerprint] = index
	}
	for _, call := range calls {
		if _, exists := executed[call.ID]; exists {
			continue
		}
		e.noteToolCall(call)
		callCopy := call
		if err := send(RunningTools, Event{ToolCall: &callCopy}); err != nil {
			return nil, err
		}
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
			policyKind := tool.ParallelSerial
			if _, desc, _, err := e.options.Tools.ResolveBound(call.Name, binding); err == nil {
				policyKind = desc.ParallelPolicy
			}
			release, err := sched.Admit(toolCtx, policyKind)
			if err != nil {
				results[index] = tool.Result{
					Content: "tool aborted: " + err.Error(), IsError: true,
				}
				return
			}
			defer release()

			span := e.beginToolSpan(call)
			e.options.Metrics.ToolExecution()

			callCtx := tool.WithOutputObserver(toolCtx, stream.observe(call))
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
		results[index] = replayedToolResult(results[owner], calls[owner].ID)
	}
	for index, err := range errorsByIndex {
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", calls[index].Name, err)
		}
	}
	batchMutated := false
	for _, result := range results {
		if len(observedFileChanges(result.Metadata)) != 0 {
			batchMutated = true
			break
		}
	}
	if batchMutated {
		replay.revision++
		clear(replay.entries)
	} else {
		for index, fingerprint := range fingerprints {
			if fingerprint == "" || replaySources[index] != "" || results[index].IsError {
				continue
			}
			replay.entries[fingerprint] = toolReplayEntry{
				callID: calls[index].ID,
				result: results[index],
			}
		}
	}
	for index := range calls {
		executed[calls[index].ID] = results[index]
		copy := results[index]
		call := calls[index]
		if !copy.IsError {
			for _, change := range observedFileChanges(copy.Metadata) {
				e.turnDiff.Record(TurnDiffEntry{
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
		if err := send(RunningTools, Event{
			ToolCall: &call, Result: &copy, Diagnostics: diagnosticReceipts,
			FileChanges: fileChanges,
		}); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func bindingForCall(call provider.ToolCall) tool.CatalogBinding {
	return tool.CatalogBinding{
		CatalogID: call.CatalogID, Generation: call.CatalogGeneration,
		Revision: call.CatalogRevision, Authority: call.CatalogAuthority,
	}
}

func replayFingerprint(
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

func replayedToolResult(result tool.Result, sourceCallID string) tool.Result {
	copy := result
	copy.Metadata = maps.Clone(result.Metadata)
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
