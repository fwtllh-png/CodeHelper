package engine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type engineSandboxBackend struct{ root string }

func (b engineSandboxBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "fixture",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (b engineSandboxBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	command.PreparedPolicyID = "engine-fixture"
	command.PreparedStrength = sandbox.StrengthStrong
	return command, nil
}

func TestEngineExecutesToolAndFeedsResultOnce(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "echo", Arguments: `{"text":"hello"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	executor := &echoTool{}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	var states []State
	result, err := engine.Run(t.Context(), "work", func(event Event) error {
		states = append(states, event.State)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "done" || result.State != Completed || executor.calls.Load() != 1 {
		t.Fatalf("result=%+v calls=%d", result, executor.calls.Load())
	}
	if len(runtime.requests) != 2 {
		t.Fatalf("requests = %+v", runtime.requests)
	}
	var toolMessage provider.Message
	for _, message := range runtime.requests[1].Messages {
		if message.Role == provider.RoleTool {
			toolMessage = message
			break
		}
	}
	if toolMessage.Role != provider.RoleTool || messageToolResultID(toolMessage) != "call_1" {
		t.Fatalf("tool message = %+v", toolMessage)
	}
	assertOneTerminal(t, states, Completed)
}

func TestEngineReplaysCanonicalDuplicateToolCallsWithinTurn(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "lookup",
				Arguments: `{"a":1,"b":2}`,
			}},
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 1, ID: "call_2", Name: "lookup",
				Arguments: `{ "b": 2, "a": 1 }`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		textStream("done"),
	}}
	executor := &countingCatalogExecutor{descriptor: tool.Descriptor{
		Name: "lookup", Description: "deterministic lookup",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		RepeatPolicy:       tool.RepeatReplaySameTurn,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"a": map[string]any{"type": "number"},
				"b": map[string]any{"type": "number"},
			},
			"required":             []string{"a", "b"},
			"additionalProperties": false,
		},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	var results []Event
	if _, err := engine.Run(t.Context(), "look up twice", func(event Event) error {
		if event.ToolCall != nil && event.Result != nil {
			results = append(results, event)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if executor.calls.Load() != 1 {
		t.Fatalf("lookup executions = %d, want 1", executor.calls.Load())
	}
	if len(results) != 2 ||
		results[1].Result.Metadata["replayed_from_call_id"] != "call_1" {
		t.Fatalf("tool results = %+v", results)
	}
}

func TestEngineFailsWhenCompletionRepairRemainsEmpty(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
	}}
	result, err := newEngine(t, runtime, nil).Run(t.Context(), "review", nil)
	if err == nil || protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("result=%+v err=%v, want explicit completion failure", result, err)
	}
	if result.State == Completed {
		t.Fatalf("incomplete result reported completed: %+v", result)
	}
}

func TestEngineRepairsEmptyFinalResponse(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
		textStream("无法继续执行，但已明确说明原因。"),
	}}
	result, err := newEngine(t, runtime, nil).Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "无法继续执行，但已明确说明原因。" ||
		len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	var foundFeedback bool
	for _, message := range runtime.requests[1].Messages {
		if message.Role == provider.RoleUser &&
			strings.Contains(message.Text(), "[completion_required]") {
			foundFeedback = true
			break
		}
	}
	if !foundFeedback {
		t.Fatalf("completion feedback missing from request: %+v", runtime.requests[1].Messages)
	}
}

func TestWorkspaceChangeIntentRejectsTextOnlyCompletion(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream("I will make the change next."),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	var states []State
	result, err := engine.RunForTurnWithIntentAndAttachments(
		t.Context(),
		"turn-1",
		"fix the bug",
		protocol.TurnIntentWorkspaceChange,
		nil,
		func(event Event) error {
			states = append(states, event.State)
			return nil
		},
	)
	if err == nil || protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("Run() error = %v, want conflict", err)
	}
	if result.State != Failed {
		t.Fatalf("result state = %q, want failed", result.State)
	}
	assertOneTerminal(t, states, Failed)
	if len(engine.History()) != 0 {
		t.Fatalf("failed workspace change committed history: %+v", engine.History())
	}
}

func TestCompletionRepairHasIndependentStepBudget(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "echo", Arguments: `{"text":"evidence"}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonEndTurn},
		}},
		textStream("最终结论：验证完成。"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry,
		MaxOutputTokens: 128, MaxSteps: 2,
		Authorize: func(provider.ToolCall) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "最终结论：验证完成。" || len(runtime.requests) != 3 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
}

func TestEngineContinuesIncompleteProviderStop(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "partial"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonMaxTokens},
		}},
		textStream(" answer"),
	}}
	result, err := newEngine(t, runtime, nil).Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "partial answer" || len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	var partialReplay, continuationFeedback bool
	for _, message := range runtime.requests[1].Messages {
		switch {
		case message.Role == provider.RoleAssistant && message.Text() == "partial":
			partialReplay = true
		case message.Role == provider.RoleUser &&
			strings.Contains(message.Text(), "[continue_after_incomplete"):
			continuationFeedback = true
		}
	}
	if !partialReplay || !continuationFeedback {
		t.Fatalf("continuation request = %+v", runtime.requests[1].Messages)
	}
}

func TestEngineUsesBoundedFinishRouteAfterReasoningOnlyMaxTokens(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventReasoningDelta, Text: "completed analysis"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonMaxTokens},
		}},
		textStream("final answer"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	engine.options.ReasoningEffort = "max"

	result, err := engine.Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "final answer" ||
		result.Reasoning != "completed analysis" ||
		len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	if len(runtime.requests[0].Tools) == 0 {
		t.Fatal("normal reasoning sample did not receive tools")
	}
	finish := runtime.requests[1]
	if finish.ReasoningEffort != "low" || len(finish.Tools) != 0 ||
		finish.MaxOutputTokens > 4096 {
		t.Fatalf("finish request = %+v", finish)
	}
	var foundFeedback bool
	for _, message := range finish.Messages {
		if message.Role == provider.RoleUser &&
			strings.Contains(message.Text(), "[finish_after_reasoning_limit]") {
			foundFeedback = true
		}
	}
	if !foundFeedback {
		t.Fatalf("finish feedback missing from %+v", finish.Messages)
	}
}

func TestEngineDoesNotUseFinishRouteForPartialToolCall(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "partial", Name: "echo", Arguments: `{"text":`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonMaxTokens},
		}},
		textStream("recovered without replaying the partial call"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	engine.options.ReasoningEffort = "max"

	result, err := engine.Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "recovered without replaying the partial call" ||
		len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	continuation := runtime.requests[1]
	if continuation.ReasoningEffort != "max" || len(continuation.Tools) == 0 {
		t.Fatalf("partial tool continuation entered finish route: %+v", continuation)
	}
}

func TestEngineFailsAfterBoundedIncompleteContinuations(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "one"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonIncomplete},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: " two"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonMaxTokens},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: " three"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonIncomplete},
		}},
	}}
	var states []State
	result, err := newEngine(t, runtime, nil).Run(t.Context(), "review", func(event Event) error {
		states = append(states, event.State)
		return nil
	})
	if err == nil || protocol.CodeOf(err) != protocol.CodeResourceExhausted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(runtime.requests) != maxOutputContinuations+1 {
		t.Fatalf("requests=%d", len(runtime.requests))
	}
	assertOneTerminal(t, states, Failed)
}

func TestEngineDoesNotContinueContentFilterStop(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "blocked"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonContentFilter},
		}},
	}}
	result, err := newEngine(t, runtime, nil).Run(t.Context(), "review", nil)
	if err == nil || protocol.CodeOf(err) != protocol.CodeInvalidArgument {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(runtime.requests) != 1 {
		t.Fatalf("requests=%d", len(runtime.requests))
	}
}

