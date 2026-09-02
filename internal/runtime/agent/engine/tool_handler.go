package engine

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	toolresult "github.com/fwtllh-png/QCode/internal/adapter/tool/result"
	"github.com/fwtllh-png/QCode/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/QCode/internal/observability/diagnostics"
	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

type toolResultCache = tool.ResultCache

func (e *Engine) runToolsWithCache(
	ctx context.Context,
	turnID string,
	calls []provider.ToolCall,
	executed map[string]tool.Result,
	cache *toolResultCache,
	kernel *turnkernel.RuntimeKernel,
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
	toolCtx, cancel := context.WithCancelCause(tool.WithInvocationIdentity(ctx, identity))
	toolCtx = tool.WithInvocationSource(toolCtx, tool.InvocationSourceModel)
	resultBudget := max(uint64(1), e.autoCompactLimit()/uint64(max(1, len(calls))))
	scope := e.executionScope()
	if scope == nil {
		cancel(nil)
		return nil, errors.New("turn scope is not active")
	}
	scope.mu.Lock()
	surfaceMaxBytes := scope.state.toolSurfaceMaxBytes
	surfaceItemBytes := scope.state.toolSurfaceItemBytes
	scope.mu.Unlock()
	if surfaceItemBytes > 0 && surfaceMaxBytes > 0 {
		batchItemBytes := surfaceMaxBytes / max(1, len(calls))
		surfaceBytes := min(surfaceItemBytes, max(1, batchItemBytes))
		surfaceTokens := uint64((surfaceBytes + 3) / 4)
		resultBudget = min(resultBudget, max(uint64(1), surfaceTokens))
	}
	toolCtx = tool.WithResultTokenBudget(toolCtx, min(e.options.Tools.ResultTokenCapacity(), resultBudget))

	toolCtx = withToolAccount(toolCtx, &toolAccount{
		engine: e,
		emit:   func(event Event) error { return send(RunningTools, event) },
	})
	stream := tool.NewOutputStream(
		e.options.MaxToolStreamBytes,
		func(output tool.OutputProjection) {
			_ = send(RunningTools, Event{ToolOutput: &ToolOutput{
				Tool: output.Tool, CallID: output.CallID,
				Stream: output.Stream, Chunk: output.Chunk,
				Cursor: output.Cursor, Truncated: output.Truncated,
			}})
		},
	)
	defer stream.Close()

	e.setActiveCancel(cancel)
	defer e.clearActiveCancel()
	defer cancel(nil)

	sched := scope.state.scheduler
	diagnosticReceipts := make(map[string][]diagnostics.Receipt, len(calls))
	return kernel.ExecuteToolEffect(turnkernel.ToolEffect{
		Context: toolCtx, Calls: calls, Executed: executed,
		Cache: cache, Registry: e.options.Tools,
		Admit:       e.admitToolBatch,
		BeforeStart: e.contextAuthority().NoteToolCall,
		PublishStart: func(call provider.ToolCall) error {
			copy := call
			return send(RunningTools, Event{ToolCall: &copy})
		},
		PublishAborted: func(
			call provider.ToolCall,
			result tool.Result,
		) error {
			return send(RunningTools, Event{
				ToolCall: &call,
				Result:   &result,
			})
		},
		Execute: func(callCtx context.Context, call provider.ToolCall) (tool.Result, error) {
			binding := tool.BindingForCall(call)
			finishOnly := tool.FinishOnlyEnabled(toolCtx)
			if finishOnly {
				canonical, descriptor, _, resolveErr :=
					e.options.Tools.ResolveBound(call.Name, binding)
				if resolveErr == nil &&
					!tool.FinishOnlyAllowed(canonical, descriptor) {
					return tool.Result{
						Content: "read-only exploration is disabled after repeated " +
							"model samples without structured progress; apply a " +
							"workspace change, run a quality tool, update the " +
							"plan, or call turn_complete",
						IsError: true,
						Metadata: map[string]any{
							"error_category":  "no_progress_finish_only",
							"required_action": "finish_current_batch",
							"retry_original":  false,
						},
					}, nil
				}
			}
			if blocked := e.observationGate(call, finishOnly); blocked != nil {
				return *blocked, nil
			}
			if !e.toolCallEnabled(call.Name, binding) {
				return tool.Result{
					Content: "tool disabled by Session Profile: " + call.Name,
					IsError: true,
					Metadata: map[string]any{
						"error_category": "tool_disabled",
					},
				}, nil
			}
			span := e.beginToolSpan(call)
			callCtx = tool.WithOutputObserver(callCtx, stream.Observe(call))
			callCtx = tool.WithExecutionAdmission(callCtx, sched.Admit)
			callCtx = e.tracer().Context(callCtx, span.ID())
			result, err := e.guard.ExecuteBound(
				callCtx,
				call.ID,
				call.Name,
				json.RawMessage(call.Arguments),
				binding,
			)
			e.endToolSpan(call, span, result, err)
			return result, err
		},
		Recover: func(
			call provider.ToolCall,
			result tool.Result,
			err error,
		) (tool.Result, bool) {
			return toolresult.RecoverResult(
				e.options.Tools,
				call,
				result,
				err,
			)
		},
		FailureCategory: toolFailureCategory,
		BeforeClose: func(
			call provider.ToolCall,
			result *tool.Result,
			batchMutated bool,
			mutationRevision uint64,
		) {
			e.bindVerificationEvidence(
				call,
				result,
				batchMutated,
				mutationRevision,
			)
			if !result.IsError {
				for _, change := range turnkernel.ObservedFileChanges(*result) {
					scope.state.diff.Record(turnkernel.TurnDiffEntry{
						Path: change.Path, Tool: call.Name, Kind: change.Kind,
						Added: change.Added, Removed: change.Removed,
					})
					e.contextAuthority().ObservePath(
						e.options.Workspace,
						agentcontext.SourceEdited,
						e.turn,
						change.Path,
					)
					e.contextAuthority().ObserveChange(
						e.options.Workspace,
						change,
						e.turn,
					)
				}
				e.contextAuthority().ObservePath(
					e.options.Workspace,
					agentcontext.SourceRead,
					e.turn,
					turnkernel.ObservedFileRead(*result),
				)
				e.contextAuthority().ObserveToolResult(
					e.options.Workspace,
					call,
					*result,
					e.turn,
				)
			} else {
				e.contextAuthority().ObserveToolFailure(
					call,
					*result,
					e.turn,
				)
			}
			if result.Outcome != nil && result.Outcome.Facts != nil {
				diagnosticReceipts[call.ID] = result.Outcome.Facts.Diagnostics
			}
			e.recordTurnDiagnostics(diagnosticReceipts[call.ID])
			e.contextAuthority().ObserveDiagnostics(
				e.options.Workspace,
				diagnosticReceipts[call.ID],
			)
		},
		CompletionCandidate: e.completionCandidate,
		AfterClose: func(call provider.ToolCall, result tool.Result) error {
			if call.Name == toolsearch.ToolName && !result.IsError {
				return e.refreshScopeCatalog()
			}
			return nil
		},
		PublishResult: func(
			call provider.ToolCall,
			result tool.Result,
		) error {
			return send(RunningTools, Event{
				ToolCall:    &call,
				Result:      &result,
				Diagnostics: diagnosticReceipts[call.ID],
				FileChanges: turnkernel.ObservedFileChanges(result),
			})
		},
	})
}
