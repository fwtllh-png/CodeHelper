package engine

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestSnapshotTurnSpecFreezesSessionInputs(t *testing.T) {
	security := policy.DefaultRuntime(policy.ModeOperate, policy.PermissionAuto)
	security.Repository = []policy.Rule{{
		Tool: "shell_run", Resource: "*", Action: policy.ActionAsk,
	}}
	registry := tool.NewRegistry(nil, nil)
	registry.SetSandboxBackend(turnContextBackend{})
	options := Options{
		Route: testRoute(t), Tools: registry, Security: security, Workspace: "/tmp/ws",
	}
	snapshot, err := SnapshotTurnSpec(
		options,
		TurnIdentity{SessionID: "session-1", TurnID: "turn-1", ProfileRevision: 7},
		TurnRequest{Prompt: "inspect", Intent: protocol.TurnIntentAnswer},
	)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Provider == "" || snapshot.Model == "" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.Identity.TurnID != "turn-1" ||
		snapshot.Identity.ProfileRevision != 7 ||
		snapshot.Request.Prompt != "inspect" ||
		snapshot.Catalog.Generation == 0 {
		t.Fatalf("identity/request/catalog not frozen: %+v", snapshot)
	}
	// Operate is act with wider permissions, not a purpose of its own.
	if snapshot.Purpose != model.PurposeAct {
		t.Fatalf("purpose = %q, want act", snapshot.Purpose)
	}
	if snapshot.Mode != policy.ModeOperate || snapshot.Posture != policy.PermissionAuto {
		t.Fatalf("mode/posture = %s/%s", snapshot.Mode, snapshot.Posture)
	}
	if snapshot.Workspace != "/tmp/ws" || snapshot.Sandbox != "test/test/strong" {
		t.Fatalf("workspace/sandbox = %q/%q", snapshot.Workspace, snapshot.Sandbox)
	}
	if snapshot.Policy == nil || snapshot.Policy == security {
		t.Fatal("snapshot must allocate a distinct sampling policy")
	}
	security.Mode = policy.ModePlan
	security.Permission = policy.PermissionNever
	if snapshot.Policy.Mode != policy.ModeOperate || snapshot.Policy.Permission != policy.PermissionAuto {
		t.Fatalf("clone mutated with session: %+v", snapshot.Policy)
	}
	if len(snapshot.Policy.Repository) != 1 {
		t.Fatalf("repository not copied: %+v", snapshot.Policy.Repository)
	}
	security.Repository[0].Action = policy.ActionDeny
	if snapshot.Policy.Repository[0].Action != policy.ActionAsk {
		t.Fatal("repository slice must be copied")
	}
}

func TestRunForTurnIgnoresMidTurnPolicyMutation(t *testing.T) {
	security := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&turnWriteTool{}, nil); err != nil {
		t.Fatal(err)
	}
	providerRuntime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "w1", Name: "write", Arguments: `{"path":"a","value":"x"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "w2", Name: "write", Arguments: `{"path":"b","value":"y"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine, err := New(Options{
		Provider: providerRuntime, Route: testRoute(t), Tools: registry,
		Security: security, Workspace: t.TempDir(), MaxOutputTokens: 128, MaxSteps: 8,
		Authorize: func(provider.ToolCall) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu        sync.Mutex
		approvals int
		started   Event
	)
	done := make(chan error, 1)
	go func() {
		_, runErr := engine.RunForTurn(context.Background(), "freeze-1", "edit files", func(event Event) error {
			mu.Lock()
			defer mu.Unlock()
			if event.State == Preparing {
				started = event
			}
			if event.State == AwaitingApproval && event.Approval != nil {
				approvals++
				// Mid-turn host mutation: session would flip to bypass.
				security.Permission = policy.PermissionBypass
				security.Mode = policy.ModeOperate
				go func(requestID string) {
					time.Sleep(10 * time.Millisecond)
					_ = mustControl(t, engine).ResolveApproval(toolguard.ApprovalDecision{
						RequestID: requestID, Approved: true,
						Scope: policy.ApprovalOnce, ExpiresAt: time.Now().Add(time.Minute),
					})
				}(event.Approval.RequestID)
			}
			return nil
		})
		done <- runErr
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn timed out")
	}

	mu.Lock()
	defer mu.Unlock()
	if started.Mode != string(policy.ModeAct) || started.Posture != string(policy.PermissionSuggest) {
		t.Fatalf("started context = mode=%q posture=%q", started.Mode, started.Posture)
	}
	// Two write tools under Suggest → two asks even after session flipped to bypass.
	if approvals != 2 {
		t.Fatalf("approvals = %d, want 2 (mid-turn bypass must not apply)", approvals)
	}
	if security.Permission != policy.PermissionBypass {
		t.Fatal("session permission should remain mutated for the next turn")
	}
}

func TestRunForTurnNextTurnSeesUpdatedPolicy(t *testing.T) {
	security := policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	registry := tool.NewRegistry(nil, nil)
	if err := registry.Register(&turnWriteTool{}, nil); err != nil {
		t.Fatal(err)
	}
	providerRuntime := &scriptedProvider{streams: []provider.Stream{
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "w1", Name: "write", Arguments: `{"path":"a","value":"x"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "first"},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventToolCallDelta, ToolCall: &provider.ToolCallFragment{
				ID: "w2", Name: "write", Arguments: `{"path":"b","value":"y"}`,
			}},
			{Type: provider.EventMessageStop},
		}},
		&provider.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventTextDelta, Text: "second"},
			{Type: provider.EventMessageStop},
		}},
	}}
	engine, err := New(Options{
		Provider: providerRuntime, Route: testRoute(t), Tools: registry,
		Security: security, Workspace: t.TempDir(), MaxOutputTokens: 128,
		Authorize: func(provider.ToolCall) bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.RunForTurn(t.Context(), "t1", "first", nil); err != nil {
		t.Fatal(err)
	}
	security.Permission = policy.PermissionSuggest
	var approvals int
	_, err = engine.RunForTurn(t.Context(), "t2", "second", func(event Event) error {
		if event.State == AwaitingApproval && event.Approval != nil {
			approvals++
			_ = mustControl(t, engine).ResolveApproval(toolguard.ApprovalDecision{
				RequestID: event.Approval.RequestID, Approved: true,
				Scope: policy.ApprovalOnce, ExpiresAt: time.Now().Add(time.Minute),
			})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if approvals != 1 {
		t.Fatalf("second turn approvals = %d, want 1 under suggest", approvals)
	}
}

type turnWriteTool struct{}

func (turnWriteTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "write", Description: "test write", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityWrite, AccessMode: tool.AccessWrite,
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "file", Field: "path", Access: tool.AccessWrite,
		}}},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":  map[string]any{"type": "string"},
				"value": map[string]any{"type": "string"},
			},
			"required": []string{"path", "value"}, "additionalProperties": false,
		},
	}
}

func (turnWriteTool) Execute(_ context.Context, raw json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: string(raw)}, nil
}

type turnContextBackend struct{}

func (turnContextBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "test", Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (turnContextBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	return command, nil
}