func TestEngineIncompleteContinuationCanResumeWithToolCall(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "checking"},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonMaxTokens},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "echo", Arguments: `{"text":"evidence"}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		textStream("done"),
	}}
	executor := &echoTool{}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}
	result, err := newEngine(t, runtime, registry).Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "done" || len(result.Tools) != 1 ||
		executor.calls.Load() != 1 || len(runtime.requests) != 3 {
		t.Fatalf(
			"result=%+v calls=%d requests=%d",
			result,
			executor.calls.Load(),
			len(runtime.requests),
		)
	}
}

func TestEngineRepairsInterruptedPostToolNarrationBeforeCompletion(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "echo", Arguments: `{"text":"evidence"}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "所有事实齐备，现在提交修改："},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonMaxTokens},
		}},
		textStream("继续提交 file_apply 事务："),
		textStream("最终结论：修改未执行，工作区保持不变。"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	var completedText string
	result, err := newEngine(t, runtime, registry).Run(t.Context(), "review", func(event Event) error {
		if event.State == Completed {
			completedText = event.Text
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "最终结论：修改未执行，工作区保持不变。" ||
		completedText != result.Text || len(runtime.requests) != 4 {
		t.Fatalf(
			"result=%+v completed=%q requests=%d",
			result,
			completedText,
			len(runtime.requests),
		)
	}
	var foundFeedback bool
	for _, message := range runtime.requests[3].Messages {
		if message.Role == provider.RoleUser &&
			strings.Contains(message.Text(), "[completion_required]") {
			foundFeedback = true
			break
		}
	}
	if !foundFeedback {
		t.Fatalf("completion feedback missing from request: %+v", runtime.requests[3].Messages)
	}
}

func TestEngineRepairsNarrationAfterStructuredToolFailure(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "result_error", Arguments: `{}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		textStream("小笔误，修正后重跑："),
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_2", Name: "echo", Arguments: `{"text":"fixed"}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		textStream("最终结论：修正后的检查已通过。"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(resultErrorTool{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	result, err := newEngine(t, runtime, registry).Run(t.Context(), "review", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "最终结论：修正后的检查已通过。" ||
		len(result.Tools) != 2 || len(runtime.requests) != 4 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	var foundFeedback bool
	for _, message := range runtime.requests[2].Messages {
		if message.Role == provider.RoleUser &&
			strings.Contains(message.Text(), "[tool_failure_resolution_required]") {
			foundFeedback = true
			break
		}
	}
	if !foundFeedback {
		t.Fatalf("tool failure feedback missing from request: %+v", runtime.requests[2].Messages)
	}
}

func TestEngineDoesNotClearToolFailureWithTextOnlyPromises(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "result_error", Arguments: `{}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		textStream("I will retry the failed edit next."),
		textStream("The remaining fix still needs to be applied."),
		textStream("Continuing with the repair."),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(resultErrorTool{}, nil); err != nil {
		t.Fatal(err)
	}

	result, err := newEngine(t, runtime, registry).RunForTurnWithIntentAndAttachments(
		t.Context(), "turn-failure", "fix it",
		protocol.TurnIntentOperation, nil, nil,
	)

	if err == nil || protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("result=%+v error=%v, want conflict", result, err)
	}
	if result.State == Completed {
		t.Fatalf("text-only promises cleared a structured tool failure: %+v", result)
	}
}

func TestEngineRetainsFailureUntilPostRecoveryCompletionCheck(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "result_error", Arguments: `{}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_2", Name: "echo", Arguments: `{"text":"recovered"}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		textStream("与预期有出入，直接精确核实："),
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_3", Name: "echo", Arguments: `{"text":"verified"}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
		textStream("最终结论：恢复和核实均已完成。"),
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(resultErrorTool{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}

	result, err := newEngine(t, runtime, registry).Run(t.Context(), "review", nil)

	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "最终结论：恢复和核实均已完成。" ||
		len(result.Tools) != 3 || len(runtime.requests) != 5 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	var foundFeedback bool
	for _, message := range runtime.requests[3].Messages {
		if message.Role == provider.RoleUser &&
			strings.Contains(message.Text(), "[tool_failure_resolution_required]") {
			foundFeedback = true
			break
		}
	}
	if !foundFeedback {
		t.Fatalf("tool failure feedback missing from request: %+v", runtime.requests[3].Messages)
	}
}

func TestRunToolsClosesEveryStartedCallBeforeFatalBatchFailure(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(failingTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, &scriptedProvider{}, registry)
	var emitted []tool.Result

	_, err := engine.runTools(
		t.Context(),
		"turn-test",
		[]provider.ToolCall{
			{ID: "call_ok", Name: "echo", Arguments: `{"text":"hello"}`},
			{ID: "call_fail", Name: "fail", Arguments: `{}`},
		},
		make(map[string]tool.Result),
		func(_ State, event Event) error {
			if event.Result != nil {
				emitted = append(emitted, *event.Result)
			}
			return nil
		},
	)

	if err == nil || !strings.Contains(err.Error(), "intentional failure") {
		t.Fatalf("runTools() error = %v", err)
	}
	if len(emitted) != 2 {
		t.Fatalf("runTools() emitted %d results, want 2", len(emitted))
	}
	if emitted[0].IsError || !emitted[1].IsError {
		t.Fatalf("emitted results = %+v", emitted)
	}
	if category, _ := emitted[1].Metadata["error_category"].(string); category != "tool_execution_failed" {
		t.Fatalf("fatal error_category = %q", category)
	}
}

func TestEngineContainsToolPanicAsFailedTurn(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_1", Name: "panic_tool", Arguments: `{}`,
			}},
			{Type: provider.EventMessageStop, StopReason: provider.StopReasonToolUse},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(panickingTool{}, nil); err != nil {
		t.Fatal(err)
	}
	var states []State
	result, err := newEngine(t, runtime, registry).Run(t.Context(), "run it", func(event Event) error {
		states = append(states, event.State)
		return nil
	})
	if err == nil || protocol.CodeOf(err) != protocol.CodeInternal {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assertOneTerminal(t, states, Failed)
}

func TestToolDefinitionsAreEmptyWhenToolsAreDisabled(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))

	if definitions := engine.toolDefinitions(); len(definitions) != 0 {
		t.Fatalf("toolDefinitions() = %+v, want empty tools-off surface", definitions)
	}
}

func TestEngineToolRoundTripAcrossProviderProtocols(t *testing.T) {
	tests := map[string]struct {
		protocol model.WireProtocol
		path     string
		first    string
		second   string
		wantBody []string
	}{
		"chat": {
			protocol: model.ProtocolOpenAIChat, path: "/chat/completions",
			first:    "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"function\":{\"name\":\"echo\",\"arguments\":\"{\\\"text\\\":\\\"hello\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n",
			second:   "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n",
			wantBody: []string{`"role":"tool"`, `"tool_call_id":"call_1"`},
		},
		"responses": {
			protocol: model.ProtocolOpenAIResponses, path: "/responses",
			first:    "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"echo\"}}\n\ndata: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"item_id\":\"call_1\",\"delta\":\"{\\\"text\\\":\\\"hello\\\"}\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n",
			second:   "data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\"}\n\ndata: {\"type\":\"response.completed\",\"response\":{}}\n\n",
			wantBody: []string{`"type":"function_call"`, `"type":"function_call_output"`, `"call_id":"call_1"`},
		},
		"anthropic": {
			protocol: model.ProtocolAnthropic, path: "/messages",
			first:    "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"call_1\",\"name\":\"echo\"}}\n\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"text\\\":\\\"hello\\\"}\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
			second:   "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\ndata: {\"type\":\"message_stop\"}\n\n",
			wantBody: []string{`"type":"tool_use"`, `"type":"tool_result"`, `"tool_use_id":"call_1"`},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			var requests atomic.Int32
			var secondBody string
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path != test.path {
					t.Errorf("path = %q", request.URL.Path)
				}
				data, _ := io.ReadAll(request.Body)
				attempt := requests.Add(1)
				writer.Header().Set("Content-Type", "text/event-stream")
				if attempt == 1 {
					_, _ = io.WriteString(writer, test.first)
					return
				}
				secondBody = string(data)
				_, _ = io.WriteString(writer, test.second)
			}))
			defer server.Close()

			executor := &echoTool{}
			registry := tool.NewRegistry(nil, nil)
			if err := registry.Register(executor, nil); err != nil {
				t.Fatal(err)
			}
			runtime, err := New(Options{
				Provider: httpclient.New(), Route: testRouteProtocol(t, server.URL, test.protocol),
				Tools: registry, MaxOutputTokens: 128,
				Authorize: func(provider.ToolCall) bool { return true },
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := runtime.Run(t.Context(), "work", nil)
			if err != nil {
				t.Fatal(err)
			}
			if result.Text != "done" || executor.calls.Load() != 1 || requests.Load() != 2 {
				t.Fatalf("result=%+v tool_calls=%d requests=%d", result, executor.calls.Load(), requests.Load())
			}
			for _, fragment := range test.wantBody {
				if !strings.Contains(secondBody, fragment) {
					t.Fatalf("second request missing %s: %s", fragment, secondBody)
				}
			}
		})
	}
}

func TestEngineUsesWebSearchCitationToCompleteTurn(t *testing.T) {
	searchServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"results":[{"title":"Fixture Source","url":"https://example.test/source","snippet":"verified answer"}]}`)
	}))
	defer searchServer.Close()
	t.Setenv("CODEHELPER_WEB_SEARCH_URL", searchServer.URL)

	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "web_1", Name: "web_search", Arguments: `{"query":"verified answer"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "completed with citation"},
			{Type: provider.EventMessageStop},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := webtool.Register(registry, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	result, err := newEngine(t, runtime, registry).Run(t.Context(), "research", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "completed with citation" || len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
	var toolResult provider.Message
	for _, message := range runtime.requests[1].Messages {
		if message.Role == provider.RoleTool {
			toolResult = message
			break
		}
	}
	if toolResult.Role != provider.RoleTool ||
		!strings.Contains(toolResult.Blocks[0].ToolResult.Content, `"url":"https://example.test/source"`) ||
		!strings.Contains(toolResult.Blocks[0].ToolResult.Content, `"citations"`) {
		t.Fatalf("tool result = %+v", toolResult)
	}
}

func TestEngineRetriesOnlyBeforeMeaningfulStreamData(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&errorStream{err: errors.New("temporary")},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "ok"},
			{Type: provider.EventMessageStop},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	engine := newEngine(t, runtime, registry)
	engine.options.MaxRetries = 1

	result, err := engine.Run(t.Context(), "retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "ok" || len(runtime.requests) != 2 {
		t.Fatalf("result=%+v requests=%d", result, len(runtime.requests))
	}
}

func TestSamplingSnapshotRejectsToolReplacedBeforeExecution(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	oldExecutor := &countingCatalogExecutor{descriptor: echoDescriptor()}
	oldExecutor.descriptor.Description = "catalog v1"
	if _, err := registry.Reconcile(
		"dynamic:sampling", 0, []tool.Registration{tool.NewRegistration(oldExecutor)},
	); err != nil {
		t.Fatal(err)
	}
	newExecutor := &countingCatalogExecutor{descriptor: echoDescriptor()}
	newExecutor.descriptor.Description = "catalog v2"
	runtime := &catalogMutationProvider{
		registry: registry,
		streams: []provider.Stream{
			&provider.SliceStream{Events: []provider.StreamEvent{
				{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
					Index: 0, ID: "call_stale", Name: "echo", Arguments: `{"text":"old"}`,
				}},
				{Type: provider.EventMessageStop},
			}},
			&provider.SliceStream{Events: []provider.StreamEvent{
				{Type: provider.EventTextDelta, Text: "recovered"},
				{Type: provider.EventMessageStop},
			}},
			textStream("recovered"),
		},
		mutate: func() error {
			_, err := registry.Replace(
				"dynamic:sampling", registry.Generation(), tool.NewRegistration(newExecutor),
			)
			return err
		},
	}
	result, err := newEngine(t, runtime, registry).Run(t.Context(), "replace race", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "recovered" || oldExecutor.calls.Load() != 0 || newExecutor.calls.Load() != 0 {
		t.Fatalf(
			"result=%+v old_calls=%d new_calls=%d",
			result, oldExecutor.calls.Load(), newExecutor.calls.Load(),
		)
	}
	if got := definitionDescription(runtime.requests[0], "echo"); got != "catalog v1" {
		t.Fatalf("first sample description = %q", got)
	}
	if got := definitionDescription(runtime.requests[1], "echo"); got != "catalog v2" {
		t.Fatalf("next sample description = %q", got)
	}
}

func TestProviderRetryReusesCatalogSnapshot(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	oldExecutor := &countingCatalogExecutor{descriptor: echoDescriptor()}
	oldExecutor.descriptor.Description = "retry v1"
	if _, err := registry.Reconcile(
		"dynamic:retry", 0, []tool.Registration{tool.NewRegistration(oldExecutor)},
	); err != nil {
		t.Fatal(err)
	}
	newExecutor := &countingCatalogExecutor{descriptor: echoDescriptor()}
	newExecutor.descriptor.Description = "retry v2"
	runtime := &catalogMutationProvider{
		registry: registry,
		streams: []provider.Stream{
			&errorStream{err: errors.New("temporary")},
			&provider.SliceStream{Events: []provider.StreamEvent{
				{Type: provider.EventTextDelta, Text: "ok"},
				{Type: provider.EventMessageStop},
			}},
		},
		mutate: func() error {
			_, err := registry.Replace(
				"dynamic:retry", registry.Generation(), tool.NewRegistration(newExecutor),
			)
			return err
		},
	}
	engine := newEngine(t, runtime, registry)
	engine.options.MaxRetries = 1
	if _, err := engine.Run(t.Context(), "retry snapshot", nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 2 {
		t.Fatalf("requests = %d", len(runtime.requests))
	}
	for index, request := range runtime.requests {
		if got := definitionDescription(request, "echo"); got != "retry v1" {
			t.Fatalf("retry request %d description = %q, want frozen v1", index, got)
		}
	}
}

func TestMCPHealthChangesEmitOnlyOnTransition(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	snapshots := []MCPHealthSnapshot{{
		Server: "remote", State: "healthy", ChangedAt: now,
	}}
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MCPHealthSnapshot = func() []MCPHealthSnapshot {
		return append([]MCPHealthSnapshot(nil), snapshots...)
	}
	engine.options.Now = func() time.Time { return now }
	var changes []MCPHealthChanged
	send := func(_ State, event Event) error {
		if event.MCPHealthChanged != nil {
			changes = append(changes, *event.MCPHealthChanged)
		}
		return nil
	}
	if err := engine.emitMCPHealthChanges(send); err != nil {
		t.Fatal(err)
	}
	if err := engine.emitMCPHealthChanges(send); err != nil {
		t.Fatal(err)
	}
	snapshots[0].State = "open"
	snapshots[0].ConsecutiveFailures = 3
	snapshots[0].LastError = "timeout"
	snapshots[0].ChangedAt = now.Add(time.Second)
	if err := engine.emitMCPHealthChanges(send); err != nil {
		t.Fatal(err)
	}
	snapshots = nil
	now = now.Add(2 * time.Second)
	if err := engine.emitMCPHealthChanges(send); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 3 ||
		changes[0].Current.State != "healthy" ||
		changes[1].PreviousState != "healthy" ||
		changes[1].Current.State != "open" ||
		changes[2].Current.State != "removed" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestExtensionLifecycleEmitsOnlyAuditableTransitions(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	snapshots := []ExtensionSnapshot{{
		Kind: "plugin", Name: "review", Version: "1.0.0",
		Source: "builtin", Publisher: "platform", Trust: "signed-registry",
		Digest: strings.Repeat("a", 64), Generation: 1, Enabled: true,
		LastAction: "install", ChangedAt: now,
	}}
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.ExtensionSnapshot = func() ([]ExtensionSnapshot, error) {
		return append([]ExtensionSnapshot(nil), snapshots...), nil
	}
	engine.options.Now = func() time.Time { return now }
	var changes []ExtensionLifecycleChanged
	send := func(_ State, event Event) error {
		if event.ExtensionLifecycle != nil {
			changes = append(changes, *event.ExtensionLifecycle)
		}
		return nil
	}
	if err := engine.emitExtensionLifecycleChanges(send); err != nil {
		t.Fatal(err)
	}
	if err := engine.emitExtensionLifecycleChanges(send); err != nil {
		t.Fatal(err)
	}
	snapshots[0].Version = "2.0.0"
	snapshots[0].Digest = strings.Repeat("b", 64)
	snapshots[0].Generation = 2
	snapshots[0].LastAction = "update"
	snapshots[0].ChangedAt = now.Add(time.Second)
	if err := engine.emitExtensionLifecycleChanges(send); err != nil {
		t.Fatal(err)
	}
	snapshots[0].Enabled = false
	snapshots[0].ChangedAt = now.Add(2 * time.Second)
	if err := engine.emitExtensionLifecycleChanges(send); err != nil {
		t.Fatal(err)
	}
	snapshots = nil
	now = now.Add(3 * time.Second)
	if err := engine.emitExtensionLifecycleChanges(send); err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 ||
		changes[0].Action != "active" ||
		changes[1].Action != "updated" ||
		changes[1].PreviousVersion != "1.0.0" ||
		changes[2].Action != "disabled" ||
		changes[3].Action != "revoked" {
		t.Fatalf("changes = %+v", changes)
	}
}

func TestSamplingFailsClosedUntilToolCatalogSyncRecovers(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	syncFailure := errors.New("fixture MCP catalog conflict")
	failing := true
	var syncCalls int
	engine.options.ToolCatalogSync = func() error {
		syncCalls++
		if failing {
			return syncFailure
		}
		return nil
	}

	_, err := engine.Run(t.Context(), "blocked", nil)
	if !protocol.IsCode(err, protocol.CodeUnavailable) ||
		!errors.Is(err, syncFailure) {
		t.Fatalf("blocked sampling error = %v", err)
	}
	if len(runtime.requests) != 0 {
		t.Fatalf("provider requests after failed sync = %d, want 0", len(runtime.requests))
	}

	failing = false
	result, err := engine.Run(t.Context(), "retry", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "done" || len(runtime.requests) != 1 || syncCalls != 2 {
		t.Fatalf(
			"result=%+v requests=%d sync calls=%d",
			result,
			len(runtime.requests),
			syncCalls,
		)
	}
}

func definitionDescription(request provider.ModelRequest, name string) string {
	for _, definition := range request.Tools {
		if definition.Name == name {
			return definition.Description
		}
	}
	return ""
}

func TestEngineToolsOffAndOnUseSameImplementation(t *testing.T) {
	for name, registry := range map[string]*tool.Registry{
		"off": nil,
		"on":  tool.NewRegistry(nil, nil),
	} {
		t.Run(name, func(t *testing.T) {
			runtime := &scriptedProvider{streams: []provider.Stream{
				&provider.SliceStream{Events: []provider.StreamEvent{
					{Type: provider.EventTextDelta, Text: "ok"},
					{Type: provider.EventMessageStop},
				}},
			}}
			engine, err := New(Options{
				Provider: runtime, Route: testRoute(t), Tools: registry,
				MaxOutputTokens: 128, MaxSteps: 2,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := engine.Run(t.Context(), "work", nil)
			if err != nil || result.Text != "ok" || len(runtime.requests) != 1 {
				t.Fatalf("result=%+v err=%v requests=%d", result, err, len(runtime.requests))
			}
		})
	}
}

func TestEngineReplaysReasoningSignatureAndNativeSearch(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventReasoningDelta, Index: 0, Text: "think"},
			{Type: provider.EventReasoningSignature, Index: 0, Signature: "signed"},
			{Type: provider.EventTextDelta, Index: 1, Text: "first"},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "second"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), NativeSearch: true,
		MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context(), "one", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context(), "two", nil); err != nil {
		t.Fatal(err)
	}
	replayed := runtime.requests[1].Messages[1].Blocks
	if len(replayed) != 2 ||
		replayed[0].Type != provider.ContentReasoning ||
		replayed[0].Text != "think" ||
		replayed[0].Signature != "signed" ||
		replayed[1].Text != "first" ||
		!runtime.requests[1].NativeSearch {
		t.Fatalf("second request = %+v", runtime.requests[1])
	}
}

func TestEngineBudgetAndFailedHistoryRollback(t *testing.T) {
	runtime := &scriptedProvider{}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), MaxOutputTokens: 128,
		Budget: Budget{MaxTokens: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(t.Context(), "too large", nil); !protocol.IsCode(err, protocol.CodeResourceExhausted) {
		t.Fatalf("Run() error = %v", err)
	}
	if history := engine.History(); len(history) != 0 {
		t.Fatalf("failed turn committed history: %+v", history)
	}

	costEngine, err := New(Options{
		Provider: &scriptedProvider{}, Route: testRoute(t), MaxOutputTokens: 128,
		Budget: Budget{MaxCostUSD: 0.000001},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := costEngine.Run(t.Context(), "cost", nil); !protocol.IsCode(err, protocol.CodeResourceExhausted) {
		t.Fatalf("cost budget error = %v", err)
	}

	runtime.streams = []provider.Stream{&errorStream{err: errors.New("failed")}}
	engine.options.Budget = Budget{}
	if _, err := engine.Run(t.Context(), "rollback", nil); err == nil {
		t.Fatal("Run() error = nil")
	}
	if history := engine.History(); len(history) != 0 {
		t.Fatalf("provider failure committed history: %+v", history)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	cancelEngine, err := New(Options{
		Provider: cancelProvider{}, Route: testRoute(t), MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cancelEngine.Run(canceled, "cancel", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Run() error = %v", err)
	}
	if history := cancelEngine.History(); len(history) != 0 {
		t.Fatalf("canceled turn committed history: %+v", history)
	}
}

func TestEngineUnauthorizedToolHasSingleFailedTerminal(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call_1", Name: "echo", Arguments: `{"text":"hello"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry, MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	var states []State
	if _, err := engine.Run(t.Context(), "work", func(event Event) error {
		states = append(states, event.State)
		return nil
	}); err == nil {
		t.Fatal("Run() error = nil")
	}
	assertOneTerminal(t, states, Failed)
}

func TestRequestCancelHasSingleCanceledTerminalAndNoCommittedHistory(t *testing.T) {
	started := make(chan struct{})
	engine, err := New(Options{
		Provider: &steerProvider{started: started},
		Route:    testRoute(t), MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	var (
		statesMu sync.Mutex
		states   []State
	)
	done := make(chan error, 1)
	go func() {
		_, runErr := engine.Run(t.Context(), "cancel active turn", func(event Event) error {
			statesMu.Lock()
			states = append(states, event.State)
			statesMu.Unlock()
			return nil
		})
		done <- runErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model stream did not start")
	}
	engine.RequestCancel()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RequestCancel did not stop the active turn")
	}
	statesMu.Lock()
	defer statesMu.Unlock()
	assertOneTerminal(t, states, Canceled)
	if history := engine.History(); len(history) != 0 {
		t.Fatalf("canceled turn committed history: %+v", history)
	}
}

func TestCanceledTurnContinuationRetainsTaskContext(t *testing.T) {
	started := make(chan struct{})
	runtime := &steerProvider{started: started}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), MaxOutputTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, runErr := engine.Run(t.Context(), "inspect extensions/vscode", nil)
		done <- runErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("model stream did not start")
	}
	engine.RequestCancelWithReason(protocol.CancelReasonUserInterrupted)
	if runErr := <-done; !errors.Is(runErr, context.Canceled) {
		t.Fatalf("canceled Run() error = %v, want context.Canceled", runErr)
	}
	if _, err := engine.Run(t.Context(), "继续", nil); err != nil {
		t.Fatal(err)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if len(runtime.requests) != 2 {
		t.Fatalf("provider requests=%d want 2", len(runtime.requests))
	}
	var userPrompts []string
	for _, message := range runtime.requests[1].Messages {
		if message.Role == provider.RoleUser {
			userPrompts = append(userPrompts, message.Text())
		}
	}
	if !slices.Contains(userPrompts, "inspect extensions/vscode") ||
		!slices.Contains(userPrompts, "继续") {
		t.Fatalf("continuation user prompts=%q", userPrompts)
	}
}

func TestRetainCanceledHistoryDropsOrphanToolTraffic(t *testing.T) {
	messages := []provider.Message{
		provider.TextMessage(provider.RoleUser, "inspect vscode"),
		{
			Role: provider.RoleAssistant,
			Blocks: []provider.ContentBlock{{
				Type: provider.ContentToolCall,
				ToolCall: &provider.ToolCall{
					ID: "paired", Name: "read", Arguments: `{}`,
				},
			}},
		},
		{
			Role: provider.RoleTool,
			Blocks: []provider.ContentBlock{{
				Type: provider.ContentToolResult,
				ToolResult: &provider.ToolResult{
					CallID: "paired", Content: "result",
				},
			}},
		},
		{
			Role: provider.RoleAssistant,
			Blocks: []provider.ContentBlock{{
				Type: provider.ContentToolCall,
				ToolCall: &provider.ToolCall{
					ID: "orphan", Name: "read", Arguments: `{}`,
				},
			}},
		},
	}
	retained := retainCanceledHistory(messages)
	if len(retained) != 3 ||
		retained[0].Text() != "inspect vscode" ||
		retained[1].Blocks[0].ToolCall.ID != "paired" ||
		retained[2].Blocks[0].ToolResult.CallID != "paired" {
		t.Fatalf("retained history=%+v", retained)
	}
}

func TestEngineCompactionPreservesTurnGroupsAndSummary(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 600
	// A budget wide enough for the whole summary: what this test is about is the
	// shape of the summary and the atomicity of the cut, not the byte ceiling.
	engine.options.SummaryMaxBytes = 4 << 10
	// The summary reports the live critical paths, so they have to be observed the
	// way a session observes them rather than set on the frozen options.
	engine.options.Workspace = "/workspace"
	engine.options.WorkingSet = []string{"/workspace/a.go"}
	engine.seedWorkingSet()
	engine.options.ContextReceipts = []promptcontext.Receipt{{
		Kind: promptcontext.PartitionWorkingSet, SourcePath: "/workspace/a.go",
		OriginalBytes: 20, RetainedBytes: 10, OriginalTokens: 5, RetainedTokens: 3,
		Digest: "sha256:test", Truncated: true, TruncationReason: "byte_budget",
	}}
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "old request", 1),
		{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{
				ID: "call_old", Name: "echo", Arguments: `{"text":"old"}`,
			},
		}}, Turn: 1},
		{Role: provider.RoleTool, Blocks: []provider.ContentBlock{{
			Type:       provider.ContentToolResult,
			ToolResult: &provider.ToolResult{CallID: "call_old", Content: strings.Repeat("old result ", 100)},
		}}, Turn: 1},
		messageWithText(provider.RoleAssistant, strings.Repeat("old answer ", 100), 1),
		messageWithText(provider.RoleUser, "current request", 2),
	}

	receipt := engine.compact()

	if len(engine.history) != 2 {
		t.Fatalf("compacted history = %+v", engine.history)
	}
	summary := engine.history[0].Text()
	if engine.history[0].Role != provider.RoleSystem ||
		!strings.Contains(summary, compact.MarkerStart) ||
		!strings.Contains(summary, "old request") ||
		!strings.Contains(summary, "call_old") ||
		!strings.Contains(summary, "Critical paths: a.go") ||
		engine.history[1].Turn != 2 {
		t.Fatalf("compacted history = %+v", engine.history)
	}
	// Partition byte and digest detail belongs to the audit trail, not to every
	// sample after the compaction.
	if strings.Contains(summary, "PromptContextReceipts") {
		t.Fatalf("summary inlined receipt detail: %s", summary)
	}
	if receipt == nil ||
		receipt.RemovedMessages != 4 ||
		len(receipt.RemovedTurns) != 1 ||
		receipt.RemovedTurns[0] != 1 ||
		receipt.SummaryTruncated ||
		len(receipt.PromptContextReceipts) != 1 ||
		len(receipt.CriticalPaths) != 1 || receipt.CriticalPaths[0] != "a.go" ||
		len(receipt.WorkingSet) != 1 {
		t.Fatalf("compaction receipt = %+v", receipt)
	}
	if !slices.Contains(receipt.Sections, compact.SectionCritical) ||
		!slices.Contains(receipt.Sections, compact.SectionDigest) {
		t.Fatalf("compaction sections = %v", receipt.Sections)
	}
	assertToolPairs(t, engine.history)
}

// The digest keeps the turns nearest the cut, because they are the ones the next
// sample continues from.
func TestEngineCompactionDropsTheOldestDigestLinesFirst(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 400
	engine.options.SummaryMaxBytes = 320
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "first ancient request "+strings.Repeat("filler ", 60), 1),
		messageWithText(provider.RoleAssistant, "an answer in the middle", 1),
		messageWithText(provider.RoleUser, "one more question", 1),
		messageWithText(provider.RoleAssistant, "last thing before the cut", 1),
		messageWithText(provider.RoleUser, "current request", 2),
	}
	receipt := engine.compact()
	if receipt == nil || !receipt.SummaryTruncated ||
		receipt.TruncationReason != "summary_byte_budget" {
		t.Fatalf("compaction receipt = %+v", receipt)
	}
	summary := engine.history[0].Text()
	if !strings.Contains(summary, "last thing before the cut") {
		t.Fatalf("summary dropped the newest removed message:\n%s", summary)
	}
	if strings.Contains(summary, "first ancient request") {
		t.Fatalf("summary kept the oldest line over the newest:\n%s", summary)
	}
}

// A second compaction has to pass the first summary through whole. Flattening it
// like an ordinary message would cut everything the first compaction preserved
// down to one line, which is how a long session loses its early history.
func TestEngineSecondCompactionCarriesTheFirstSummaryVerbatim(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 1000
	engine.options.SummaryMaxBytes = 800
	engine.ApplyPlan(interact.Plan{
		Objective: "teach the parser about trailing commas",
		Steps:     []interact.PlanStep{{Title: "update the lexer", Status: interact.StepInProgress}},
	})
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("early ", 300), 1),
		messageWithText(provider.RoleAssistant, "the first answer", 1),
		messageWithText(provider.RoleUser, "second request", 2),
	}
	if receipt := engine.compact(); receipt == nil {
		t.Fatal("expected a first compaction")
	}
	first := engine.history[0].Text()
	if _, ok := compact.Carry(first); !ok {
		t.Fatalf("first summary is not marked as one:\n%s", first)
	}

	// The plan changes before the second compaction, so a summary that regenerates
	// its sections reports the new objective while the old one survives inside the
	// carried block.
	engine.ApplyPlan(interact.Plan{
		Objective: "also accept trailing commas in calls",
		Steps:     []interact.PlanStep{{Title: "update the parser", Status: interact.StepPending}},
	})
	engine.history = append(engine.history,
		messageWithText(provider.RoleAssistant, strings.Repeat("later ", 250), 2),
		messageWithText(provider.RoleUser, "third request", 3),
	)
	if receipt := engine.compact(); receipt == nil {
		t.Fatal("expected a second compaction")
	}
	second := engine.history[0].Text()
	if !strings.Contains(second, "Goal: also accept trailing commas in calls") {
		t.Fatalf("second summary lost the current goal:\n%s", second)
	}
	if !strings.Contains(second, "Earlier summary:") ||
		!strings.Contains(second, "teach the parser about trailing commas") {
		t.Fatalf("second summary did not carry the first one:\n%s", second)
	}
	if !strings.Contains(second, "the first answer") {
		t.Fatalf("second summary lost the first summary's digest:\n%s", second)
	}
	if strings.Count(second, compact.MarkerStart) != 1 {
		t.Fatalf("carried summary nested its markers:\n%s", second)
	}
}

func TestEngineCompactStripsFragmentsAndPromptContextReinjects(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 400
	skills := promptcontext.WrapFragment(promptcontext.FragmentSkills, "skill catalog body")
	constitution := promptcontext.WrapFragment(promptcontext.FragmentConstitution, "constitution body")
	engine.options.PromptContext = []provider.Message{
		provider.TextMessage(provider.RoleSystem, skills),
		provider.TextMessage(provider.RoleSystem, constitution),
	}
	engine.history = []provider.Message{
		provider.TextMessage(provider.RoleSystem, skills),
		messageWithText(provider.RoleUser, strings.Repeat("old ", 80), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("ans ", 80), 1),
		messageWithText(provider.RoleUser, "keep", 2),
	}
	receipt := engine.CompactForced()
	if receipt == nil {
		t.Fatal("expected forced compact")
	}
	for _, message := range engine.History() {
		if promptcontext.IsContextualFragment(message.Text()) {
			t.Fatalf("history retained fragment after compact: %q", message.Text())
		}
	}
	prompt := engine.promptMessages()
	var sawSkills, sawConstitution bool
	for _, message := range prompt {
		kind, ok := promptcontext.MatchFragment(message.Text())
		if !ok {
			continue
		}
		switch kind {
		case promptcontext.FragmentSkills:
			sawSkills = true
		case promptcontext.FragmentConstitution:
			sawConstitution = true
		}
	}
	if !sawSkills || !sawConstitution {
		t.Fatalf("prompt reinjection missing skills=%v constitution=%v prompt=%+v",
			sawSkills, sawConstitution, prompt)
	}
}

func TestEngineCompactForcedRejectsAReplacementThatWouldGrowHistory(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 256 << 10
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, "old request", 1),
		messageWithText(provider.RoleAssistant, "old answer", 1),
		messageWithText(provider.RoleUser, "current request", 2),
		messageWithText(provider.RoleAssistant, "current answer", 2),
	}
	if receipt := engine.Compact(); receipt != nil {
		t.Fatalf("auto Compact under budget = %+v", receipt)
	}
	before := engine.History()
	if receipt := engine.CompactForced(); receipt != nil {
		t.Fatalf("CompactForced receipt = %+v, want no-growth rejection", receipt)
	}
	if !reflect.DeepEqual(engine.History(), before) {
		t.Fatalf("rejected compaction changed history: %+v", engine.History())
	}
}

