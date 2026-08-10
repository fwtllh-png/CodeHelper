package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestValidToolArgumentsOmitsMalformedProviderPayload(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "valid", value: `{"path":"a.go"}`, want: `{"path":"a.go"}`},
		{name: "empty", value: ""},
		{name: "whitespace", value: "  \n"},
		{name: "truncated", value: `{"path":`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			got := validToolArguments(testCase.value)
			if string(got) != testCase.want {
				t.Fatalf(
					"validToolArguments(%q) = %q, want %q",
					testCase.value, got, testCase.want,
				)
			}
			data, err := json.Marshal(&protocol.ToolStartData{
				Tool: "read", CallID: "call-1", Arguments: got,
			})
			if err != nil {
				t.Fatalf("marshal safe ToolStartData: %v", err)
			}
			if !json.Valid(data) {
				t.Fatalf("encoded ToolStartData is invalid: %s", data)
			}
		})
	}
}

func TestWorkspaceChangeReceiptMatchesTerminalOutcome(t *testing.T) {
	t.Run("failed_without_changes", func(t *testing.T) {
		worker, err := agentengine.New(agentengine.Options{
			Provider: &singleAnswerProvider{}, Route: runtimeTestRoute(t),
			Tools: tool.NewRegistry(nil, nil),
			Security: policy.DefaultRuntime(
				policy.ModeAct,
				policy.PermissionSuggest,
			),
			Workspace: t.TempDir(), Metrics: telemetry.NewMetrics(),
			MaxOutputTokens: 128,
		})
		if err != nil {
			t.Fatal(err)
		}
		receipt, terminal := runWorkspaceChangeTurn(t, worker)
		if terminal.Kind != protocol.EventTurnFailed {
			t.Fatalf("terminal = %s, want turn.failed", terminal.Kind)
		}
		if receipt.Outcome != "" || len(receipt.Changes) != 0 ||
			receipt.WorkspaceOutcome == nil ||
			receipt.WorkspaceOutcome.Status != "unchanged" {
			t.Fatalf("failed receipt = %+v", receipt)
		}
	})

	t.Run("completed_with_changes", func(t *testing.T) {
		registry := tool.NewRegistry(nil, nil)
		if err := registry.Register(&runtimeWriteTool{}, nil); err != nil {
			t.Fatal(err)
		}
		worker, err := agentengine.New(agentengine.Options{
			Provider: &runtimeApprovalProvider{}, Route: runtimeTestRoute(t),
			Tools: registry, Security: policy.DefaultRuntime(
				policy.ModeAct,
				policy.PermissionBypass,
			),
			Workspace: t.TempDir(), Metrics: telemetry.NewMetrics(),
			MaxOutputTokens: 128,
			Verify: agentengine.VerifyOptions{
				Mode: agentengine.VerifyModeHard, Scope: verify.ScopeDiagnostics,
				Runner: passingVerifier{},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		receipt, terminal := runWorkspaceChangeTurn(t, worker)
		if terminal.Kind != protocol.EventTurnCompleted {
			t.Fatalf("terminal = %s, want turn.completed", terminal.Kind)
		}
		if receipt.Outcome != protocol.TurnOutcomeChanged ||
			len(receipt.Changes) != 1 ||
			receipt.WorkspaceOutcome == nil ||
			receipt.WorkspaceOutcome.Status != "changed" {
			t.Fatalf("completed receipt = %+v", receipt)
		}
	})
}

type passingVerifier struct{}

func (passingVerifier) Verify(
	context.Context, verify.Request,
) (verify.Receipt, error) {
	return verify.Receipt{
		Scope: verify.ScopeDiagnostics, Status: verify.StatusPassed,
	}, nil
}

func runWorkspaceChangeTurn(
	t *testing.T,
	worker *agentengine.Engine,
) (*protocol.ExecutionReceiptData, protocol.Event) {
	t.Helper()
	runtime := NewRuntime(Options{Engine: AdaptEngine(worker)})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	events, err := runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "prompt",
		Prompt: "change the workspace", Intent: protocol.TurnIntentWorkspaceChange,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}
	var receipt *protocol.ExecutionReceiptData
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if data, ok := event.Data.(*protocol.ExecutionReceiptData); ok {
				receipt = data
			}
			if protocol.IsTerminalEvent(event.Kind) {
				if receipt == nil {
					t.Fatal("terminal event arrived without an execution receipt")
				}
				return receipt, event
			}
		case <-deadline:
			t.Fatal("turn did not reach a terminal event")
		}
	}
}

type singleAnswerProvider struct{}

func (*singleAnswerProvider) Stream(
	context.Context,
	provider.ModelRequest,
) (provider.Stream, error) {
	return &provider.SliceStream{Events: []provider.StreamEvent{
		{Type: provider.EventTextDelta, Text: "no changes needed"},
		{Type: provider.EventMessageStop},
	}}, nil
}

