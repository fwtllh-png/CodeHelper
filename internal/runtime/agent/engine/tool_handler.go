package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

func (e *Engine) runTools(
	ctx context.Context,
	turnID string,
	calls []provider.ToolCall,
	executed map[string]tool.Result,
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
		if previous, exists := executed[call.ID]; exists {
			results[index] = previous
			continue
		}
		group.Add(1)
		go func(index int, call provider.ToolCall) {
			defer group.Done()
			binding := tool.CatalogBinding{
				CatalogID: call.CatalogID, Generation: call.CatalogGeneration,
				Revision: call.CatalogRevision, Authority: call.CatalogAuthority,
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
			result, err := e.guard.ExecuteBound(
				callCtx, call.ID, call.Name, json.RawMessage(call.Arguments), binding,
			)
			e.endToolSpan(call, span, result, err)
			if err != nil {
				if content, recoverable := recoverableToolFailure(err); recoverable {
					results[index] = tool.Result{Content: content, IsError: true}
					if category := toolFailureCategory(err); category != "" {
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
	for index, err := range errorsByIndex {
		if err != nil {
			return nil, fmt.Errorf("tool %s: %w", calls[index].Name, err)
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
		if copy.Metadata != nil {
			diagnosticReceipts, _ = copy.Metadata["diagnostics"].([]diagnostics.Receipt)
		}
		e.recordTurnDiagnostics(diagnosticReceipts)
		e.observeDiagnosticsEvidence(diagnosticReceipts)
		if err := send(RunningTools, Event{
			ToolCall: &call, Result: &copy, Diagnostics: diagnosticReceipts,
		}); err != nil {
			return nil, err
		}
	}
	return results, nil
}