func TestEngineCompactionRetainsToolPairingAtomically(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 500
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("discard ", 100), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("discarded ", 100), 1),
		messageWithText(provider.RoleUser, "use a tool", 2),
		{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolCall,
			ToolCall: &provider.ToolCall{
				ID: "call_keep", Name: "echo", Arguments: `{"text":"keep"}`,
			},
		}}, Turn: 2},
		{Role: provider.RoleTool, Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolResult,
			ToolResult: &provider.ToolResult{
				CallID: "call_keep", Content: "kept",
			},
		}}, Turn: 2},
		messageWithText(provider.RoleAssistant, "tool completed", 2),
		messageWithText(provider.RoleUser, "current", 3),
	}

	receipt := engine.compact()
	if receipt == nil || len(receipt.RemovedTurns) != 1 || receipt.RemovedTurns[0] != 1 {
		t.Fatalf("compaction receipt = %+v", receipt)
	}
	assertToolPairs(t, engine.history)
	for _, message := range engine.history {
		if message.Turn == 1 {
			t.Fatalf("compaction retained a partial old turn: %+v", engine.history)
		}
	}
}

func TestMidTurnCompactionCutsClosedToolPairsWithinActiveTurn(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 600
	engine.options.SummaryMaxBytes = 400
	history := []provider.Message{
		messageWithText(provider.RoleUser, "fix the parser "+strings.Repeat("context ", 80), 1),
		toolCallMessage(1, "call_1", "read", `{}`),
		toolResultMessage(1, "call_1", strings.Repeat("first ", 100)),
		toolCallMessage(1, "call_2", "read", `{}`),
		toolResultMessage(1, "call_2", strings.Repeat("second ", 100)),
		toolCallMessage(1, "call_3", "read", `{}`),
		toolResultMessage(1, "call_3", "latest"),
	}
	original := historyBytes(history)
	var receipt *CompactionReceipt
	err := engine.runMidTurnCompactGate(&history, func(_ State, event Event) error {
		receipt = event.Compaction
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt == nil || receipt.RetainedBytes >= receipt.OriginalBytes ||
		receipt.RetainedBytes > engine.options.MaxContextBytes ||
		receipt.OriginalBytes != original {
		t.Fatalf("mid-turn receipt = %+v", receipt)
	}
	if !strings.Contains(history[0].Text(), "Goal: fix the parser") {
		t.Fatalf("summary lost active goal: %q", history[0].Text())
	}
	assertToolPairs(t, history)
	if len(history) != 3 ||
		messageToolCalls(history[1])[0].ID != "call_3" ||
		messageToolResultID(history[2]) != "call_3" {
		t.Fatalf("mid-turn history = %+v", history)
	}
}

func TestMidTurnCompactionFailsClosedWhenNoSafeCandidateFits(t *testing.T) {
	engine := newEngine(t, &scriptedProvider{}, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 200
	engine.options.SummaryMaxBytes = 100
	history := []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("goal ", 100), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("active ", 100), 1),
	}
	before := cloneMessages(history)
	err := engine.runMidTurnCompactGate(&history, func(State, Event) error {
		t.Fatal("failed compaction emitted an event")
		return nil
	})
	if err == nil || protocol.CodeOf(err) != protocol.CodeResourceExhausted {
		t.Fatalf("compaction error = %v", err)
	}
	if !reflect.DeepEqual(history, before) {
		t.Fatalf("failed compaction changed history: %+v", history)
	}
}

func TestTerminalCompletionFailsClosedWhenFinalMessageExceedsContextBudget(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		textStream(strings.Repeat("final ", 200)),
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 128
	engine.options.SummaryMaxBytes = 64
	var completed bool

	result, err := engine.Run(t.Context(), "short request", func(event Event) error {
		completed = completed || event.State == Completed
		return nil
	})

	if err == nil || protocol.CodeOf(err) != protocol.CodeResourceExhausted {
		t.Fatalf("result=%+v error=%v, want resource_exhausted", result, err)
	}
	if completed || result.State == Completed {
		t.Fatalf("over-budget terminal was completed: result=%+v emitted=%v", result, completed)
	}
}

func TestTerminalSeparatesPrimaryAndContextFinalizationFailure(t *testing.T) {
	var terminal Event
	handler := newTerminalHandler(2, func(event Event) error {
		terminal = event
		return nil
	})
	primary := protocol.NewProblem(
		protocol.CodeConflict, "primary verification conflict", false, nil,
	)
	secondary := protocol.NewProblem(
		protocol.CodeResourceExhausted, "history compaction failed", false, nil,
	)
	handler.setPrimary(primary)
	handler.addSecondary("terminal_context", secondary)
	resultErr := errors.Join(primary, secondary)
	result := Result{}
	handler.finish(t.Context(), &result, &resultErr)

	if terminal.ErrorCode != protocol.CodeConflict ||
		terminal.Error != "primary verification conflict" {
		t.Fatalf("primary terminal error = %+v", terminal)
	}
	if len(terminal.SecondaryIssues) != 1 ||
		terminal.SecondaryIssues[0].Phase != "terminal_context" ||
		terminal.SecondaryIssues[0].Code != protocol.CodeResourceExhausted {
		t.Fatalf("secondary issues = %+v", terminal.SecondaryIssues)
	}
}

func TestFailedTurnFinalizesDurableHistoryBeforeTerminalEvent(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&errorStream{err: errors.New("provider failed")},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 500
	engine.options.SummaryMaxBytes = 160
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("old request ", 10), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("old answer ", 10), 1),
		messageWithText(provider.RoleUser, strings.Repeat("recent request ", 10), 2),
		messageWithText(provider.RoleAssistant, strings.Repeat("recent answer ", 10), 2),
	}
	engine.turn = 2
	var terminalBudget *ContextBudgetSnapshot
	var postTurnCompaction bool

	_, err := engine.Run(t.Context(), "new request", func(event Event) error {
		if event.Compaction != nil &&
			event.Compaction.Phase == CompactionPhasePostTurn {
			postTurnCompaction = true
		}
		if event.State == Failed {
			terminalBudget = event.ContextBudget
		}
		return nil
	})

	if err == nil || !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("Run() error = %v", err)
	}
	if !postTurnCompaction {
		t.Fatal("failed turn did not emit post-turn compaction")
	}
	if terminalBudget == nil ||
		terminalBudget.HistoryBytes > terminalBudget.MaxHistoryBytes {
		t.Fatalf("terminal context budget = %+v", terminalBudget)
	}
	historyBytes, maxHistoryBytes := engine.ContextBudget()
	if historyBytes != terminalBudget.HistoryBytes ||
		maxHistoryBytes != terminalBudget.MaxHistoryBytes {
		t.Fatalf(
			"durable history = %d/%d, terminal snapshot = %+v",
			historyBytes, maxHistoryBytes, terminalBudget,
		)
	}
}

