package engine

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

// Engine used to allocate a Guard without OnNetworkAllow while wire kept the
// callback on a different Guard instance. Approvals then succeeded but the
// session Gate never learned the host, so the retry still got egress denied.
// Under Bypass, mid-flight hosts auto-grant (no ask); this still proves the
// engine-owned Guard owns OnNetworkAllow.
func TestAllocatedGuardGrantsEgressAfterApproval(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	executor := &egressRetryTool{}
	if err := registry.Register(executor, nil); err != nil {
		t.Fatal(err)
	}

	var grantedMu sync.Mutex
	var granted []string

	engine, err := New(Options{
		Provider: &scriptedProvider{}, Route: testRoute(t), Tools: registry,
		Workspace: t.TempDir(),
		Security:  policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass),
		OnNetworkAllow: func(host, _ string) {
			grantedMu.Lock()
			granted = append(granted, host)
			grantedMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, execErr := engine.guard.Execute(
		context.Background(), "call-egress", "web_fetch",
		json.RawMessage(`{"url":"https://example.com/page"}`),
	)
	if execErr != nil {
		t.Fatal(execErr)
	}
	if result.IsError || result.Content != `{"ok":true}` {
		t.Fatalf("result = %+v", result)
	}
	if executor.calls.Load() != 2 {
		t.Fatalf("calls = %d, want retry after grant", executor.calls.Load())
	}
	grantedMu.Lock()
	defer grantedMu.Unlock()
	found := false
	for _, host := range granted {
		if host == "cdn.example" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("OnNetworkAllow never saw cdn.example: %v", granted)
	}
}

type egressRetryTool struct {
	calls atomic.Int32
}

func (e *egressRetryTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "web_fetch", Description: "test fetch", Visibility: tool.VisibleModel,
		Capability: tool.CapabilityNetwork, AccessMode: tool.AccessRead,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "url", Field: "url", Access: tool.AccessRead,
		}}},
		ParallelPolicy: tool.ParallelConcurrent, SandboxRequirement: tool.SandboxNone,
		Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "minLength": 1},
			},
			"required": []string{"url"}, "additionalProperties": false,
		},
	}
}

func (e *egressRetryTool) Execute(context.Context, json.RawMessage) (tool.Result, error) {
	if e.calls.Add(1) == 1 {
		return tool.Result{
			Content: "egress denied · host=cdn.example", IsError: true,
			Metadata: map[string]any{
				"error_category": "egress_denied", "host": "cdn.example",
				"protocol": "https", "status_code": 0,
			},
			Outcome: &tool.Outcome{
				Status: tool.OutcomeFailed,
				Security: &tool.SecuritySignal{
					EgressDenied: &tool.NetworkTarget{
						Host: "cdn.example", Protocol: "https",
					},
				},
			},
		}, nil
	}
	return tool.Result{Content: `{"ok":true}`}, nil
}