func TestRuntimeApprovalPauseResumeE2E(t *testing.T) {
	for _, decision := range []protocol.ApprovalDecision{
		protocol.ApprovalApprove, protocol.ApprovalDeny, protocol.ApprovalCancel,
	} {
		t.Run(string(decision), func(t *testing.T) {
			registry := tool.NewRegistry(nil, nil)
			executor := &runtimeWriteTool{}
			if err := registry.Register(executor, nil); err != nil {
				t.Fatal(err)
			}
			security := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
			worker, err := agentengine.New(agentengine.Options{
				Provider: &runtimeApprovalProvider{}, Route: runtimeTestRoute(t),
				Tools: registry, Security: security, Workspace: t.TempDir(),
				Metrics: telemetry.NewMetrics(), MaxOutputTokens: 128,
			})
			if err != nil {
				t.Fatal(err)
			}
			runtime := NewRuntime(Options{Engine: AdaptEngine(worker)})
			defer runtime.Close(context.Background())
			events, err := runtime.Events(t.Context(), 0)
			if err != nil {
				t.Fatal(err)
			}
			start, err := protocol.NewOperation(&protocol.StartTurnPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "prompt", Prompt: "write",
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.Submit(t.Context(), start); err != nil {
				t.Fatal(err)
			}

			var required *protocol.ApprovalRequiredData
			deadline := time.After(3 * time.Second)
			for required == nil {
				select {
				case event := <-events:
					if data, ok := event.Data.(*protocol.ApprovalRequiredData); ok {
						required = data
					}
				case <-deadline:
					t.Fatal("approval.required was not emitted")
				}
			}
			if executor.calls.Load() != 0 {
				t.Fatal("tool executed before approval decision")
			}
			approval, err := protocol.NewOperation(&protocol.ApprovalDecisionPayload{
				ThreadID: "thread", TurnID: "turn", ItemID: "approval",
				RequestID: required.RequestID, Decision: decision,
				Scope: protocol.ApprovalScopeOnce, ExpiresAt: required.ExpiresAt,
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := runtime.Submit(t.Context(), approval); err != nil {
				t.Fatal(err)
			}

			wantTerminal := protocol.EventTurnCompleted
			wantCalls := int32(1)
			switch decision {
			case protocol.ApprovalDeny:
				// Decline feeds a tool error; model continues and completes.
				wantTerminal, wantCalls = protocol.EventTurnCompleted, 0
			case protocol.ApprovalCancel:
				wantTerminal, wantCalls = protocol.EventTurnCanceled, 0
			}
			for {
				select {
				case event := <-events:
					if protocol.IsTerminalEvent(event.Kind) {
						if event.Kind != wantTerminal {
							t.Fatalf("terminal = %s, want %s", event.Kind, wantTerminal)
						}
						if executor.calls.Load() != wantCalls {
							t.Fatalf("tool calls = %d, want %d", executor.calls.Load(), wantCalls)
						}
						return
					}
				case <-deadline:
					t.Fatal("turn did not resume to terminal event")
				}
			}
		})
	}
}

type runtimeWriteTool struct{ calls atomic.Int32 }

func (*runtimeWriteTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "write", Description: "write fixture", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityWrite, AccessMode: tool.AccessWrite,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "file", Field: "path", Access: tool.AccessWrite,
		}}},
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "minLength": float64(1)},
			},
			"required": []string{"path"}, "additionalProperties": false,
		},
	}
}

func (t *runtimeWriteTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	t.calls.Add(1)
	return tool.Result{
		Content: "written",
		Metadata: map[string]any{
			toolguard.MetadataChanges: []toolguard.FileChange{{
				Path: "out.txt", Kind: toolguard.FileCreated, Added: 1,
			}},
		},
	}, nil
}

type runtimeApprovalProvider struct {
	mu    sync.Mutex
	calls int
}

func (p *runtimeApprovalProvider) Stream(
	_ context.Context, _ provider.ModelRequest,
) (provider.Stream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	switch p.calls {
	case 1:
		return &provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				Index: 0, ID: "call_write", Name: "write", Arguments: `{"path":"out.txt"}`,
			}},
			{Type: provider.EventMessageStop},
		}}, nil
	case 2:
		return &provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}}, nil
	case 3:
		return &provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}}, nil
	default:
		return nil, errors.New("unexpected provider call")
	}
}

func runtimeTestRoute(t *testing.T) model.ReadyRoute {
	t.Helper()
	catalog, err := model.NewCatalog(model.Provider{
		ID: "test", Kind: model.ProviderCustom, Endpoint: "http://127.0.0.1:1",
		Protocol: model.ProtocolOpenAIChat, Provenance: model.ProvenanceFixture,
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