func TestFailedTurnCompactsWithinOversizedDurableLastTurn(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&errorStream{err: errors.New("provider failed")},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 500
	engine.options.SummaryMaxBytes = 160
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("durable request ", 20), 1),
		toolCallMessage(1, "call_1", "read", `{}`),
		toolResultMessage(1, "call_1", strings.Repeat("first result ", 40)),
		toolCallMessage(1, "call_2", "read", `{}`),
		toolResultMessage(1, "call_2", strings.Repeat("second result ", 40)),
		toolCallMessage(1, "call_3", "read", `{}`),
		toolResultMessage(1, "call_3", "latest result"),
	}
	engine.turn = 1
	var terminalBudget *ContextBudgetSnapshot
	var postTurn *CompactionReceipt

	_, err := engine.Run(t.Context(), "new request", func(event Event) error {
		if event.Compaction != nil &&
			event.Compaction.Phase == CompactionPhasePostTurn {
			postTurn = event.Compaction
		}
		if event.State == Failed {
			terminalBudget = event.ContextBudget
		}
		return nil
	})

	if err == nil || !strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("Run() error = %v", err)
	}
	if postTurn == nil || postTurn.OriginalBytes <= postTurn.RetainedBytes {
		t.Fatalf("post-turn compaction = %+v", postTurn)
	}
	if terminalBudget == nil ||
		terminalBudget.HistoryBytes > terminalBudget.MaxHistoryBytes {
		t.Fatalf("terminal context budget = %+v", terminalBudget)
	}
	assertToolPairs(t, engine.history)
	for _, message := range engine.history {
		if strings.Contains(message.Text(), "new request") {
			t.Fatalf("failed transaction entered durable history: %+v", engine.history)
		}
	}
}

func TestEngineEmitsStructuredCompactionReceipt(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 200
	engine.options.ContextReceipts = []promptcontext.Receipt{{
		Kind: promptcontext.PartitionBase, SourcePath: "builtin://base-system",
		OriginalBytes: 4, RetainedBytes: 4, OriginalTokens: 1, RetainedTokens: 1,
		Digest: "sha256:base",
	}}
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("old ", 100), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("answer ", 100), 1),
	}
	engine.turn = 1
	var compaction *CompactionReceipt
	var states []State
	if _, err := engine.Run(t.Context(), "new request", func(event Event) error {
		states = append(states, event.State)
		if event.State == Compacting {
			compaction = event.Compaction
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if compaction == nil ||
		compaction.Phase != CompactionPhasePreSampling ||
		compaction.RemovedMessages != 2 ||
		len(compaction.PromptContextReceipts) != 1 ||
		!compaction.SummaryTruncated {
		t.Fatalf("compaction event = %+v", compaction)
	}
	assertStateOrder(t, states, Preparing, Compacting, CallingModel)
}

func assertStateOrder(t *testing.T, got []State, want ...State) {
	t.Helper()
	index := 0
	for _, state := range got {
		if index < len(want) && state == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("state order %v missing prefix %v", got, want)
	}
}

func TestEnginePreSamplingGateBeforeModelCall(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "ok"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	engine.options.MaxContextBytes = 400
	engine.history = []provider.Message{
		messageWithText(provider.RoleUser, strings.Repeat("old ", 80), 1),
		messageWithText(provider.RoleAssistant, strings.Repeat("ans ", 80), 1),
	}
	engine.turn = 1
	var sawCompact bool
	var states []State
	if _, err := engine.Run(t.Context(), "fresh", func(event Event) error {
		states = append(states, event.State)
		if event.State == Compacting {
			sawCompact = true
			if event.Compaction == nil || event.Compaction.Phase != CompactionPhasePreSampling {
				t.Fatalf("pre-sampling receipt = %+v", event.Compaction)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !sawCompact {
		t.Fatal("expected pre-sampling compact gate")
	}
	assertStateOrder(t, states, Preparing, Compacting, CallingModel)
	if len(runtime.requests) != 1 {
		t.Fatalf("model requests = %d, want 1 after gate", len(runtime.requests))
	}
}

func TestEngineSteerContinuesCurrentTurnWithoutStaleInput(t *testing.T) {
	runtime := &steerProvider{started: make(chan struct{})}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	done := make(chan error, 1)
	go func() {
		_, err := engine.Run(t.Context(), "initial", nil)
		done <- err
	}()
	<-runtime.started
	if err := engine.Steer("change direction"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) != 2 {
		t.Fatalf("requests = %+v", runtime.requests)
	}
	second := runtime.requests[1].Messages
	if len(second) != 3 ||
		second[0].Text() != "initial" ||
		second[1].Text() != "partial" ||
		second[2].Text() != "change direction" {
		t.Fatalf("steered messages = %+v", second)
	}
	if err := engine.Steer("stale"); err == nil {
		t.Fatal("Steer() after completion succeeded")
	}
}

func TestEnginePendingInputFIFOSteerAndMailbox(t *testing.T) {
	runtime := &steerProvider{started: make(chan struct{})}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	done := make(chan error, 1)
	go func() {
		_, err := engine.Run(t.Context(), "initial", nil)
		done <- err
	}()
	<-runtime.started
	if err := engine.EnqueueMailbox("mail-first", true); err != nil {
		t.Fatal(err)
	}
	if err := engine.Steer("steer-second"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(runtime.requests) < 2 {
		t.Fatalf("requests = %d, want >= 2", len(runtime.requests))
	}
	second := runtime.requests[1].Messages
	if len(second) < 4 {
		t.Fatalf("steered messages = %+v", second)
	}
	// After initial + partial assistant, FIFO: mailbox then steer.
	if second[2].Text() != "[mailbox] mail-first" {
		t.Fatalf("mailbox text = %q", second[2].Text())
	}
	if second[3].Text() != "steer-second" {
		t.Fatalf("steer text = %q", second[3].Text())
	}
}

func TestEngineMailboxNonTriggerHeldUntilNextTurn(t *testing.T) {
	runtime := &steerProvider{started: make(chan struct{})}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	done := make(chan error, 1)
	go func() {
		_, err := engine.Run(t.Context(), "turn-1", nil)
		done <- err
	}()
	<-runtime.started
	if err := engine.EnqueueMailbox("late-mail", false); err != nil {
		t.Fatal(err)
	}
	if err := engine.Steer("nudge"); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	second := runtime.requests[1].Messages
	for _, msg := range second {
		if strings.Contains(msg.Text(), "late-mail") {
			t.Fatalf("non-trigger mailbox leaked into current turn: %+v", second)
		}
	}
	// Next turn should promote held mailbox before/with user prompt inject path.
	runtime2 := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "ok"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine.options.Provider = runtime2
	if _, err := engine.Run(t.Context(), "turn-2", nil); err != nil {
		t.Fatal(err)
	}
	if len(runtime2.requests) != 1 {
		t.Fatalf("next-turn requests = %d", len(runtime2.requests))
	}
	msgs := runtime2.requests[0].Messages
	found := false
	for _, msg := range msgs {
		if msg.Text() == "[mailbox] late-mail" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("held mailbox not promoted on next turn: %+v", msgs)
	}
}

func TestEngineUndoRemovesCompleteToolTurnAndForkIsIndependent(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "call_1", Name: "echo", Arguments: `{"text":"hello"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&echoTool{}, nil); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, runtime, registry)
	if _, err := engine.Run(t.Context(), "work", nil); err != nil {
		t.Fatal(err)
	}
	fork := engine.Fork()
	fork.history[1].Blocks[0].ToolCall.Name = "changed"
	if engine.history[1].Blocks[0].ToolCall.Name != "echo" {
		t.Fatal("Fork() shares tool-call backing storage")
	}
	if !engine.Undo() || len(engine.History()) != 0 {
		t.Fatalf("history after undo = %+v", engine.History())
	}
}

func TestEngineCloneEmptyIsolatesHistoryAndGuard(t *testing.T) {
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "hello"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine := newEngine(t, runtime, tool.NewRegistry(nil, nil))
	if _, err := engine.Run(t.Context(), "seed", nil); err != nil {
		t.Fatal(err)
	}
	clone, err := engine.CloneEmpty()
	if err != nil {
		t.Fatal(err)
	}
	if len(clone.History()) != 0 {
		t.Fatalf("clone history = %+v", clone.History())
	}
	if len(engine.History()) == 0 {
		t.Fatal("source history cleared")
	}
	if clone.guard == nil || clone.guard == engine.guard {
		t.Fatal("CloneEmpty must allocate a distinct Guard")
	}
}

func TestPostEditDiagnosticsTwoStepFixture(t *testing.T) {
	_, runtime, path := runWorkspaceEditTurn(t, "turn-diagnostics")
	if len(runtime.requests) != 3 {
		t.Fatalf("model requests = %d, want 3", len(runtime.requests))
	}
	var secondStepSawDiagnostics bool
	for _, message := range runtime.requests[2].Messages {
		for _, block := range message.Blocks {
			if block.ToolResult != nil && strings.Contains(block.ToolResult.Content, "fake diagnostic") {
				secondStepSawDiagnostics = true
			}
		}
	}
	if !secondStepSawDiagnostics {
		t.Fatal("model step after edit did not receive structured diagnostics")
	}
	if data, _ := os.ReadFile(path); string(data) != "after\n" {
		t.Fatalf("edited workspace content = %q", data)
	}
}

func TestWorkspaceRevertContract(t *testing.T) {
	engine, _, path := runWorkspaceEditTurn(t, "turn-revert")
	receipt, err := engine.RevertWorkspace(t.Context(), "turn-revert")
	if err != nil {
		t.Fatalf("revert error = %v receipt=%+v", err, receipt)
	}
	if len(receipt.Restored) != 1 || len(receipt.Conflicts) != 0 ||
		receipt.NonFileSideEffectsReverted {
		t.Fatalf("revert receipt = %+v", receipt)
	}
	if data, _ := os.ReadFile(path); string(data) != "before\n" {
		t.Fatalf("workspace after revert = %q", data)
	}
	if len(engine.History()) != 0 {
		t.Fatalf("history after workspace revert = %+v", engine.History())
	}
}

func runWorkspaceEditTurn(
	t *testing.T, turnID string,
) (*Engine, *scriptedProvider, string) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, "value.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := contentstore.NewMemory(contentstore.Options{})
	registry := tool.NewRegistry(nil, tool.NewResultStoreWithStore(32<<10, store))
	files, err := filetool.NewWithBackend(root, engineSandboxBackend{root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := files.Register(registry); err != nil {
		t.Fatal(err)
	}
	journal, err := workspacejournal.New(root, store)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "read", Name: "file_read", Arguments: `{"path":"value.txt"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "edit", Name: "file_edit",
				Arguments: `{"path":"value.txt","old":"before","new":"after"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry, Workspace: root,
		MaxOutputTokens: 128, Journal: journal, Diagnostics: fakeDiagnosticRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunForTurn(t.Context(), turnID, "edit", nil); err != nil {
		t.Fatal(err)
	}
	return engine, runtime, path
}

type fakeDiagnosticRunner struct{}

func (fakeDiagnosticRunner) Run(_ context.Context, path string) (diagnostics.Receipt, error) {
	return diagnostics.Receipt{
		Path: path, Status: "completed", Runner: "fake",
		Diagnostics: []diagnostics.Diagnostic{{
			Path: path, Severity: "warning", Code: "fixture",
			Message: "fake diagnostic", Source: "fake",
		}},
	}, nil
}

func newEngine(t *testing.T, runtime provider.Provider, registry *tool.Registry) *Engine {
	t.Helper()
	engine, err := New(Options{
		Provider: runtime, Route: testRoute(t), Tools: registry, MaxOutputTokens: 128,
		Authorize: func(provider.ToolCall) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func messageWithText(role provider.Role, text string, turn uint64) provider.Message {
	message := provider.TextMessage(role, text)
	message.Turn = turn
	return message
}

func toolCallMessage(
	turn uint64,
	id string,
	name string,
	arguments string,
) provider.Message {
	return provider.Message{
		Role: provider.RoleAssistant, Turn: turn,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolCall,
			ToolCall: &provider.ToolCall{
				ID: id, Name: name, Arguments: arguments,
			},
		}},
	}
}

func toolResultMessage(turn uint64, id string, content string) provider.Message {
	return provider.Message{
		Role: provider.RoleTool, Turn: turn,
		Blocks: []provider.ContentBlock{{
			Type: provider.ContentToolResult,
			ToolResult: &provider.ToolResult{
				CallID: id, Content: content,
			},
		}},
	}
}

func testRoute(t *testing.T) model.ReadyRoute {
	return testRouteProtocol(t, "http://127.0.0.1:1", model.ProtocolOpenAIChat)
}

func testRouteProtocol(t *testing.T, endpoint string, protocol model.WireProtocol) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "test", Kind: model.ProviderCustom, Endpoint: endpoint,
		Protocol: protocol, Provenance: model.ProvenanceFixture,
		Models: map[string]model.Model{"model": {
			ID: "model", CanonicalID: "model", WireID: "model",
			Limits:       model.Limits{ContextTokens: 4096, MaxOutputTokens: 1024},
			Capabilities: model.Capabilities{Streaming: true, ToolCalls: true},
			Pricing: model.Pricing{
				InputPerMillion: 1, OutputPerMillion: 1,
				Currency: "USD", Known: true, Provenance: model.ProvenanceFixture,
			},
			Provenance: model.ProvenanceFixture,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := model.NewResolver(catalog)
	if err != nil {
		t.Fatal(err)
	}
	route, err := resolver.Resolve(model.RouteRequest{ProviderID: "test", ModelID: "model"})
	if err != nil {
		t.Fatal(err)
	}
	return route
}

type scriptedProvider struct {
	streams  []provider.Stream
	requests []provider.ModelRequest
}

type cancelProvider struct{}

func (cancelProvider) Stream(ctx context.Context, _ provider.ModelRequest) (provider.Stream, error) {
	return nil, ctx.Err()
}

func (p *scriptedProvider) Stream(_ context.Context, request provider.ModelRequest) (provider.Stream, error) {
	p.requests = append(p.requests, request)
	if len(p.streams) == 0 {
		return nil, errors.New("no stream")
	}
	stream := p.streams[0]
	p.streams = p.streams[1:]
	return stream, nil
}

type catalogMutationProvider struct {
	registry *tool.Registry
	streams  []provider.Stream
	requests []provider.ModelRequest
	mutate   func() error
	calls    int
}

func (p *catalogMutationProvider) Stream(
	_ context.Context,
	request provider.ModelRequest,
) (provider.Stream, error) {
	p.requests = append(p.requests, request)
	if p.calls == 0 && p.mutate != nil {
		if err := p.mutate(); err != nil {
			return nil, err
		}
	}
	p.calls++
	if len(p.streams) == 0 {
		return nil, errors.New("no stream")
	}
	stream := p.streams[0]
	p.streams = p.streams[1:]
	return stream, nil
}

type errorStream struct {
	err error
}

func (s *errorStream) Recv() (provider.StreamEvent, error) {
	if s.err == nil {
		return provider.StreamEvent{}, io.EOF
	}
	err := s.err
	s.err = nil
	return provider.StreamEvent{}, err
}

func (*errorStream) Close() error { return nil }

type steerProvider struct {
	mu       sync.Mutex
	started  chan struct{}
	requests []provider.ModelRequest
	calls    atomic.Int32
}

func (p *steerProvider) Stream(ctx context.Context, request provider.ModelRequest) (provider.Stream, error) {
	p.mu.Lock()
	p.requests = append(p.requests, request)
	p.mu.Unlock()
	if p.calls.Add(1) == 1 {
		return &steerStream{ctx: ctx, started: p.started}, nil
	}
	return &provider.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "final"},
		{Type: provider.EventMessageStop},
	}}, nil
}

type steerStream struct {
	ctx     context.Context
	started chan struct{}
	step    int
}

func (s *steerStream) Recv() (provider.StreamEvent, error) {
	if s.step == 0 {
		s.step++
		close(s.started)
		return provider.StreamEvent{Type: provider.EventTextDelta, Text: "partial"}, nil
	}
	<-s.ctx.Done()
	return provider.StreamEvent{}, s.ctx.Err()
}

func (*steerStream) Close() error { return nil }

type echoTool struct {
	calls atomic.Int32
}

type countingCatalogExecutor struct {
	descriptor tool.Descriptor
	calls      atomic.Int32
}

func echoDescriptor() tool.Descriptor {
	return (&echoTool{}).Descriptor()
}

func (e *countingCatalogExecutor) Descriptor() tool.Descriptor {
	return e.descriptor
}

func (e *countingCatalogExecutor) Execute(
	_ context.Context,
	raw json.RawMessage,
) (tool.Result, error) {
	e.calls.Add(1)
	return tool.Result{Content: string(raw)}, nil
}

type failingTool struct{}

func (failingTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "fail", Description: "always fail", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

func (failingTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{}, errors.New("intentional failure")
}

type resultErrorTool struct{}

func (resultErrorTool) Descriptor() tool.Descriptor {
	descriptor := failingTool{}.Descriptor()
	descriptor.Name = "result_error"
	descriptor.Description = "return a structured tool failure"
	return descriptor
}

func (resultErrorTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "structured failure", IsError: true}, nil
}

type panickingTool struct{}

func (panickingTool) Descriptor() tool.Descriptor {
	descriptor := failingTool{}.Descriptor()
	descriptor.Name = "panic_tool"
	descriptor.Description = "panic fixture"
	return descriptor
}

func (panickingTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	panic("intentional panic")
}

func (*echoTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "echo", Description: "echo text", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityRead, AccessMode: tool.AccessRead,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"text": map[string]any{"type": "string"}},
			"required":   []string{"text"}, "additionalProperties": false,
		},
	}
}

func (t *echoTool) Execute(_ context.Context, raw json.RawMessage) (tool.Result, error) {
	t.calls.Add(1)
	return tool.Result{Content: string(raw)}, nil
}

func assertOneTerminal(t *testing.T, states []State, want State) {
	t.Helper()
	count := 0
	var got State
	for _, state := range states {
		if state == Completed || state == Failed || state == Canceled {
			count++
			got = state
		}
	}
	if count != 1 || got != want {
		t.Fatalf("terminal count=%d state=%q all=%v", count, got, states)
	}
}

func assertToolPairs(t *testing.T, messages []provider.Message) {
	t.Helper()
	calls := make(map[string]struct{})
	results := make(map[string]struct{})
	for _, message := range messages {
		for _, call := range messageToolCalls(message) {
			calls[call.ID] = struct{}{}
		}
		if id := messageToolResultID(message); id != "" {
			results[id] = struct{}{}
		}
	}
	if len(calls) != len(results) {
		t.Fatalf("tool pairing mismatch: calls=%v results=%v", calls, results)
	}
	for id := range calls {
		if _, exists := results[id]; !exists {
			t.Fatalf("tool call %q has no retained result", id)
		}
	}
}
